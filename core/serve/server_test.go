package serve_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	"github.com/kameas-ai/kenaz-harness/core/serve"
	"github.com/kameas-ai/kenaz-harness/core/serve/authbroker"
	"github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
)

// minimalStaticFS returns a minimal in-memory FS that mimics the dist-served
// bundle layout, with a served.html that contains the token placeholder and
// a small JS asset.
func minimalStaticFS() fs.FS {
	const indexHTML = `<!DOCTYPE html><html><head>` +
		`<!--HARNESS_TOKEN_META-->` +
		`</head><body><div id="app"></div>` +
		`<script src="/assets/app.js"></script>` +
		`</body></html>`
	const appJS = `console.log("harness-served");`
	return fstest.MapFS{
		"served.html":    &fstest.MapFile{Data: []byte(indexHTML)},
		"assets/app.js":  &fstest.MapFile{Data: []byte(appJS)},
		"assets/app.css": &fstest.MapFile{Data: []byte(`body{margin:0}`)},
	}
}

// newTestServerWithStatic starts a serve.Server with the given token and a
// minimal in-memory static bundle.
func newTestServerWithStatic(t *testing.T, token string) (srv *serve.Server, baseURL string, cancel context.CancelFunc) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil)
	ctx, ctxCancel := context.WithCancel(context.Background())
	s := serve.New(api, addr, token, minimalStaticFS(), nil)

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return s, "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

// newTestServer starts a serve.Server on a random port with the given token,
// backed by a rpc.New(nil) API (test chassis — no core).
// Returns the server and its base URL.  The server shuts down when the
// returned cancel function is called.
func newTestServer(t *testing.T, token string) (srv *serve.Server, baseURL string, cancel context.CancelFunc) {
	t.Helper()

	// Pick a free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // release; serve.Server will re-listen on the same port

	api := rpc.New(nil) // test chassis — nil core, stable stub surfaces

	ctx, ctxCancel := context.WithCancel(context.Background())

	s := serve.New(api, addr, token, nil, nil)

	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	// Wait until the server is accepting connections.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return s, "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

// ─── /healthz ─────────────────────────────────────────────────────────────

func TestHealthz_Unauthenticated(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "secret-token")
	defer cancel()

	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected {\"ok\":true}, got %v", body)
	}
}

// ─── /rpc auth ────────────────────────────────────────────────────────────

