// Package browser implements the kaneaz__browser built-in tool.
//
// # CDP client choice
//
// The brief (Phase 10 BRIEF.md) offers two implementation paths:
//
//   - github.com/chromedp/chromedp (high-level, manages its own process)
//   - Raw WebSocket + github.com/chromedp/cdproto types (thin client)
//
// We use a third, equivalent path: raw WebSocket (gorilla/websocket, already
// in go.mod) with hand-rolled JSON-RPC dispatch. This avoids introducing a new
// module dependency. The CDP protocol is simple enough that a thin client is
// the right abstraction for a single-connection, single-page use case.
//
// CDP over WebSocket is: send {id, method, params} JSON → receive {id, result}
// or {id, error} JSON. Event frames (no id) are discarded in v1.
//
// # Architecture
//
// The tool holds a single *cdpSession per harness session. On the first Call it
// connects to ws://localhost:9222/json/list, selects the first available page
// target (creating one if needed), and opens a WebSocket to that target's
// webSocketDebuggerUrl. Subsequent calls reuse the session. Concurrent calls
// are serialised by a mutex — one page per session in v1.
//
// The CDPEndpoint option defaults to "ws://localhost:9222". It can be overridden
// via the KANEAZ_CDP_ENDPOINT environment variable for tests and local dev.
package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// ── Tool constants ────────────────────────────────────────────────────────────

const (
	// ToolName is the namespaced tool identifier surfaced to the model.
	ToolName = "kaneaz__browser"

	// ToolDescription is the model-facing description.
	ToolDescription = "Drive a headless Chromium browser to navigate the web. " +
		"Supports navigate, click, extract_text, extract_html, screenshot, evaluate_js. " +
		"Each call operates on a single browser page (one page per workbench in v1)."

	// MaxTextBytes caps extract_text / extract_html responses.
	MaxTextBytes = 256 * 1024 // 256 KiB

	// MaxScreenshotBytes caps the pre-base64 screenshot data.
	MaxScreenshotBytes = 4 * 1024 * 1024 // 4 MiB

	// MaxEvalResultBytes caps evaluate_js results.
	MaxEvalResultBytes = 64 * 1024 // 64 KiB

	// MaxErrorMessageBytes caps error messages returned to the model.
	MaxErrorMessageBytes = 256 // 256 UTF-8 bytes, per FR-027

	// DefaultTimeoutMs is the default per-call timeout.
	DefaultTimeoutMs = 30_000
)

const inputSchema = `{
  "type": "object",
  "required": ["action"],
  "properties": {
    "action": {
      "type": "string",
      "enum": ["navigate", "click", "extract_text", "extract_html", "screenshot", "evaluate_js"]
    },
    "url":        { "type": "string",  "description": "For navigate." },
    "selector":   { "type": "string",  "description": "CSS selector for click/extract_*." },
    "expression": { "type": "string",  "description": "JS expression for evaluate_js." },
    "timeout_ms": { "type": "integer", "description": "Per-call timeout in ms. Default: 30000." }
  }
}`

// ── Error codes ───────────────────────────────────────────────────────────────

const (
	ErrCodeNavigationFailed  = "navigation_failed"
	ErrCodeSelectorNoMatch   = "selector_no_match"
	ErrCodeCDPDisconnect     = "cdp_disconnect"
	ErrCodeTimeout           = "timeout"
	ErrCodeJSEvalFailed      = "js_eval_failed"
	ErrCodeChromiumUnreach   = "chromium_unreachable"
)

// ToolError is the structured error returned to the model.
type ToolError struct {
	Code             string `json:"code"`
	MessageTruncated string `json:"message_truncated"`
}

// Options configures the Tool at construction.
type Options struct {
	// CDPEndpoint is the base URL of the CDP HTTP endpoint, e.g.
	// "ws://localhost:9222". The KANEAZ_CDP_ENDPOINT env var overrides this.
	CDPEndpoint string
	// DialTimeout is the timeout for establishing the WebSocket connection to
	// a CDP target. Default: 10 seconds.
	DialTimeout time.Duration
}

// Tool is the kaneaz__browser builtin. Safe for concurrent use.
type Tool struct {
	cdpEndpoint string
	dialTimeout time.Duration

	mu  sync.Mutex
	ses *cdpSession // nil until first call
}

