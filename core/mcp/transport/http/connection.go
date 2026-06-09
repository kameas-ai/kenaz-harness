package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// Spec is the per-recipe configuration the HTTP Connection consumes.
// It mirrors transport.SpawnSpec for the universal subset (ID,
// timeouts) and adds HTTP-specific fields. The Pool builds a Spec
// per recipe at openOne time and hands it to NewConnection.
type Spec struct {
	// ID identifies the server in logs and on Tool.Server. Mirrors
	// SpawnSpec.ID.
	ID string

	// URL is the JSON-RPC POST endpoint. Required. Subject to
	// ${VAR} env-var substitution at Open time so credentials in
	// the path source from the keychain rather than the recipe
	// YAML.
	URL string

	// HeadersTemplate is the per-request header set. Values run
	// through env-var substitution at Open time.
	HeadersTemplate map[string]string

	// Env is the resolved env-var map used to substitute ${VAR}
	// tokens in URL and HeadersTemplate. Populated by the pool
	// from recipes.ResolveEnv.
	Env map[string]string

	// FirstByteTimeout caps the wait for the first response's
	// headers after a connection is established. Falls back to
	// transport.DefaultFirstByteTimeout when zero.
	FirstByteTimeout time.Duration

	// InitTimeout is the response deadline once initialize is on
	// the wire. Falls back to transport.DefaultInitTimeout when
	// zero.
	InitTimeout time.Duration

	// PingPeriod overrides transport.DefaultPingPeriod when > 0.
	// Negative disables health pings entirely.
	PingPeriod time.Duration

	// PingTimeout overrides transport.DefaultPingTimeout when > 0.
	PingTimeout time.Duration

	// HTTPClient is the *http.Client the Connection uses for
	// every outbound POST. Nil → a fresh client with a default
	// transport (no auto-redirect on 3xx responses, since MCP
	// servers do not legitimately 3xx a JSON-RPC POST). Tests
	// inject httptest.Server.Client() here.
	HTTPClient *stdhttp.Client
}

// Connection is the HTTP implementation of transport.Connection.
// Each instance owns one *http.Client + one inbound queue + one
// goroutine pool servicing in-flight POSTs.
//
// JSON-RPC over POST is fundamentally request/response, but the
// Connection abstraction treats Send/Recv as independent half-
// duplex channels (so the same Framer-style reader loop above
// stdio drives HTTP without modification). The Connection bridges
// the gap by dispatching every Send onto a goroutine that POSTs
// the envelope, parses the response (which is itself a JSON-RPC
// envelope), and pushes the response onto the inbound queue Recv
// drains.
//
// Lifecycle:
//
//   - NewConnection captures the Spec; nothing has been allocated.
//   - Open resolves URL/HeadersTemplate substitution, validates
//     the resulting URL one more time, and prepares the
//     inbound-queue plumbing. No network I/O happens at Open —
//     the first Send drives the first request. This matches the
//     "lazy" HTTP transport pattern in the MCP spec and keeps
//     Open cheap (so a Pool with 100 HTTP recipes does not fan
//     out 100 simultaneous POSTs at boot).
//   - Send starts a goroutine that POSTs the envelope and parses
//     the response. Concurrent Send is safe.
//   - Recv blocks on the inbound queue, returning io.EOF when
//     Close fires.
//   - Close cancels every in-flight POST, drains the queue, and
//     marks the connection done. Idempotent.
type Connection struct {
	spec   Spec
	logger ConnectionLogger

	// resolved holds the post-substitution URL + headers. Captured
	// at Open so a runtime keychain rotation does not see a
	// half-applied state mid-request. Restart re-runs Open and
	// produces a fresh resolved set.
	resolvedURL     string
	resolvedHeaders *HeaderTemplate

	// rootCtx is the parent context every in-flight request derives
	// from. Cancelled by Close so all pending requests abort within
	// 100ms (response-body close is the slowest path; the cancel
	// itself is immediate).
	rootCtx    context.Context
	rootCancel context.CancelFunc

	// inboundCh is the queue Recv reads from. Buffered so the
	// dispatch goroutine never blocks waiting for the reader on
	// completion of a single POST. Closed by Close.
	inboundCh chan transport.RawMessage

	// inflightWG tracks pending POST goroutines so Close blocks
	// until every request has either completed or aborted.
	inflightWG sync.WaitGroup

	// errMu guards the rolling lastErrBody snapshot StderrTail
	// returns when callers want to surface a failed-POST error
	// payload through the existing diagnostic surface.
	errMu       sync.Mutex
	lastErrBody string

	mu       sync.Mutex
	opened   bool
	closed   atomic.Bool
	closeErr error
}

