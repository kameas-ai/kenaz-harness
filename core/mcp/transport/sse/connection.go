// Package sse hosts the SSE (Server-Sent Events) transport
// implementation of transport.Connection. It keeps a long-lived GET
// stream open to Recipe.URL for inbound JSON-RPC events and POSTs
// client→server messages to Recipe.PostURL.
//
// The SSE connection is structurally similar to the HTTP transport
// (both are HTTP-based) but differs in a key way: inbound messages
// arrive as a persistent chunked event-stream rather than as
// individual POST responses. The connection opens the stream once at
// Open time and demuxes `data:` lines through an internal queue Recv
// drains, while Send dispatches outbound envelopes as individual
// POSTs to PostURL.
//
// Reconnect on stream EOF mirrors the stdio supervisor's restart-
// history backoff (transport.BackoffSchedule / transport.RestartWindow).
// See reconnect.go for the reconnect loop.
package sse

import (
	"bufio"
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

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// Spec is the per-recipe configuration the SSE Connection consumes.
// It mirrors the HTTP transport's Spec for the shared subset and adds
// SSE-specific fields (PostURL for the client→server channel).
type Spec struct {
	// ID identifies the server in logs and on Tool.Server.
	ID string

	// URL is the SSE stream endpoint. The Connection opens a long-
	// lived GET here with Accept: text/event-stream.
	URL string

	// PostURL is the client→server endpoint. Each Send POSTs a
	// JSON-RPC envelope here and expects a 200/202 ACK. It is the
	// SSE transport's write channel — distinct from the GET stream
	// which is read-only from the client's perspective.
	PostURL string

	// HeadersTemplate is the per-request header set applied to both
	// the SSE GET and every outbound POST. Values run through ${VAR}
	// env-var substitution at Open time.
	HeadersTemplate map[string]string

	// Env is the resolved env-var map used to substitute ${VAR}
	// tokens in URL, PostURL, and HeadersTemplate.
	Env map[string]string

	// FirstByteTimeout caps the wait for the first SSE event after
	// the stream is opened. Falls back to
	// transport.DefaultFirstByteTimeout when zero.
	FirstByteTimeout time.Duration

	// InitTimeout is the response deadline once initialize is on the
	// wire. Falls back to transport.DefaultInitTimeout when zero.
	InitTimeout time.Duration

	// PingPeriod overrides transport.DefaultPingPeriod when > 0.
	// Negative disables health pings entirely.
	PingPeriod time.Duration

	// PingTimeout overrides transport.DefaultPingTimeout when > 0.
	PingTimeout time.Duration

	// HTTPClient is the *http.Client used for all outbound requests
	// (both the SSE GET and each POST). Nil → a per-Connection
	// client. Tests inject httptest.Server.Client() here.
	HTTPClient *stdhttp.Client
}

// Connection is the SSE implementation of transport.Connection.
// Each instance opens one long-lived GET stream at Spec.URL and
// dispatches POSTs to Spec.PostURL.
//
// Lifecycle:
//
//   - NewConnection captures the Spec; nothing has been allocated.
//   - Open resolves URL/PostURL/HeadersTemplate substitution, opens
//     the SSE stream, and starts the scanner goroutine that pushes
//     `data:` events onto the inbound queue.
//   - Send marshals v and POSTs it to PostURL. Concurrent Send is safe.
//   - Recv blocks on the inbound queue. Returns io.EOF when the
//     stream has been closed.
//   - Close cancels the root context (which tears down the SSE GET
//     and any in-flight POSTs), waits for the scanner goroutine to
//     exit, and closes the inbound queue.
type Connection struct {
	spec   Spec
	logger ConnectionLogger

	// resolved holds post-substitution URL/PostURL/headers.
	resolvedURL     string
	resolvedPostURL string
	resolvedHeaders map[string]string

	// rootCtx is the parent context every outbound HTTP request
	// derives from. Cancelled by Close.
	rootCtx    context.Context
	rootCancel context.CancelFunc

	// inboundCh is the queue Recv reads from. Events pushed by the
	// scanner goroutine; closed by Close.
	inboundCh chan transport.RawMessage

	// scanWG tracks the scanner goroutine so Close can drain it.
	scanWG sync.WaitGroup

	// streamBody is the SSE response body the scanner reads from.
	// Held so Close can call Close() on it directly — context cancel
	// alone is not always sufficient to unblock a bufio.Scanner that
	// is parked inside a chunked-transfer Read on the underlying
	// connection. Closing the body forces the Read to return EOF.
	streamBody io.ReadCloser

	// errMu guards the rolling lastErrBody snapshot.
	errMu       sync.Mutex
	lastErrBody string

	mu       sync.Mutex
	opened   bool
	closed   atomic.Bool
	closeErr error
}

// ConnectionLogger is the minimal logger surface Connection uses for
// structured diagnostics. Pass a *slog.Logger or nil; nil silences.
type ConnectionLogger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}