// New constructs a browser Tool.
func New(opts Options) *Tool {
	ep := opts.CDPEndpoint
	if ep == "" {
		ep = "ws://localhost:9222"
	}
	if override := os.Getenv("KANEAZ_CDP_ENDPOINT"); override != "" {
		ep = override
	}
	dt := opts.DialTimeout
	if dt <= 0 {
		dt = 10 * time.Second
	}
	return &Tool{cdpEndpoint: ep, dialTimeout: dt}
}

// Name implements BuiltinTool.
func (t *Tool) Name() string { return ToolName }

// Description implements BuiltinTool.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema implements BuiltinTool.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// ── Call args / results ───────────────────────────────────────────────────────

type callArgs struct {
	Action     string `json:"action"`
	URL        string `json:"url"`
	Selector   string `json:"selector"`
	Expression string `json:"expression"`
	TimeoutMs  int    `json:"timeout_ms"`
}

// Call implements BuiltinTool. It serialises concurrent calls against the
// single page session (v1 one-page-at-a-time design).
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	var args callArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return toolError(ErrCodeCDPDisconnect, "invalid args: "+err.Error()), nil
	}

	timeoutMs := args.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = DefaultTimeoutMs
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	// Serialise concurrent calls.
	t.mu.Lock()
	defer t.mu.Unlock()

	sess, err := t.getSession(callCtx)
	if err != nil {
		return toolError(ErrCodeChromiumUnreach, err.Error()), nil
	}

	switch args.Action {
	case "navigate":
		return t.doNavigate(callCtx, sess, args)
	case "click":
		return t.doClick(callCtx, sess, args)
	case "extract_text":
		return t.doExtract(callCtx, sess, args, false)
	case "extract_html":
		return t.doExtract(callCtx, sess, args, true)
	case "screenshot":
		return t.doScreenshot(callCtx, sess)
	case "evaluate_js":
		return t.doEvalJS(callCtx, sess, args)
	default:
		return toolError(ErrCodeCDPDisconnect, "unknown action: "+args.Action), nil
	}
}

// getSession returns the cached cdpSession, connecting if necessary.
// Caller holds t.mu.
func (t *Tool) getSession(ctx context.Context) (*cdpSession, error) {
	if t.ses != nil && t.ses.alive() {
		return t.ses, nil
	}

	dialCtx, cancel := context.WithTimeout(ctx, t.dialTimeout)
	defer cancel()

	sess, err := newCDPSession(dialCtx, t.cdpEndpoint)
	if err != nil {
		return nil, err
	}
	t.ses = sess
	return sess, nil
}

// ── Action handlers ───────────────────────────────────────────────────────────

func (t *Tool) doNavigate(ctx context.Context, sess *cdpSession, args callArgs) (json.RawMessage, error) {
	if args.URL == "" {
		return toolError(ErrCodeNavigationFailed, "url is required for navigate"), nil
	}

	result, err := sess.call(ctx, "Page.navigate", map[string]any{
		"url": args.URL,
	})
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	// Page.navigate returns {frameId, loaderId, errorText?}.
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	_ = json.Unmarshal(result, &nav)
	if nav.ErrorText != "" {
		return toolError(ErrCodeNavigationFailed, nav.ErrorText), nil
	}

	// Wait for Page.loadEventFired.
	if err := sess.waitLoad(ctx); err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	// Retrieve final URL and title via Runtime.evaluate.
	finalURL, err := sess.evalString(ctx, "window.location.href")
	if err != nil {
		finalURL = args.URL
	}
	title, err := sess.evalString(ctx, "document.title")
	if err != nil {
		title = ""
	}

	return marshalResult(map[string]any{
		"status":    "ok",
		"final_url": finalURL,
		"title":     title,
	})
}

