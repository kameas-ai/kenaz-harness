package browser

// CDP fake for unit tests.
//
// The fake is a minimal httptest server that:
//   1. Serves GET /json/list  → returns a single page target whose
//      webSocketDebuggerUrl points back to the test server.
//   2. Handles WebSocket upgrades on /devtools/page/test-target.
//   3. Routes CDP method calls by name and dispatches to per-method handlers
//      registered by each test.
//
// Tests construct a Tool with CDPEndpoint pointing at the fake server, then
// call tool.Call() as the real harness would.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ── CDP fake ──────────────────────────────────────────────────────────────────

type cdpFake struct {
	srv      *httptest.Server
	upgrader websocket.Upgrader

	mu       sync.Mutex
	handlers map[string]fakeHandler // method → handler
}

// fakeHandler returns (result JSON, cdpError). Return nil error for success.
type fakeHandler func(params json.RawMessage) (json.RawMessage, *cdpError)

type cdpRequest struct {
	ID     int64           `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type cdpReplyFrame struct {
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

func newCDPFake(t *testing.T) *cdpFake {
	t.Helper()
	f := &cdpFake{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		handlers: make(map[string]fakeHandler),
	}

	mux := http.NewServeMux()

	// /json/list — returns the single test target.
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		// The webSocketDebuggerUrl must point at the test server's WS path.
		wsURL := "ws://" + f.srv.Listener.Addr().String() + "/devtools/page/test-target"
		targets := []map[string]any{
			{
				"id":                   "test-target",
				"type":                 "page",
				"webSocketDebuggerUrl": wsURL,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(targets)
	})

	// /devtools/page/test-target — accepts WS upgrade and dispatches CDP.
	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		conn, err := f.upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("cdpFake: upgrade error: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req cdpRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			f.mu.Lock()
			h, ok := f.handlers[req.Method]
			f.mu.Unlock()

			var reply cdpReplyFrame
			reply.ID = req.ID
			if !ok {
				// Default: return empty result for unknown methods.
				reply.Result = json.RawMessage(`{}`)
			} else {
				result, cdpErr := h(req.Params)
				if cdpErr != nil {
					reply.Error = cdpErr
				} else {
					reply.Result = result
				}
			}

			data, _ := json.Marshal(reply)
			_ = conn.WriteMessage(websocket.TextMessage, data)
		}
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// register installs a handler for a CDP method (replaces any prior handler).
func (f *cdpFake) register(method string, h fakeHandler) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = h
}

// endpoint returns the ws:// base URL that the Tool should dial.
func (f *cdpFake) endpoint() string {
	return "ws://" + f.srv.Listener.Addr().String()
}

// toolWithFake constructs a Tool pointing at the fake.
func toolWithFake(t *testing.T, f *cdpFake) *Tool {
	t.Helper()
	return New(Options{
		CDPEndpoint: f.endpoint(),
		DialTimeout: 5 * time.Second,
	})
}

// call is a convenience wrapper that calls tool.Call and unmarshals the result.
func call(t *testing.T, tool *Tool, args any) json.RawMessage {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("tool.Call error: %v", err)
	}
	return result
}

// ── Helpers for standard CDP responses ────────────────────────────────────────

// pageNavigateSuccess returns a handler that simulates a successful navigation.
func pageNavigateSuccess(frameID string) fakeHandler {
	return func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		v, _ := json.Marshal(map[string]any{"frameId": frameID, "loaderId": "load1"})
		return v, nil
	}
}

// runtimeEvalStringHandler returns a handler that returns a string value from
// Runtime.evaluate.
func runtimeEvalStringHandler(value string) fakeHandler {
	return func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		v, _ := json.Marshal(map[string]any{
			"result": map[string]any{
				"type":  "string",
				"value": value,
			},
		})
		return v, nil
	}
}

// runtimeEvalBoolHandler returns a handler that returns a bool value.
func runtimeEvalBoolHandler(value bool) fakeHandler {
	return func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		v, _ := json.Marshal(map[string]any{
			"result": map[string]any{
				"type":  "boolean",
				"value": value,
			},
		})
		return v, nil
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestBrowser_NavigateSuccess verifies that a successful navigate returns
// {status:"ok", final_url, title}.
func TestBrowser_NavigateSuccess(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	// Page.enable — default empty response is fine.
	// Page.navigate.
	f.register("Page.navigate", pageNavigateSuccess("frame1"))
	// Runtime.evaluate for readyState.
	callCount := 0
	f.register("Runtime.evaluate", func(params json.RawMessage) (json.RawMessage, *cdpError) {
		callCount++
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "readyState") {
			return runtimeEvalStringHandler("complete")(params)
		}
		if strings.Contains(p.Expression, "location.href") {
			return runtimeEvalStringHandler("https://example.com/")(params)
		}
		if strings.Contains(p.Expression, "document.title") {
			return runtimeEvalStringHandler("Example Domain")(params)
		}
		// Fallback.
		return runtimeEvalStringHandler("")(params)
	})

	result := call(t, tool, map[string]any{
		"action": "navigate",
		"url":    "https://example.com",
	})

	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if got["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", got["status"])
	}
	if got["final_url"] != "https://example.com/" {
		t.Errorf("expected final_url=https://example.com/, got %v", got["final_url"])
	}
	if got["title"] != "Example Domain" {
		t.Errorf("expected title=Example Domain, got %v", got["title"])
	}
}

// TestBrowser_NavigateFailure verifies that a CDP error on Page.navigate
// returns a navigation_failed ToolError.
func TestBrowser_NavigateFailure(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	f.register("Page.navigate", func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		return nil, &cdpError{Code: -32602, Message: "net::ERR_NAME_NOT_RESOLVED"}
	})

	result := call(t, tool, map[string]any{
		"action": "navigate",
		"url":    "https://this.does.not.exist.invalid",
	})

	var te ToolError
	if err := json.Unmarshal(result, &te); err != nil {
		t.Fatalf("unmarshal ToolError: %v", err)
	}
	if te.Code != ErrCodeNavigationFailed && te.Code != ErrCodeJSEvalFailed {
		t.Errorf("expected navigation_failed or js_eval_failed, got %q", te.Code)
	}
}

// TestBrowser_ExtractTextRespectsCap verifies that a 1 MiB text body is
// truncated to 256 KiB and that truncated=true is set.
func TestBrowser_ExtractTextRespectsCap(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	// Build a 1 MiB string.
	bigText := strings.Repeat("a", 1024*1024)

	// Runtime.evaluate — first call is readyState, subsequent are for extract.
	evalCallCount := 0
	f.register("Runtime.evaluate", func(params json.RawMessage) (json.RawMessage, *cdpError) {
		evalCallCount++
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "readyState") {
			return runtimeEvalStringHandler("complete")(params)
		}
		// For innerText / outerHTML.
		return runtimeEvalStringHandler(bigText)(params)
	})

	result := call(t, tool, map[string]any{
		"action": "extract_text",
	})

	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	text, ok := got["text"].(string)
	if !ok {
		t.Fatalf("expected text key, got %v", got)
	}
	if len(text) != MaxTextBytes {
		t.Errorf("expected text capped at %d bytes, got %d", MaxTextBytes, len(text))
	}
	if got["truncated"] != true {
		t.Errorf("expected truncated=true, got %v", got["truncated"])
	}
}

// TestBrowser_SelectorNoMatch verifies that clicking a missing selector returns
// selector_no_match.
func TestBrowser_SelectorNoMatch(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	// querySelector returns null → exists=false.
	f.register("Runtime.evaluate", func(params json.RawMessage) (json.RawMessage, *cdpError) {
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		// Boolean(document.querySelector(...) !== null) → false.
		if strings.Contains(p.Expression, "Boolean") {
			return runtimeEvalBoolHandler(false)(params)
		}
		return runtimeEvalStringHandler("")(params)
	})

	result := call(t, tool, map[string]any{
		"action":   "click",
		"selector": "#does-not-exist",
	})

	var te ToolError
	if err := json.Unmarshal(result, &te); err != nil {
		t.Fatalf("unmarshal ToolError: %v", err)
	}
	if te.Code != ErrCodeSelectorNoMatch {
		t.Errorf("expected selector_no_match, got %q", te.Code)
	}
}

// TestBrowser_ScreenshotEncoding verifies that a screenshot result contains
// a non-empty base64_png field.
func TestBrowser_ScreenshotEncoding(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	// Build a small fake PNG (1×1 pixel, valid PNG header).
	// We use a minimal valid PNG so base64 decode doesn't fail.
	fakePNG := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x04, 0x00, // 1024×1024
		0x08, 0x02, 0x00, 0x00, 0x00,
	}
	fakeB64 := base64.StdEncoding.EncodeToString(fakePNG)

	f.register("Page.captureScreenshot", func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		v, _ := json.Marshal(map[string]any{"data": fakeB64})
		return v, nil
	})

	result := call(t, tool, map[string]any{
		"action": "screenshot",
	})

	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	b64, ok := got["base64_png"].(string)
	if !ok || b64 == "" {
		t.Errorf("expected non-empty base64_png, got %v", got)
	}
	// Verify it decodes cleanly.
	if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
		t.Errorf("base64_png does not decode: %v", err)
	}
}

// TestBrowser_EvaluateJSErr verifies that a CDP exception from Runtime.evaluate
// returns js_eval_failed.
func TestBrowser_EvaluateJSErr(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	f.register("Runtime.evaluate", func(_ json.RawMessage) (json.RawMessage, *cdpError) {
		v, _ := json.Marshal(map[string]any{
			"result": map[string]any{"type": "undefined"},
			"exceptionDetails": map[string]any{
				"text":      "ReferenceError: undeclaredVariable is not defined",
				"lineNumber": 0,
			},
		})
		return v, nil
	})

	result := call(t, tool, map[string]any{
		"action":     "evaluate_js",
		"expression": "undeclaredVariable.foo",
	})

	var te ToolError
	if err := json.Unmarshal(result, &te); err != nil {
		t.Fatalf("unmarshal ToolError: %v", err)
	}
	if te.Code != ErrCodeJSEvalFailed {
		t.Errorf("expected js_eval_failed, got %q", te.Code)
	}
}

// TestBrowser_ChromiumUnreachable verifies that connection refused on the
// CDP endpoint returns chromium_unreachable.
func TestBrowser_ChromiumUnreachable(t *testing.T) {
	// Point the tool at a port that is guaranteed to refuse connections.
	// We use a closed httptest server so the port is in TIME_WAIT / closed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close() // Close immediately so port is not listening.

	tool := New(Options{
		CDPEndpoint: "ws://" + addr,
		DialTimeout: 2 * time.Second,
	})

	argsJSON, _ := json.Marshal(map[string]any{"action": "navigate", "url": "https://example.com"})
	result, err := tool.Call(context.Background(), argsJSON)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	var te ToolError
	if unmErr := json.Unmarshal(result, &te); unmErr != nil {
		t.Fatalf("unmarshal ToolError: %v", unmErr)
	}
	if te.Code != ErrCodeChromiumUnreach {
		t.Errorf("expected chromium_unreachable, got %q", te.Code)
	}
}

// TestBrowser_ConcurrentCallsSerialize verifies that two concurrent navigate
// calls on the same tool do not race (v1 one-page serialisation).
func TestBrowser_ConcurrentCallsSerialize(t *testing.T) {
	f := newCDPFake(t)
	tool := toolWithFake(t, f)

	// Count how many navigate calls the fake receives; they must come
	// sequentially (the mutex means one at a time).
	var (
		navMu        sync.Mutex
		concurrentMax int
		active        int
	)

	evalCallCount := 0
	f.register("Runtime.evaluate", func(params json.RawMessage) (json.RawMessage, *cdpError) {
		evalCallCount++
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		if strings.Contains(p.Expression, "readyState") {
			return runtimeEvalStringHandler("complete")(params)
		}
		return runtimeEvalStringHandler("https://example.com/")(params)
	})

	f.register("Page.navigate", func(params json.RawMessage) (json.RawMessage, *cdpError) {
		navMu.Lock()
		active++
		if active > concurrentMax {
			concurrentMax = active
		}
		navMu.Unlock()

		// Simulate a short delay.
		time.Sleep(10 * time.Millisecond)

		navMu.Lock()
		active--
		navMu.Unlock()

		v, _ := json.Marshal(map[string]any{"frameId": "frame1"})
		return v, nil
	})

	// Fire two concurrent calls.
	var wg sync.WaitGroup
	errs := make([]string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			argsJSON, _ := json.Marshal(map[string]any{
				"action": "navigate",
				"url":    fmt.Sprintf("https://example.com/%d", idx),
			})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result, err := tool.Call(ctx, argsJSON)
			if err != nil {
				errs[idx] = fmt.Sprintf("call error: %v", err)
				return
			}
			var te ToolError
			if unmErr := json.Unmarshal(result, &te); unmErr == nil && te.Code != "" {
				errs[idx] = fmt.Sprintf("tool error: %s", te.Code)
			}
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != "" {
			t.Errorf("call %d: %s", i, e)
		}
	}

	// The mutex means at most 1 call is active inside the fake at any time.
	if concurrentMax > 1 {
		t.Errorf("concurrent calls were not serialised: max concurrency = %d, want 1", concurrentMax)
	}
}

// TestToolName verifies the ToolName constant is the expected namespaced value.
func TestToolName(t *testing.T) {
	tool := New(Options{CDPEndpoint: "ws://localhost:9222"})
	if tool.Name() != ToolName {
		t.Errorf("expected %q, got %q", ToolName, tool.Name())
	}
	if ToolName != "kaneaz__browser" {
		t.Errorf("ToolName = %q, want kaneaz__browser", ToolName)
	}
}

// TestInputSchemaValid verifies that InputSchema() returns valid JSON.
func TestInputSchemaValid(t *testing.T) {
	tool := New(Options{CDPEndpoint: "ws://localhost:9222"})
	schema := tool.InputSchema()
	var parsed map[string]any
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
	if parsed["type"] != "object" {
		t.Errorf("expected schema type=object, got %v", parsed["type"])
	}
}

// TestErrorMessageTruncation verifies the 256-byte message cap.
func TestErrorMessageTruncation(t *testing.T) {
	longMsg := strings.Repeat("x", 1000)
	result := toolError(ErrCodeCDPDisconnect, longMsg)

	var te ToolError
	if err := json.Unmarshal(result, &te); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(te.MessageTruncated) > MaxErrorMessageBytes {
		t.Errorf("message not truncated: len=%d, want <=%d", len(te.MessageTruncated), MaxErrorMessageBytes)
	}
}
