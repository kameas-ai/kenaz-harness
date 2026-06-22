package serve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"

	"github.com/kameas-ai/kenaz-harness/core/rpc"
	"github.com/kameas-ai/kenaz-harness/core/serve"
)

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

	s := serve.New(api, addr, token, nil)

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
	if !strings.Contains(envelope.Error, "method not found") {
		t.Errorf("expected 'method not found' error, got %q", envelope.Error)
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