func (t *Tool) doClick(ctx context.Context, sess *cdpSession, args callArgs) (json.RawMessage, error) {
	if args.Selector == "" {
		return toolError(ErrCodeSelectorNoMatch, "selector is required for click"), nil
	}

	// Check element exists.
	existsExpr := fmt.Sprintf("document.querySelector(%q) !== null", args.Selector)
	exists, err := sess.evalBool(ctx, existsExpr)
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}
	if !exists {
		return toolError(ErrCodeSelectorNoMatch,
			fmt.Sprintf("selector %q matches zero elements", args.Selector)), nil
	}

	// Click via JS (synchronous, no coordinate resolution needed).
	clickExpr := fmt.Sprintf("document.querySelector(%q).click(); true", args.Selector)
	_, err = sess.evalBool(ctx, clickExpr)
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	return marshalResult(map[string]any{"status": "ok"})
}

func (t *Tool) doExtract(ctx context.Context, sess *cdpSession, args callArgs, html bool) (json.RawMessage, error) {
	var expr string
	if args.Selector == "" {
		if html {
			expr = "document.documentElement.outerHTML"
		} else {
			expr = "document.body ? document.body.innerText : document.documentElement.innerText"
		}
	} else {
		el := fmt.Sprintf("document.querySelector(%q)", args.Selector)
		exists, err := sess.evalBool(ctx, el+" !== null")
		if err != nil {
			return toolError(codeFromErr(err), err.Error()), nil
		}
		if !exists {
			return toolError(ErrCodeSelectorNoMatch,
				fmt.Sprintf("selector %q matches zero elements", args.Selector)), nil
		}
		if html {
			expr = el + ".outerHTML"
		} else {
			expr = el + ".innerText"
		}
	}

	raw, err := sess.evalString(ctx, expr)
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	truncated := false
	if len(raw) > MaxTextBytes {
		raw = raw[:MaxTextBytes]
		truncated = true
	}

	key := "text"
	if html {
		key = "html"
	}
	out := map[string]any{key: raw}
	if truncated {
		out["truncated"] = true
	}
	return marshalResult(out)
}

func (t *Tool) doScreenshot(ctx context.Context, sess *cdpSession) (json.RawMessage, error) {
	result, err := sess.call(ctx, "Page.captureScreenshot", map[string]any{
		"format":  "png",
		"quality": 80,
	})
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	var ss struct {
		Data string `json:"data"` // base64-encoded PNG
	}
	if err := json.Unmarshal(result, &ss); err != nil {
		return toolError(ErrCodeCDPDisconnect, "parse screenshot result: "+err.Error()), nil
	}

	// Decode to check size cap, then re-encode (or truncate before re-encoding
	// would corrupt PNG — we cap at the raw bytes level).
	raw, err := base64.StdEncoding.DecodeString(ss.Data)
	if err != nil {
		return toolError(ErrCodeCDPDisconnect, "decode screenshot: "+err.Error()), nil
	}
	if len(raw) > MaxScreenshotBytes {
		return toolError(ErrCodeCDPDisconnect,
			fmt.Sprintf("screenshot too large: %d bytes (cap %d)", len(raw), MaxScreenshotBytes)), nil
	}

	// We return the base64 string from CDP directly (it's already correct).
	return marshalResult(map[string]any{
		"base64_png": ss.Data,
	})
}