// ConnectionLogger is the minimal logger surface Connection uses to
// emit structured diagnostics. Same shape as the stdio package's
// ConnectionLogger so callers can pass *slog.Logger or nil.
type ConnectionLogger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}

// NewConnection builds an unopened HTTP Connection bound to spec.
// Logger may be nil — the noop implementation silences diagnostics.
func NewConnection(spec Spec, logger ConnectionLogger) *Connection {
	if logger == nil {
		logger = noopLogger{}
	}
	return &Connection{
		spec:   spec,
		logger: logger,
	}
}

// compile-time witness that *Connection satisfies the cross-
// transport interface.
var _ transport.Connection = (*Connection)(nil)

// Open resolves URL + headers substitution, validates the resolved
// URL, and primes the inbound channel. Per the lazy-HTTP pattern,
// no network I/O happens here — the first Send drives the first
// POST.
func (c *Connection) Open(ctx context.Context) error {
	c.mu.Lock()
	if c.opened {
		c.mu.Unlock()
		return errors.New("http: connection already opened")
	}
	if strings.TrimSpace(c.spec.URL) == "" {
		c.mu.Unlock()
		return errors.New("http: empty URL")
	}
	c.opened = true
	spec := c.spec
	c.mu.Unlock()

	// Substitute env-var tokens in URL + headers. Use the recipes
	// package as the single source of truth for token shape so the
	// HTTP transport stays in lockstep with stdio's ArgsTemplate
	// substitution rules.
	resolvedURL := substituteURL(spec.URL, spec.Env)

	parsed, err := url.Parse(resolvedURL)
	if err != nil {
		return fmt.Errorf("http: invalid resolved URL %q: %w", resolvedURL, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("http: resolved URL %q has unsupported scheme", resolvedURL)
	}
	if parsed.Host == "" {
		return fmt.Errorf("http: resolved URL %q has no host", resolvedURL)
	}

	headers := NewHeaderTemplate(spec.HeadersTemplate)
	headers.Resolve(spec.Env)

	if spec.HTTPClient == nil {
		// Per FR-XXX (HTTP transport bring-up): a per-Connection
		// client lets Close cleanly tear down the underlying
		// transport's idle keep-alives without touching a shared
		// http.DefaultTransport that other code paths depend on.
		spec.HTTPClient = &stdhttp.Client{
			// Don't follow redirects — JSON-RPC POST endpoints
			// should not 3xx, and following one quietly would mask
			// a misconfigured base URL.
			CheckRedirect: func(*stdhttp.Request, []*stdhttp.Request) error {
				return stdhttp.ErrUseLastResponse
			},
		}
	}

	c.mu.Lock()
	c.resolvedURL = resolvedURL
	c.resolvedHeaders = headers
	c.spec.HTTPClient = spec.HTTPClient
	c.rootCtx, c.rootCancel = context.WithCancel(context.Background())
	// Buffer holds 16 envelopes — well above the steady-state
	// concurrent-POST count for any single recipe; the pool sees
	// 1-2 in-flight at peak. Larger buffer would just delay back-
	// pressure detection.
	c.inboundCh = make(chan transport.RawMessage, 16)
	c.mu.Unlock()

	return nil
}

// Send marshals v as a JSON-RPC envelope and dispatches a POST. The
// goroutine pushes the parsed response onto the inbound queue. Send
// itself returns once the request has been launched — it does not
// block on the HTTP round-trip, so concurrent Send is cheap.
func (c *Connection) Send(v any) error {
	if c.closed.Load() {
		return errors.New("http: connection closed")
	}
	c.mu.Lock()
	if !c.opened {
		c.mu.Unlock()
		return errors.New("http: connection not open")
	}
	resolvedURL := c.resolvedURL
	headers := c.resolvedHeaders
	httpClient := c.spec.HTTPClient
	rootCtx := c.rootCtx
	c.mu.Unlock()

	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("http: marshal envelope: %w", err)
	}

	c.inflightWG.Add(1)
	go c.dispatch(rootCtx, httpClient, resolvedURL, headers, body)
	return nil
}

