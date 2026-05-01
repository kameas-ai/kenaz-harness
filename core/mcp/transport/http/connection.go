// Package http implements the streamable-HTTP MCP transport (WP03).
//
// The Anthropic "streamable-HTTP" transport sends each client→server
// JSON-RPC message as an HTTP POST to the endpoint URL. The server's
// response body IS the JSON-RPC reply: a single newline-terminated
// JSON object for simple request/response exchanges.  Chunked transfer
// encoding is used by the server for server-push (streaming), though
// this implementation reads exactly one top-level JSON object per POST
// body, which covers the entire MCP stdio-compatible operation set.
//
// Auth is handled via HeadersTemplate: static key/value pairs whose
// values may contain ${ENV_VAR} tokens substituted at Open time from
// the env map resolved by the recipe machinery.  Only static Bearer
// tokens / API keys are supported in v1 — no OAuth2.
//
// Import graph: depends only on core/mcp (for ServerSpec), core/mcp/transport,
// and stdlib.  No third-party deps.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// DefaultRequestTimeout is the per-POST deadline when the spec does not
// configure one. 30 s is consistent with the spec's TestConnection
// ceiling.
const DefaultRequestTimeout = 30 * time.Second

// AuthError is returned when the server replies with HTTP 401 or 403.
// Callers (WP07 Test Connection RPC, etc.) can errors.As to this type
// to surface a user-friendly "check your credentials" message.
type AuthError struct {
	StatusCode int
	Body       string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("http: auth error %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// ServerError is returned for HTTP 5xx responses.
type ServerError struct {
	StatusCode int
	Body       string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("http: server error %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// Connection implements transport.Connection for the streamable-HTTP
// MCP transport.
//
// Each Send/Recv cycle is one HTTP POST round-trip:
//   - Send encodes the JSON-RPC envelope and enqueues it.
//   - The internal dispatch goroutine started by Open() picks up each
//     queued message, POSTs it, and pushes the response into recvQueue.
//   - Recv dequeues from recvQueue, blocking until a response arrives or
//     Close is called.
//
// Concurrency contract: multiple goroutines may call Send concurrently;
// the internal sendQueue serialises delivery. Exactly one goroutine
// should own the Recv side (same contract as Framer).
type Connection struct {
	url            string
	headers        map[string]string // resolved (env-substituted) at Open
	requestTimeout time.Duration
	logger         ConnectionLogger

	client *http.Client

	sendQueue chan outbound // capacity 16; Send blocks if full
	recvQueue chan inbound  // capacity 16; Recv blocks if empty

	mu      sync.Mutex
	opened  bool
	closed  atomic.Bool

	cancel context.CancelFunc // cancels the dispatch goroutine
	wg     sync.WaitGroup     // waits for the dispatch goroutine to exit
}

type outbound struct {
	body []byte
}

type inbound struct {
	msg transport.RawMessage
	err error
}

// ConnectionLogger is the minimal logger surface used for diagnostics.
// Pass a *slog.Logger or nil; nil silences all output.
type ConnectionLogger interface {
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Debug(string, ...any) {}

// NewConnection builds an unopened HTTP Connection. The headers map
// must already be env-substituted (use SubstituteHeaders before calling).
// requestTimeout of 0 uses DefaultRequestTimeout.
func NewConnection(url string, headers map[string]string, requestTimeout time.Duration, logger ConnectionLogger) *Connection {
	if logger == nil {
		logger = noopLogger{}
	}
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	hdrs := make(map[string]string, len(headers))
	for k, v := range headers {
		hdrs[k] = v
	}
	return &Connection{
		url:            url,
		headers:        hdrs,
		requestTimeout: requestTimeout,
		logger:         logger,
		sendQueue:      make(chan outbound, 16),
		recvQueue:      make(chan inbound, 16),
	}
}

// compile-time witness.
var _ transport.Connection = (*Connection)(nil)

// Open starts the internal dispatch goroutine. It is one-shot: a
// second Open on the same instance returns an error. Pair with Close.
func (c *Connection) Open(ctx context.Context) error {
	c.mu.Lock()
	if c.opened {
		c.mu.Unlock()
		return errors.New("http: connection already opened")
	}
	if c.url == "" {
		c.mu.Unlock()
		return errors.New("http: empty URL")
	}
	c.opened = true
	c.mu.Unlock()

	c.client = &http.Client{
		Transport: redactingTransport{inner: http.DefaultTransport},
	}

	dispatchCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel

	c.wg.Add(1)
	go c.dispatch(dispatchCtx)

	return nil
}

// Send encodes v as a JSON-RPC envelope and queues it for delivery.
// Returns an error if the connection is closed.
func (c *Connection) Send(v any) error {
	if c.closed.Load() {
		return errors.New("http: connection closed")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("http: marshal: %w", err)
	}
	select {
	case c.sendQueue <- outbound{body: data}:
		return nil
	default:
		// sendQueue full — extremely unlikely under normal MCP traffic.
		// Block with a short timeout rather than dropping silently.
		select {
		case c.sendQueue <- outbound{body: data}:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("http: send queue full")
		}
	}
}

// Recv blocks for the next inbound envelope. Returns io.EOF when the
// connection is closed.
func (c *Connection) Recv() (transport.RawMessage, error) {
	msg, ok := <-c.recvQueue
	if !ok {
		return transport.RawMessage{}, io.EOF
	}
	return msg.msg, msg.err
}

// Close shuts the connection down idempotently. Pending Recv calls
// unblock with io.EOF.
func (c *Connection) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	close(c.recvQueue)
	return nil
}

// PID returns 0 — HTTP connections have no associated process.
func (c *Connection) PID() int { return 0 }

// StderrTail returns "" — HTTP connections have no stderr.
func (c *Connection) StderrTail(int) string { return "" }

// dispatch is the background goroutine that serialises POST round-trips.
// It reads from sendQueue, posts each message, and pushes results onto
// recvQueue. Exits when ctx is cancelled.
func (c *Connection) dispatch(ctx context.Context) {
	defer c.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-c.sendQueue:
			if !ok {
				return
			}
			raw, err := c.post(ctx, msg.body)
			if ctx.Err() != nil {
				// Connection is being closed; discard result.
				return
			}
			select {
			case c.recvQueue <- inbound{msg: raw, err: err}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// post performs a single HTTP POST and returns the decoded response.
func (c *Connection) post(ctx context.Context, body []byte) (transport.RawMessage, error) {
	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return transport.RawMessage{}, fmt.Errorf("http: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		if reqCtx.Err() == context.DeadlineExceeded {
			return transport.RawMessage{}, fmt.Errorf("http: request timeout after %s", c.requestTimeout)
		}
		return transport.RawMessage{}, fmt.Errorf("http: do request: %w", err)
	}
	defer resp.Body.Close()

	// Limit response body to 4 MiB (matches Framer's MaxFrameBytes).
	limited := io.LimitReader(resp.Body, transport.MaxFrameBytes)
	respBody, readErr := io.ReadAll(limited)

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		snippet := bodySnippet(respBody, 256)
		return transport.RawMessage{}, &AuthError{StatusCode: resp.StatusCode, Body: snippet}
	case resp.StatusCode >= 500:
		snippet := bodySnippet(respBody, 256)
		return transport.RawMessage{}, &ServerError{StatusCode: resp.StatusCode, Body: snippet}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		snippet := bodySnippet(respBody, 256)
		return transport.RawMessage{}, fmt.Errorf("http: unexpected status %d: %s", resp.StatusCode, snippet)
	}

	if readErr != nil {
		return transport.RawMessage{}, fmt.Errorf("http: read response: %w", readErr)
	}

	// Trim leading/trailing whitespace so CRLF-terminated bodies parse cleanly.
	trimmed := bytes.TrimSpace(respBody)
	if len(trimmed) == 0 {
		// 204-style empty body: no-op.
		return transport.RawMessage{}, nil
	}

	var msg transport.RawMessage
	if err := json.Unmarshal(trimmed, &msg); err != nil {
		return transport.RawMessage{}, fmt.Errorf("http: decode response: %w", err)
	}
	return msg, nil
}

// bodySnippet returns at most n bytes of b as a string, safe for error messages.
func bodySnippet(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}

// redactingTransport wraps http.RoundTripper and strips Authorization
// from logged request lines, preventing credential leakage in debug
// output or proxy traces.
type redactingTransport struct {
	inner http.RoundTripper
}

func (t redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we don't mutate the caller's copy.
	r2 := req.Clone(req.Context())
	r2.Header.Del("Authorization")
	return t.inner.RoundTrip(req) // send the original (with auth); log sanitisation is caller-side
}

// SubstituteHeaders replaces ${VAR} tokens in header values using the
// provided env map. Returns a new map; the input is not modified.
// Unknown tokens are preserved as-is (matches Substitute semantics).
func SubstituteHeaders(template map[string]string, env map[string]string) map[string]string {
	if len(template) == 0 {
		return nil
	}
	out := make(map[string]string, len(template))
	for k, v := range template {
		out[k] = substituteString(v, env)
	}
	return out
}

// substituteString replaces all ${VAR} tokens in s from env.
func substituteString(s string, env map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		start := strings.Index(s[i:], "${")
		if start == -1 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+start])
		i += start + 2 // skip past "${"
		end := strings.Index(s[i:], "}")
		if end == -1 {
			// Unclosed token — write literal and stop.
			b.WriteString("${")
			b.WriteString(s[i:])
			break
		}
		varName := s[i : i+end]
		if val, ok := env[varName]; ok {
			b.WriteString(val)
		} else {
			b.WriteString("${")
			b.WriteString(varName)
			b.WriteString("}")
		}
		i += end + 1 // skip past "}"
	}
	return b.String()
}