func (t *Tool) doEvalJS(ctx context.Context, sess *cdpSession, args callArgs) (json.RawMessage, error) {
	if args.Expression == "" {
		return toolError(ErrCodeJSEvalFailed, "expression is required for evaluate_js"), nil
	}

	result, err := sess.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":            args.Expression,
		"returnByValue":         true,
		"generatePreview":       false,
		"userGesture":           false,
		"awaitPromise":          false,
	})
	if err != nil {
		return toolError(codeFromErr(err), err.Error()), nil
	}

	var evalResult struct {
		Result struct {
			Type        string          `json:"type"`
			Value       json.RawMessage `json:"value"`
			Description string          `json:"description"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &evalResult); err != nil {
		return toolError(ErrCodeJSEvalFailed, "parse eval result: "+err.Error()), nil
	}
	if evalResult.ExceptionDetails != nil {
		return toolError(ErrCodeJSEvalFailed, evalResult.ExceptionDetails.Text), nil
	}

	resultJSON, err := json.Marshal(evalResult.Result.Value)
	if err != nil {
		return toolError(ErrCodeJSEvalFailed, "marshal eval result: "+err.Error()), nil
	}
	if len(resultJSON) > MaxEvalResultBytes {
		resultJSON = resultJSON[:MaxEvalResultBytes]
	}

	return marshalResult(map[string]any{
		"result": json.RawMessage(resultJSON),
	})
}

// ── CDP session ───────────────────────────────────────────────────────────────

type cdpSession struct {
	conn     *websocket.Conn
	mu       sync.Mutex
	nextID   atomic.Int64
	pending  map[int64]chan cdpResponse
	pendingMu sync.Mutex
	closed   atomic.Bool
}

type cdpResponse struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *cdpError       `json:"error"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// cdpTarget describes one entry from /json/list.
type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// newCDPSession connects to the CDP endpoint and returns an active session.
func newCDPSession(ctx context.Context, cdpEndpoint string) (*cdpSession, error) {
	// Convert ws:// → http:// for the /json/list REST endpoint.
	httpBase := strings.Replace(cdpEndpoint, "ws://", "http://", 1)
	httpBase = strings.Replace(httpBase, "wss://", "https://", 1)
	// Strip trailing path after the scheme+host; /json/list is always at root.
	// Find the scheme separator ("://") and skip past it to find the first "/".
	if schemeEnd := strings.Index(httpBase, "://"); schemeEnd >= 0 {
		rest := httpBase[schemeEnd+3:]
		if idx := strings.Index(rest, "/"); idx >= 0 {
			httpBase = httpBase[:schemeEnd+3+idx]
		}
	}

	// Retrieve targets list.
	listURL := httpBase + "/json/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build /json/list request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", listURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, fmt.Errorf("read /json/list: %w", err)
	}

	var targets []cdpTarget
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, fmt.Errorf("parse /json/list: %w", err)
	}

	// Find the first page target (or any if no page type exists).
	var target *cdpTarget
	for i := range targets {
		if targets[i].Type == "page" {
			target = &targets[i]
			break
		}
	}
	if target == nil && len(targets) > 0 {
		target = &targets[0]
	}

	// If no targets, create a new page.
	wsURL := ""
	if target != nil && target.WebSocketDebuggerURL != "" {
		wsURL = target.WebSocketDebuggerURL
	} else {
		// Create a new target.
		newURL := httpBase + "/json/new"
		req2, err := http.NewRequestWithContext(ctx, http.MethodPut, newURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build /json/new: %w", err)
		}
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			return nil, fmt.Errorf("PUT /json/new: %w", err)
		}
		defer resp2.Body.Close()
		var newTarget cdpTarget
		if err := json.NewDecoder(resp2.Body).Decode(&newTarget); err != nil {
			return nil, fmt.Errorf("parse /json/new response: %w", err)
		}
		wsURL = newTarget.WebSocketDebuggerURL
	}

	if wsURL == "" {
		return nil, fmt.Errorf("no WebSocket debugger URL available")
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dial CDP WebSocket %s: %w", wsURL, err)
	}

	sess := &cdpSession{
		conn:    conn,
		pending: make(map[int64]chan cdpResponse),
	}

	// Enable events.
	go sess.readLoop()

	// Enable Page events so we can wait for load.
	initCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := sess.call(initCtx, "Page.enable", nil); err != nil {
		// Non-fatal: navigate still works without load-event waiting.
		_ = err
	}

	return sess, nil
}

// alive reports whether the session is still connected.
func (s *cdpSession) alive() bool {
	return !s.closed.Load()
}

// readLoop drains incoming WebSocket frames and routes responses to
// pending callers. Event frames (no id field) are discarded in v1.
func (s *cdpSession) readLoop() {
	defer s.closed.Store(true)
	for {
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			// Connection closed; wake all pending callers.
			s.pendingMu.Lock()
			for id, ch := range s.pending {
				ch <- cdpResponse{ID: id, Error: &cdpError{Message: "cdp connection closed: " + err.Error()}}
				delete(s.pending, id)
			}
			s.pendingMu.Unlock()
			return
		}

		var frame struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(msg, &frame); err != nil || frame.ID == 0 {
			// Event frame or malformed — discard.
			continue
		}

		var resp cdpResponse
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}

		s.pendingMu.Lock()
		ch, ok := s.pending[frame.ID]
		if ok {
			delete(s.pending, frame.ID)
		}
		s.pendingMu.Unlock()

		if ok {
			ch <- resp
		}
	}
}