// dispatch is the per-POST goroutine. It owns one HTTP round-trip
// from request build through response parse + queue-push. Errors
// surface as a fabricated -32603 response on the inbound queue so
// the JSON-RPC layer above can fail the in-flight call cleanly
// instead of hanging forever.
func (c *Connection) dispatch(rootCtx context.Context, client *stdhttp.Client, endpoint string, headers *HeaderTemplate, body []byte) {
	defer c.inflightWG.Done()

	// Each request gets its own deadline derived from the spec
	// timeouts. We leave the deadline at "no explicit deadline" by
	// default — the request still aborts when rootCtx is cancelled
	// (Close path), and the JSON-RPC layer above us applies its
	// own per-call deadline through the framer's reader-side ctx.
	req, err := stdhttp.NewRequestWithContext(rootCtx, stdhttp.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		c.pushError(body, fmt.Errorf("build request: %w", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if headers != nil {
		headers.Apply(req.Header)
	}

	c.logger.Debug("http.request",
		"server", c.spec.ID,
		"url", endpoint,
		"headers", RedactHeaders(headersToMap(req.Header)),
	)

	resp, err := client.Do(req)
	if err != nil {
		c.pushError(body, err)
		return
	}
	defer func() {
		// Drain + close so keep-alive can recycle the connection.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	respBody, err := readBody(resp)
	if err != nil {
		c.pushError(body, fmt.Errorf("read response body: %w", err))
		return
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface the body as the StderrTail snapshot for diagnostic
		// surfaces, but still push a structured -32603 envelope onto
		// the inbound queue keyed by the same id the request carried
		// (so the router cancels the right call).
		c.recordErrBody(string(respBody))
		c.pushHTTPError(body, resp.StatusCode, respBody)
		return
	}

	var msg transport.RawMessage
	if err := json.Unmarshal(respBody, &msg); err != nil {
		c.pushError(body, fmt.Errorf("decode response: %w", err))
		return
	}
	if msg.JSONRPC == "" {
		msg.JSONRPC = transport.JSONRPCVersion
	}

	c.pushInbound(msg)
}

// pushInbound attempts to send msg onto the inbound queue without
// blocking past Close. When Close fires concurrently, the goroutine
// drops the message rather than leaking on a closed channel.
func (c *Connection) pushInbound(msg transport.RawMessage) {
	c.mu.Lock()
	ch := c.inboundCh
	rootCtx := c.rootCtx
	c.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- msg:
	case <-rootCtx.Done():
	}
}

// pushError fabricates a JSON-RPC error envelope and pushes it onto
// the inbound queue so the in-flight call wakes up. We extract the
// id from the outbound body so the router maps the response back to
// the original request rather than nudging an unrelated call awake.
func (c *Connection) pushError(reqBody []byte, cause error) {
	id := extractID(reqBody)
	msg := transport.RawMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Error: &transport.RPCError{
			Code:    transport.ErrCodeInternalError,
			Message: cause.Error(),
		},
	}
	c.recordErrBody(cause.Error())
	c.pushInbound(msg)
}

// pushHTTPError surfaces a 4xx/5xx as a JSON-RPC error envelope so
// the in-flight call observes a structured failure instead of a
// hang. Includes the HTTP status text in the message; the body
// (which may carry sensitive data) lands only in the StderrTail
// snapshot.
func (c *Connection) pushHTTPError(reqBody []byte, status int, _ []byte) {
	id := extractID(reqBody)
	msg := transport.RawMessage{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Error: &transport.RPCError{
			Code:    transport.ErrCodeInternalError,
			Message: fmt.Sprintf("HTTP %d %s", status, stdhttp.StatusText(status)),
		},
	}
	c.pushInbound(msg)
}

// Recv blocks for the next inbound envelope. Returns io.EOF when
// Close fires; the channel close synchronises with the cancelled
// rootCtx so the reader does not race a half-closed queue.
func (c *Connection) Recv() (transport.RawMessage, error) {
	c.mu.Lock()
	ch := c.inboundCh
	c.mu.Unlock()
	if ch == nil {
		return transport.RawMessage{}, io.EOF
	}
	msg, ok := <-ch
	if !ok {
		return transport.RawMessage{}, io.EOF
	}
	return msg, nil
}

// Close cancels every in-flight POST, drains the dispatch
// goroutines, and closes the inbound queue. Idempotent — repeat
// calls return the first close's error.
func (c *Connection) Close() error {
	if c.closed.Load() {
		return c.closeErr
	}
	c.mu.Lock()
	if c.closed.Load() {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed.Store(true)
	cancel := c.rootCancel
	ch := c.inboundCh
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Wait for every dispatch goroutine to finish — bounded by
	// the cancellation propagation window (transport.CloseGrace).
	doneCh := make(chan struct{})
	go func() {
		c.inflightWG.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(transport.CloseGrace):
		// Hard timeout: in-flight goroutines blocked in DNS or a
		// slow response-body close get a chance to exit, but we
		// don't hang Close forever waiting for them.
	}

	c.mu.Lock()
	if ch != nil {
		close(ch)
	}
	c.inboundCh = nil
	c.mu.Unlock()
	return nil
}

// PID returns 0 — HTTP connections do not wrap a child process.
func (c *Connection) PID() int { return 0 }

// StderrTail returns the most recent error-body snapshot, capped at
// maxBytes. Empty when no error has occurred. The snapshot is the
// HTTP transport's analogue of stdio's child-stderr ring buffer:
// status surfaces and Test-Connection RPCs render the same field
// regardless of transport, so we surface the most recent failed-
// POST body rather than leaving callers with "".
func (c *Connection) StderrTail(maxBytes int) string {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if maxBytes <= 0 || len(c.lastErrBody) <= maxBytes {
		return c.lastErrBody
	}
	return c.lastErrBody[len(c.lastErrBody)-maxBytes:]
}

// recordErrBody captures the most recent error-body snapshot. The
// body may contain sensitive data (server error pages echo the
// Authorization header in some misconfigurations) — the snapshot is
// only surfaced through StderrTail, never through slog.
func (c *Connection) recordErrBody(body string) {
	c.errMu.Lock()
	c.lastErrBody = body
	c.errMu.Unlock()
}

// substituteURL applies the recipes package's scalar token-
// substitution to the URL field. Imported lazily to avoid a hard
// dependency cycle in the transport package proper.
func substituteURL(raw string, env map[string]string) string {
	// Local copy of the substitution rule: recipes.SubstituteString
	// is the canonical entry point but the package-import cycle
	// guard means we re-implement the scalar walk here against the
	// same regex shape. (recipes imports transport-style packages
	// only via the indirect substitution path; we keep that
	// boundary clean.)
	return scalarSubstitute(raw, env)
}

// scalarSubstitute mirrors recipes.SubstituteString without
// importing the recipes package — keeps the http transport
// independent of catalog data structures.
func scalarSubstitute(s string, vars map[string]string) string {
	if s == "" {
		return s
	}
	out := make([]byte, 0, len(s))
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '$' && s[i+1] == '{' {
			j := i + 2
			for j < len(s) && s[j] != '}' {
				j++
			}
			if j < len(s) {
				name := s[i+2 : j]
				if v, ok := vars[name]; ok {
					out = append(out, v...)
					i = j + 1
					continue
				}
			}
		}
		out = append(out, s[i])
		i++
	}
	return string(out)
}

// extractID unmarshals just the id field of a JSON-RPC envelope so
// pushError can fabricate a response keyed to the right call. nil
// id means the outbound was a notification (no response expected),
// in which case the fabricated envelope still surfaces but the
// router will drop it cleanly.
func extractID(body []byte) *json.RawMessage {
	var probe struct {
		ID *json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil
	}
	return probe.ID
}

// readBody reads the entire response body. Caller is responsible
// for closing resp.Body after this returns.
func readBody(resp *stdhttp.Response) ([]byte, error) {
	const maxBody = 4 * 1024 * 1024
	limited := io.LimitReader(resp.Body, maxBody+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBody)
	}
	return body, nil
}

// headersToMap projects an http.Header onto a flat map[string]string
// for log redaction. Multi-valued headers fold to the first value;
// the redaction logger does not need value granularity.
func headersToMap(h stdhttp.Header) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			out[k] = vs[0]
		}
	}
	return out
}

// ConnectionFactory builds an HTTP Connection bound to a recipe
// id. Mirrors stdio.ConnectionFactory so the cross-transport
// supervisor can hold a slice of factories without branching on
// transport.
type ConnectionFactory struct {
	Spec   Spec
	Logger ConnectionLogger
}

// NewConnection returns a fresh, unopened Connection bound to
// f.Spec. The id parameter overrides f.Spec.ID when the latter is
// empty so the same factory can dispatch by recipe id in the
// multi-recipe pool.
func (f *ConnectionFactory) NewConnection(id string) (transport.Connection, error) {
	if strings.TrimSpace(f.Spec.URL) == "" {
		return nil, errors.New("http: factory spec has empty URL")
	}
	spec := f.Spec
	if spec.ID == "" {
		spec.ID = id
	}
	return NewConnection(spec, f.Logger), nil
}

// compile-time witness for the cross-transport factory contract.
var _ transport.ConnectionFactory = (*ConnectionFactory)(nil)