// NewConnection builds an unopened SSE Connection bound to spec.
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

// Open resolves URL/PostURL/headers substitution, validates the
// resolved URLs, opens the long-lived SSE stream, and starts the
// scanner goroutine. After Open returns nil, Send/Recv are usable.
func (c *Connection) Open(ctx context.Context) error {
	c.mu.Lock()
	if c.opened {
		c.mu.Unlock()
		return errors.New("sse: connection already opened")
	}
	if strings.TrimSpace(c.spec.URL) == "" {
		c.mu.Unlock()
		return errors.New("sse: empty URL")
	}
	if strings.TrimSpace(c.spec.PostURL) == "" {
		c.mu.Unlock()
		return errors.New("sse: empty PostURL")
	}
	c.opened = true
	spec := c.spec
	c.mu.Unlock()

	// Resolve ${VAR} tokens in URL, PostURL, and headers.
	resolvedURL := recipes.SubstituteString(spec.URL, spec.Env)
	resolvedPostURL := recipes.SubstituteString(spec.PostURL, spec.Env)

	if err := validateURL(resolvedURL); err != nil {
		return fmt.Errorf("sse: invalid URL %q: %w", resolvedURL, err)
	}
	if err := validateURL(resolvedPostURL); err != nil {
		return fmt.Errorf("sse: invalid PostURL %q: %w", resolvedPostURL, err)
	}

	resolvedHeaders := recipes.SubstituteHeaders(spec.HeadersTemplate, spec.Env)

	httpClient := spec.HTTPClient
	if httpClient == nil {
		httpClient = &stdhttp.Client{
			CheckRedirect: func(*stdhttp.Request, []*stdhttp.Request) error {
				return stdhttp.ErrUseLastResponse
			},
		}
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	inboundCh := make(chan transport.RawMessage, 64)

	c.mu.Lock()
	c.resolvedURL = resolvedURL
	c.resolvedPostURL = resolvedPostURL
	c.resolvedHeaders = resolvedHeaders
	c.spec.HTTPClient = httpClient
	c.rootCtx = rootCtx
	c.rootCancel = rootCancel
	c.inboundCh = inboundCh
	c.mu.Unlock()

	// Open the SSE stream.
	streamResp, err := c.openStream(rootCtx, httpClient, resolvedURL, resolvedHeaders)
	if err != nil {
		rootCancel()
		return fmt.Errorf("sse: open stream: %w", err)
	}

	// Start the scanner goroutine that demuxes `data:` lines.
	c.mu.Lock()
	c.streamBody = streamResp.Body
	c.mu.Unlock()
	c.scanWG.Add(1)
	go c.runScanner(streamResp.Body, inboundCh, rootCtx)

	return nil
}

// openStream issues the SSE GET request. Returns the response (whose
// Body the scanner goroutine will own) or an error.
func (c *Connection) openStream(ctx context.Context, client *stdhttp.Client, streamURL string, headers map[string]string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	c.logger.Debug("sse.stream.open",
		"server", c.spec.ID,
		"url", streamURL,
		"headers", redactHeaders(headersToMap(req.Header)),
	)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		_ = resp.Body.Close()
		c.recordErrBody(string(body))
		return nil, fmt.Errorf("SSE stream returned HTTP %d %s", resp.StatusCode, stdhttp.StatusText(resp.StatusCode))
	}
	return resp, nil
}

// runScanner reads lines from the SSE stream body, parses `data:`
// prefixed lines as JSON-RPC envelopes, and pushes them onto
// inboundCh. When the stream closes (body EOF or rootCtx cancel),
// the goroutine closes the inbound channel and exits.
func (c *Connection) runScanner(body io.ReadCloser, ch chan transport.RawMessage, rootCtx context.Context) {
	defer c.scanWG.Done()
	defer body.Close()
	defer func() {
		// Signal Recv that the stream is done.
		c.mu.Lock()
		if c.inboundCh == ch {
			close(ch)
			c.inboundCh = nil
		}
		c.mu.Unlock()
	}()

	scanner := bufio.NewScanner(body)
	// SSE lines can carry large JSON payloads. Cap at 4 MiB.
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			// SSE comments, event:, id:, retry: — skip.
			continue
		}
		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}

		var msg transport.RawMessage
		if err := json.Unmarshal([]byte(data), &msg); err != nil {
			c.logger.Warn("sse.scanner.decode_error",
				"server", c.spec.ID,
				"err", err.Error(),
			)
			continue
		}
		if msg.JSONRPC == "" {
			msg.JSONRPC = transport.JSONRPCVersion
		}

		select {
		case ch <- msg:
		case <-rootCtx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		c.recordErrBody(err.Error())
		c.logger.Warn("sse.scanner.error",
			"server", c.spec.ID,
			"err", err.Error(),
		)
	}
}