// call sends a CDP command and waits for the response.
func (s *cdpSession) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s.closed.Load() {
		return nil, fmt.Errorf("%s: session closed", ErrCodeCDPDisconnect)
	}

	id := s.nextID.Add(1)
	cmd := map[string]any{
		"id":     id,
		"method": method,
	}
	if params != nil {
		cmd["params"] = params
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal CDP command: %w", err)
	}

	ch := make(chan cdpResponse, 1)
	s.pendingMu.Lock()
	s.pending[id] = ch
	s.pendingMu.Unlock()

	s.mu.Lock()
	err = s.conn.WriteMessage(websocket.TextMessage, data)
	s.mu.Unlock()
	if err != nil {
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		return nil, fmt.Errorf("%s: write: %w", ErrCodeCDPDisconnect, err)
	}

	select {
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, id)
		s.pendingMu.Unlock()
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("%s: deadline exceeded calling %s", ErrCodeTimeout, method)
		}
		return nil, fmt.Errorf("%s: context cancelled calling %s", ErrCodeCDPDisconnect, method)
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s: CDP error %d: %s",
				ErrCodeJSEvalFailed, resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

// waitLoad waits for the Page.loadEventFired event by polling the document
// ready state. (We don't forward events in v1, so we poll instead.)
func (s *cdpSession) waitLoad(ctx context.Context) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		state, err := s.evalString(ctx, "document.readyState")
		if err != nil {
			return err
		}
		if state == "complete" || state == "interactive" {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s: page did not reach ready state within timeout", ErrCodeTimeout)
}

// evalString evaluates a JS expression and returns the string result.
func (s *cdpSession) evalString(ctx context.Context, expr string) (string, error) {
	result, err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "String(" + expr + ")",
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var eval struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &eval); err != nil {
		return "", fmt.Errorf("parse evalString result: %w", err)
	}
	if eval.ExceptionDetails != nil {
		return "", fmt.Errorf("%s: %s", ErrCodeJSEvalFailed, eval.ExceptionDetails.Text)
	}
	return eval.Result.Value, nil
}

// evalBool evaluates a JS expression and returns the boolean result.
func (s *cdpSession) evalBool(ctx context.Context, expr string) (bool, error) {
	result, err := s.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "Boolean(" + expr + ")",
		"returnByValue": true,
	})
	if err != nil {
		return false, err
	}
	var eval struct {
		Result struct {
			Value bool `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(result, &eval); err != nil {
		return false, fmt.Errorf("parse evalBool result: %w", err)
	}
	if eval.ExceptionDetails != nil {
		return false, fmt.Errorf("%s: %s", ErrCodeJSEvalFailed, eval.ExceptionDetails.Text)
	}
	return eval.Result.Value, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// toolError returns a JSON-encoded ToolError. The message is capped at
// MaxErrorMessageBytes (256 UTF-8 bytes, same cap as Phase 8's FR-027).
func toolError(code, message string) json.RawMessage {
	msg := message
	if len(msg) > MaxErrorMessageBytes {
		msg = msg[:MaxErrorMessageBytes]
	}
	te := ToolError{Code: code, MessageTruncated: msg}
	v, _ := json.Marshal(te)
	return v
}

func marshalResult(v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return toolError(ErrCodeCDPDisconnect, "marshal result: "+err.Error()), nil
	}
	return raw, nil
}

// codeFromErr maps an error string prefix to an error code.
func codeFromErr(err error) string {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, ErrCodeTimeout):
		return ErrCodeTimeout
	case strings.HasPrefix(msg, ErrCodeJSEvalFailed):
		return ErrCodeJSEvalFailed
	case strings.HasPrefix(msg, ErrCodeNavigationFailed):
		return ErrCodeNavigationFailed
	case strings.HasPrefix(msg, ErrCodeSelectorNoMatch):
		return ErrCodeSelectorNoMatch
	case strings.HasPrefix(msg, ErrCodeCDPDisconnect):
		return ErrCodeCDPDisconnect
	default:
		return ErrCodeCDPDisconnect
	}
}