func TestRPC_Unauthorized(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "secret-token")
	defer cancel()

	resp, err := http.Post(baseURL+"/rpc", "application/json",
		strings.NewReader(`{"method":"AppInfo","params":{}}`))
	if err != nil {
		t.Fatalf("POST /rpc: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestRPC_WrongToken(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "secret-token")
	defer cancel()

	req, _ := http.NewRequest(http.MethodPost, baseURL+"/rpc",
		strings.NewReader(`{"method":"AppInfo","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /rpc: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// ─── /rpc method calls ────────────────────────────────────────────────────

func authedPost(t *testing.T, baseURL, token, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/rpc",
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func TestRPC_AppInfo(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	resp := authedPost(t, baseURL, "tok", `{"method":"AppInfo","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}

	var envelope struct {
		Result struct {
			Build     string `json:"build"`
			GoVersion string `json:"goVersion"`
			Platform  string `json:"platform"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("unexpected error field: %s", envelope.Error)
	}
	if envelope.Result.GoVersion == "" {
		t.Error("expected non-empty GoVersion")
	}
	if envelope.Result.Platform == "" {
		t.Error("expected non-empty Platform")
	}
}

func TestRPC_SessionsList(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	resp := authedPost(t, baseURL, "tok", `{"method":"Sessions_List","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}

	var envelope struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("unexpected error: %s", envelope.Error)
	}
	// rpc.New(nil) returns an empty list — just verify the result is a JSON array.
	if len(envelope.Result) == 0 {
		t.Fatal("empty result")
	}
	if envelope.Result[0] != '[' && string(envelope.Result) != "null" {
		t.Errorf("expected JSON array or null, got %s", envelope.Result)
	}
}

func TestRPC_UnknownMethod(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	resp := authedPost(t, baseURL, "tok", `{"method":"Nonexistent","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 envelope, got %d", resp.StatusCode)
	}
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// FR-005: unhandled methods now return an explicit "not ported" error
	// rather than a generic "method not found" to help callers distinguish
	// "typo" from "valid desktop binding not yet wired in serve mode".
	if !strings.Contains(envelope.Error, "not ported to served mode") {
		t.Errorf("expected 'not ported to served mode' in error, got %q", envelope.Error)
	}
}

// ─── /ws ──────────────────────────────────────────────────────────────────

func TestWS_SessionsStream(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	// Replace http:// with ws://
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"

	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("Authorization", fmt.Sprintf("Bearer %s", "tok"))

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	// Request the sessions stream.
	if err := websocket.JSON.Send(ws, map[string]any{
		"method": "Sessions_Stream",
		"params": map[string]any{},
	}); err != nil {
		t.Fatalf("ws send: %v", err)
	}

	// Expect an initial snapshot frame.
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := websocket.JSON.Receive(ws, &frame); err != nil {
		t.Fatalf("ws receive: %v", err)
	}
	if frame.Error != "" {
		t.Fatalf("ws frame error: %s", frame.Error)
	}
	if frame.Event != "sessions:snapshot" {
		t.Errorf("expected event 'sessions:snapshot', got %q", frame.Event)
	}
	// Data should be a JSON array (possibly empty).
	if len(frame.Data) == 0 || (frame.Data[0] != '[' && string(frame.Data) != "null") {
		t.Errorf("expected JSON array or null for sessions snapshot, got %s", frame.Data)
	}
}

func TestWS_UnknownMethod(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"

	cfg, _ := websocket.NewConfig(wsURL, "http://localhost")
	cfg.Header.Set("Authorization", "Bearer tok")

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close()

	if err := websocket.JSON.Send(ws, map[string]any{"method": "Unknown_Method"}); err != nil {
		t.Fatalf("ws send: %v", err)
	}

	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame struct {
		Error string `json:"error"`
	}
	if err := websocket.JSON.Receive(ws, &frame); err != nil {
		t.Fatalf("ws receive: %v", err)
	}
	if !strings.Contains(frame.Error, "unknown stream method") {
		t.Errorf("expected 'unknown stream method' error, got %q", frame.Error)
	}
}

// newTestAPIServer is like newTestServer but also returns the *rpc.API so
// callers can publish directly to the EventBus to trigger push assertions.
func newTestAPIServer(t *testing.T, token string) (api *rpc.API, srv *serve.Server, baseURL string, cancel context.CancelFunc) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api = rpc.New(nil)
	ctx, ctxCancel := context.WithCancel(context.Background())
	srv = serve.New(api, addr, token, nil, nil)

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return api, srv, "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

// TestWS_SessionsStream_PushDirect verifies that publishing a
// TopicSessionListChanged event to the API's EventBus causes a connected /ws
// client to receive a "sessions:update" frame in real time, with no polling.
// The assertion deadline is 500 ms — well under the old 2 s poll period.
func TestWS_SessionsStream_PushDirect(t *testing.T) {
	api, _, baseURL, cancel := newTestAPIServer(t, "tok")
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("Authorization", "Bearer tok")

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	// Start the sessions stream.
	if err := websocket.JSON.Send(ws, map[string]any{
		"method": "Sessions_Stream",
		"params": map[string]any{},
	}); err != nil {
		t.Fatalf("ws send: %v", err)
	}

	// Consume the mandatory initial snapshot.
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var snapshotFrame struct {
		Event string `json:"event"`
	}
	if err := websocket.JSON.Receive(ws, &snapshotFrame); err != nil {
		t.Fatalf("snapshot receive: %v", err)
	}
	if snapshotFrame.Event != "sessions:snapshot" {
		t.Fatalf("expected 'sessions:snapshot', got %q", snapshotFrame.Event)
	}

	// Publish a session-list change directly to the EventBus.
	// This is what the real broker does when a session mutation fires.
	go func() {
		// Small yield so the streamSessions goroutine is blocked on busCh select.
		time.Sleep(20 * time.Millisecond)
		api.EventBus().Publish(rpc.TopicSessionListChanged, rpc.SessionListChangedPayload{
			Reason:    "created",
			SessionID: "test-session-1",
			Timestamp: 1000,
		})
	}()

	// Expect a "sessions:update" frame within 500 ms — NOT 2 s.
	ws.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var updateFrame struct {
		Event string `json:"event"`
		Error string `json:"error"`
	}
	if err := websocket.JSON.Receive(ws, &updateFrame); err != nil {
		t.Fatalf("update frame not received within 500 ms (push path broken?): %v", err)
	}
	if updateFrame.Error != "" {
		t.Fatalf("unexpected frame error: %s", updateFrame.Error)
	}
	if updateFrame.Event != "sessions:update" {
		t.Errorf("expected 'sessions:update', got %q", updateFrame.Event)
	}
}

// TestWS_AuthRequired verifies that /ws rejects connections without a token.
func TestWS_AuthRequired(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "secret")
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, _ := websocket.NewConfig(wsURL, "http://localhost")
	// No Authorization header — should get 401.

	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Error("expected dial failure for unauthenticated WS, got nil error")
	}
}

// TestWS_SubprotocolAuth verifies that a browser-style WebSocket connection
// authenticated via Sec-WebSocket-Protocol: harness-auth.<base64(token)>
// is accepted (FR-004).
func TestWS_SubprotocolAuth(t *testing.T) {
	const secret = "my-secret-token"
	_, baseURL, cancel := newTestServer(t, secret)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	// Browser path: no Authorization header; token via Sec-WebSocket-Protocol.
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	// Strip padding to match the client's btoa() + replace(/=/g,'') pattern.
	encoded = strings.TrimRight(encoded, "=")
	// Use cfg.Protocol (not cfg.Header.Set) so the x/net/websocket client
	// sends the correct Sec-WebSocket-Protocol header and validates the
	// server's sub-protocol selection in the 101 response.
	cfg.Protocol = []string{"harness-v1", fmt.Sprintf("harness-auth.%s", encoded)}

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("expected WS connection to succeed with sub-protocol auth, got: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	// Send the sessions stream request and expect a snapshot — verifies the
	// connection is fully functional, not just HTTP 101.
	if err := websocket.JSON.Send(ws, map[string]any{
		"method": "Sessions_Stream",
		"params": map[string]any{},
	}); err != nil {
		t.Fatalf("ws send: %v", err)
	}
	ws.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame struct {
		Event string `json:"event"`
		Error string `json:"error"`
	}
	if err := websocket.JSON.Receive(ws, &frame); err != nil {
		t.Fatalf("ws receive: %v", err)
	}
	if frame.Error != "" {
		t.Fatalf("ws frame error: %s", frame.Error)
	}
	if frame.Event != "sessions:snapshot" {
		t.Errorf("expected 'sessions:snapshot', got %q", frame.Event)
	}
}

// TestWS_SubprotocolAuthWrongToken verifies that the sub-protocol path still
// rejects a wrong token (constant-time compare, not just prefix match).
func TestWS_SubprotocolAuthWrongToken(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "correct-token")
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, _ := websocket.NewConfig(wsURL, "http://localhost")
	encoded := base64.StdEncoding.EncodeToString([]byte("wrong-token"))
	encoded = strings.TrimRight(encoded, "=")
	cfg.Protocol = []string{"harness-v1", fmt.Sprintf("harness-auth.%s", encoded)}

	_, err := websocket.DialConfig(cfg)
	if err == nil {
		t.Error("expected dial failure for wrong token via sub-protocol, got nil error")
	}
}

// ─── static-bundle serving + token injection ──────────────────────────────

// TestStatic_IndexInjectsToken verifies that GET / returns the SPA HTML with
// the bearer token injected as a <meta name="harness-token"> tag.
func TestStatic_IndexInjectsToken(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "my-secret-token")
	defer cancel()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html Content-Type, got %q", ct)
	}
	// Must not be cached.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("expected Cache-Control: no-store, got %q", cc)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	// The placeholder must have been replaced.
	if strings.Contains(bodyStr, "<!--HARNESS_TOKEN_META-->") {
		t.Error("token placeholder was not replaced in served HTML")
	}
	// The injected meta tag must carry the token.
	wantMeta := `<meta name="harness-token" content="my-secret-token"`
	if !strings.Contains(bodyStr, wantMeta) {
		t.Errorf("expected injected token meta tag in HTML, got:\n%s", bodyStr)
	}
}

// TestStatic_IndexEmptyToken verifies that an empty token still produces a
// valid meta tag (empty content) rather than the raw placeholder.
func TestStatic_IndexEmptyToken(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "")
	defer cancel()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	if strings.Contains(bodyStr, "<!--HARNESS_TOKEN_META-->") {
		t.Error("placeholder must be replaced even when token is empty")
	}
	// An empty token still produces a meta element.
	wantMeta := `<meta name="harness-token" content=""`
	if !strings.Contains(bodyStr, wantMeta) {
		t.Errorf("expected empty-token meta tag, got:\n%s", bodyStr)
	}
}

// minimalStaticFSWithCSP is like minimalStaticFS but the served.html carries
// the raw __CSP_PLACEHOLDER__ sentinel (simulating a bundle built without the
// Vite inject-csp plugin) so we can assert the server substitutes it.
func minimalStaticFSWithCSP() fs.FS {
	const indexHTML = `<!DOCTYPE html><html><head>` +
		`<meta http-equiv="Content-Security-Policy" content="__CSP_PLACEHOLDER__" />` +
		`<!--HARNESS_TOKEN_META-->` +
		`</head><body><div id="app"></div></body></html>`
	return fstest.MapFS{
		"served.html": &fstest.MapFile{Data: []byte(indexHTML)},
	}
}

// TestStatic_IndexSubstitutesCSPPlaceholder verifies that when the served
// bundle still carries the raw __CSP_PLACEHOLDER__ sentinel, the server
// replaces it with the real served-mode CSP rather than shipping a literal
// placeholder as the Content-Security-Policy.
func TestStatic_IndexSubstitutesCSPPlaceholder(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil)
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	s := serve.New(api, addr, "tok", minimalStaticFSWithCSP(), nil)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	if strings.Contains(bodyStr, "__CSP_PLACEHOLDER__") {
		t.Error("CSP placeholder was not substituted in served HTML")
	}
	// The real served CSP allows same-origin connect-src (needed for /rpc + /ws).
	if !strings.Contains(bodyStr, "connect-src 'self'") {
		t.Errorf("expected served CSP with connect-src 'self', got:\n%s", bodyStr)
	}
}

// TestStatic_AssetServed verifies that a known asset from the bundle is served
// with the correct content type.
func TestStatic_AssetServed(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "tok")
	defer cancel()

	resp, err := http.Get(baseURL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("expected javascript Content-Type, got %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "harness-served") {
		t.Errorf("unexpected asset body: %s", body)
	}
}

// TestStatic_CSSAssetServed verifies CSS assets are served with the right MIME type.
func TestStatic_CSSAssetServed(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "tok")
	defer cancel()

	resp, err := http.Get(baseURL + "/assets/app.css")
	if err != nil {
		t.Fatalf("GET /assets/app.css: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "css") {
		t.Errorf("expected css Content-Type, got %q", ct)
	}
}

// TestStatic_UnknownPathFallsBackToIndex verifies that an unrecognised path
// falls back to serving the SPA index so the hash-router can handle it.
func TestStatic_UnknownPathFallsBackToIndex(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "tok")
	defer cancel()

	resp, err := http.Get(baseURL + "/sessions/abc123")
	if err != nil {
		t.Fatalf("GET /sessions/abc123: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (SPA fallback), got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html for SPA fallback, got %q", ct)
	}
}

// TestStatic_NilFSReturns404 verifies that when no static bundle is provided
// (nil staticFS), GET / returns 404 instead of panicking.
func TestStatic_NilFSReturns404(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok") // newTestServer passes nil staticFS
	defer cancel()

	resp, err := http.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when staticFS is nil, got %d", resp.StatusCode)
	}
}

// ─── Auth_State RPC ───────────────────────────────────────────────────────

// TestRPC_AuthState_NoSession verifies that Auth_State returns "anonymous"
// when no authbroker.Session is wired (the default for tests).
func TestRPC_AuthState_NoSession(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "tok")
	defer cancel()

	resp := authedPost(t, baseURL, "tok", `{"method":"Auth_State","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	var envelope struct {
		Result struct {
			State string `json:"state"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("unexpected error: %s", envelope.Error)
	}
	if envelope.Result.State != "anonymous" {
		t.Errorf("expected state 'anonymous', got %q", envelope.Result.State)
	}
}

// TestRPC_AuthState_WithSession verifies that Auth_State reflects the auth
// session state when a session is wired via WithAuthSession.
func TestRPC_AuthState_WithSession(t *testing.T) {
	// Create an anonymous session (no signed-in seed).
	anonSession := authbroker.NewSession(
		context.Background(),
		authbroker.Config{SignedIn: false},
		nil,
	)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil)
	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()

	srv := serve.New(api, addr, "tok", nil, nil, serve.WithAuthSession(anonSession))
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	// Wait for server to be ready.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	baseURL := "http://" + addr

	resp := authedPost(t, baseURL, "tok", `{"method":"Auth_State","params":{}}`)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	var envelope struct {
		Result struct {
			State string `json:"state"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error != "" {
		t.Fatalf("unexpected error: %s", envelope.Error)
	}
	if envelope.Result.State != "anonymous" {
		t.Errorf("expected state 'anonymous' from anon session, got %q", envelope.Result.State)
	}
}

// TestRPC_UnhandledMethod_NotPortedError verifies that calling a desktop
// binding not yet wired in serve mode returns a clear "not ported" error
// (FR-005) rather than fake data or a generic "method not found" message.
func TestRPC_UnhandledMethod_NotPortedError(t *testing.T) {
	_, baseURL, cancel := newTestServer(t, "")
	defer cancel()

	// "Settings_Get" is a valid desktop binding not wired in serve dispatch.
	body := strings.NewReader(`{"method":"Settings_Get","params":{}}`)
	resp, err := http.Post(baseURL+"/rpc", "application/json", body)
	if err != nil {
		t.Fatalf("POST /rpc: %v", err)
	}
	defer resp.Body.Close()

	// The HTTP response is 200 (RPC-level error, not HTTP error).
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var envelope struct {
		Error  string `json:"error"`
		Result any    `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Error == "" {
		t.Fatal("expected an error for an unhandled method, got none")
	}
	if !strings.Contains(envelope.Error, "not ported to served mode") {
		t.Errorf("expected 'not ported to served mode' in error, got %q", envelope.Error)
	}
}

// newTestServerWithElicit starts a serve.Server on a random port with a
// stable elicit surface injected via WithElicitAPI, so a pending ask
// registered through the returned *elicit.API is visible to the server's
// Elicit_ListPending / elicit:pending:snapshot paths.
func newTestServerWithElicit(t *testing.T, token string) (elicit *elicitview.API, srv *serve.Server, baseURL string, cancel context.CancelFunc) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	api := rpc.New(nil)
	elicit = elicitview.New(elicitview.Config{}) // emitter nil — snapshot path needs no emit
	ctx, ctxCancel := context.WithCancel(context.Background())
	srv = serve.New(api, addr, token, nil, nil, serve.WithElicitAPI(elicit))

	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, cerr := net.Dial("tcp", addr)
		if cerr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	return elicit, srv, "http://" + addr, func() {
		ctxCancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	}
}

// TestWS_SessionsStream_ElicitPendingSnapshot verifies FR-007: when a blocking
// elicitation is pending, a client that (re)connects the Sessions_Stream WS
// receives an "elicit:pending:snapshot" frame carrying that ask. This is the
// mechanism by which a served-mode reconnect re-surfaces an in-flight ask.
//
// It mirrors the WS_SessionsStream pattern: register a pending ask (via a
// backgrounded OpenDialog on an injected elicit surface), open a *second* WS,
// consume the mandatory sessions:snapshot, then assert the elicit snapshot
// frame contains the registered request.
func TestWS_SessionsStream_ElicitPendingSnapshot(t *testing.T) {
	elicit, _, baseURL, cancel := newTestServerWithElicit(t, "tok")
	defer cancel()

	// Register a pending elicitation. OpenDialog blocks until answered /
	// timed out, so run it in a goroutine; it registers the pending entry
	// synchronously before blocking on the response channel.
	askDone := make(chan struct{})
	go func() {
		defer close(askDone)
		_, _ = elicit.OpenDialog(context.Background(), askuserquestion.AskArgs{
			Question: "Proceed with deploy?",
			Kind:     askuserquestion.KindRadio,
			Options: []askuserquestion.QuestionOption{
				{Value: "yes", Label: "Yes"},
				{Value: "no", Label: "No"},
			},
		}.ToQuestion())
	}()

	// Poll until the ask is registered (OpenDialog registers before it blocks).
	regDeadline := time.Now().Add(2 * time.Second)
	for {
		pending, _ := elicit.ListPending(context.Background())
		if len(pending) > 0 {
			break
		}
		if time.Now().After(regDeadline) {
			t.Fatal("pending ask was not registered in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Connect a (second) WS client — this is the "reconnect" that must
	// re-surface the pending ask.
	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws"
	cfg, err := websocket.NewConfig(wsURL, "http://localhost")
	if err != nil {
		t.Fatalf("ws config: %v", err)
	}
	cfg.Header.Set("Authorization", "Bearer tok")

	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close() //nolint:errcheck

	if err := websocket.JSON.Send(ws, map[string]any{
		"method": "Sessions_Stream",
		"params": map[string]any{},
	}); err != nil {
		t.Fatalf("ws send: %v", err)
	}

	// Read frames until we see the elicit snapshot. The mandatory
	// sessions:snapshot frame arrives first.
	var (
		sawSnapshot bool
		sawElicit   bool
		gotRequest  string
	)
	readDeadline := time.Now().Add(5 * time.Second)
	for !sawElicit && time.Now().Before(readDeadline) {
		ws.SetReadDeadline(time.Now().Add(2 * time.Second))
		var frame struct {
			Event string          `json:"event"`
			Data  json.RawMessage `json:"data"`
			Error string          `json:"error"`
		}
		if err := websocket.JSON.Receive(ws, &frame); err != nil {
			t.Fatalf("ws receive: %v", err)
		}
		if frame.Error != "" {
			t.Fatalf("ws frame error: %s", frame.Error)
		}
		switch frame.Event {
		case "sessions:snapshot":
			sawSnapshot = true
		case "elicit:pending:snapshot":
			sawElicit = true
			// Data is an array of ElicitRequest; confirm our ask is present.
			var reqs []struct {
				RequestID string `json:"request_id"`
				Question  string `json:"question"`
			}
			if err := json.Unmarshal(frame.Data, &reqs); err != nil {
				t.Fatalf("unmarshal elicit snapshot: %v (data=%s)", err, frame.Data)
			}
			if len(reqs) == 0 {
				t.Fatal("elicit:pending:snapshot data was empty")
			}
			for _, r := range reqs {
				if r.Question == "Proceed with deploy?" {
					gotRequest = r.RequestID
				}
			}
		}
	}

	if !sawSnapshot {
		t.Error("did not receive mandatory sessions:snapshot frame")
	}
	if !sawElicit {
		t.Fatal("did not receive elicit:pending:snapshot frame within deadline")
	}
	if gotRequest == "" {
		t.Error("elicit:pending:snapshot did not contain the registered ask")
	}

	// Resolve the ask so the backgrounded OpenDialog returns and the
	// goroutine exits cleanly (avoids a leaked goroutine under -race).
	if err := elicit.SubmitAnswer(context.Background(), gotRequest, json.RawMessage(`"yes"`), false); err != nil {
		t.Errorf("SubmitAnswer: %v", err)
	}
	select {
	case <-askDone:
	case <-time.After(2 * time.Second):
		t.Error("OpenDialog did not return after SubmitAnswer")
	}
}

// TestStatic_HealthzStillWorks confirms /healthz is served even when a static
// FS is wired (the mux ordering is correct).
func TestStatic_HealthzStillWorks(t *testing.T) {
	_, baseURL, cancel := newTestServerWithStatic(t, "tok")
	defer cancel()

	resp, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for /healthz, got %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Errorf("expected {\"ok\":true}, got %v", body)
	}
}