// Send marshals v as a JSON-RPC envelope and POSTs it to PostURL.
// Safe for concurrent use.
func (c *Connection) Send(v any) error {
	if c.closed.Load() {
		return errors.New("sse: connection closed")
	}
	c.mu.Lock()
	if !c.opened {
		c.mu.Unlock()
		return errors.New("sse: connection not open")
	}
	resolvedPostURL := c.resolvedPostURL
	resolvedHeaders := c.resolvedHeaders
	httpClient := c.spec.HTTPClient
	rootCtx := c.rootCtx
	c.mu.Unlock()

	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("sse: marshal envelope: %w", err)
	}

	req, err := stdhttp.NewRequestWithContext(rootCtx, stdhttp.MethodPost, resolvedPostURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("sse: build POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range resolvedHeaders {
		req.Header.Set(k, v)
	}

	c.logger.Debug("sse.post",
		"server", c.spec.ID,
		"url", resolvedPostURL,
		"headers", redactHeaders(headersToMap(req.Header)),
	)

	resp, err := httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return errors.New("sse: connection closed")
		}
		return fmt.Errorf("sse: POST: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024))
		c.recordErrBody(string(respBody))
		return fmt.Errorf("sse: POST returned HTTP %d %s", resp.StatusCode, stdhttp.StatusText(resp.StatusCode))
	}
	return nil
}

// Recv blocks for the next inbound envelope. Returns io.EOF when the
// stream has been closed or Close has fired.
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

// Close cancels the root context (which closes the SSE stream and any
// in-flight POSTs), waits for the scanner goroutine to drain, and
// marks the connection done. Idempotent.
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
	body := c.streamBody
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	// Closing the body forces a parked bufio.Scanner.Scan to return
	// EOF immediately. Context cancel alone propagates through the
	// underlying transport but does not always unblock a chunked-
	// transfer reader sitting in fill().
	if body != nil {
		_ = body.Close()
	}

	// Wait for the scanner goroutine. It will close inboundCh.
	doneCh := make(chan struct{})
	go func() {
		c.scanWG.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(transport.CloseGrace):
	}
	return nil
}

// PID returns 0 — SSE connections do not wrap a child process.
func (c *Connection) PID() int { return 0 }

// StderrTail returns the most recent error-body snapshot, capped at
// maxBytes. Empty when no error has occurred. Analogous to the HTTP
// transport's StderrTail: the SSE transport surfaces the most recent
// failed-POST body or stream-error message rather than "".
func (c *Connection) StderrTail(maxBytes int) string {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if maxBytes <= 0 || len(c.lastErrBody) <= maxBytes {
		return c.lastErrBody
	}
	return c.lastErrBody[len(c.lastErrBody)-maxBytes:]
}

// recordErrBody captures the most recent error-body snapshot.
func (c *Connection) recordErrBody(body string) {
	c.errMu.Lock()
	c.lastErrBody = body
	c.errMu.Unlock()
}

// validateURL checks that the resolved URL has an http or https scheme
// and a non-empty host.
func validateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("host required")
	}
	return nil
}

// headersToMap projects an http.Header onto a flat map[string]string
// for log redaction.
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

// sensitiveHeaders is the canonical-cased set of header names the
// transport redacts from slog diagnostics. Matches the HTTP
// transport's SensitiveHeaders set.
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"x-api-key":           {},
	"cookie":              {},
	"proxy-authorization": {},
}

// redactHeaders returns a copy of headers with sensitive values
// replaced by "REDACTED".
func redactHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if _, ok := sensitiveHeaders[strings.ToLower(k)]; ok {
			out[k] = "REDACTED"
		} else {
			out[k] = v
		}
	}
	return out
}

// ConnectionFactory builds an SSE Connection bound to a Spec.
// Mirrors stdio.ConnectionFactory and http.ConnectionFactory so the
// cross-transport supervisor can hold a factory without knowing the
// transport.
type ConnectionFactory struct {
	Spec   Spec
	Logger ConnectionLogger
}

// NewConnection returns a fresh, unopened SSE Connection bound to
// f.Spec. The id parameter overrides f.Spec.ID when the latter is
// empty so the same factory can dispatch by recipe id in the multi-
// recipe pool.
func (f *ConnectionFactory) NewConnection(id string) (transport.Connection, error) {
	if strings.TrimSpace(f.Spec.URL) == "" {
		return nil, errors.New("sse: factory spec has empty URL")
	}
	if strings.TrimSpace(f.Spec.PostURL) == "" {
		return nil, errors.New("sse: factory spec has empty PostURL")
	}
	spec := f.Spec
	if spec.ID == "" {
		spec.ID = id
	}
	return NewConnection(spec, f.Logger), nil
}

// compile-time witness for the cross-transport factory contract.
var _ transport.ConnectionFactory = (*ConnectionFactory)(nil)
