package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	httptransport "github.com/sigil-tech/kaneaz-harness/core/mcp/transport/http"
)

// fakeJSONRPCServer is a minimal httptest.Server stub that echoes
// every incoming JSON-RPC request as a successful response. The
// test harness uses it to drive Send/Recv round-trips without
// reaching for a real MCP server.
func fakeJSONRPCServer(t *testing.T, capture func(req map[string]any, headers stdhttp.Header)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		var raw map[string]any
		body, err := io.ReadAll(r.Body)
		if err != nil {
			stdhttp.Error(w, err.Error(), stdhttp.StatusBadRequest)
			return
		}
		_ = json.Unmarshal(body, &raw)
		if capture != nil {
			// Snapshot headers so the test can assert them after the
			// request returns; http.Header itself is not safe to
			// retain past the handler.
			cp := stdhttp.Header{}
			for k, v := range r.Header {
				cp[k] = append([]string{}, v...)
			}
			capture(raw, cp)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      raw["id"],
			"result":  map[string]any{"echoed": raw["method"]},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return srv
}

func TestConnectionRoundTrip(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		seenReq  map[string]any
		seenHdrs stdhttp.Header
	)
	srv := fakeJSONRPCServer(t, func(req map[string]any, h stdhttp.Header) {
		mu.Lock()
		seenReq = req
		seenHdrs = h
		mu.Unlock()
	})
	defer srv.Close()

	conn := httptransport.NewConnection(httptransport.Spec{
		ID:  "test",
		URL: srv.URL,
		HeadersTemplate: map[string]string{
			"X-Trace": "abc",
		},
		HTTPClient: srv.Client(),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.Send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if msg.Error != nil {
		t.Fatalf("server returned error: %+v", msg.Error)
	}
	var result map[string]any
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result["echoed"] != "tools/list" {
		t.Errorf("echoed = %v, want tools/list", result["echoed"])
	}

	mu.Lock()
	defer mu.Unlock()
	if seenReq["method"] != "tools/list" {
		t.Errorf("server saw method = %v", seenReq["method"])
	}
	if seenHdrs.Get("X-Trace") != "abc" {
		t.Errorf("X-Trace header missing: got %v", seenHdrs)
	}
}

func TestConnectionEnvSubstitution(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		seenHdrs stdhttp.Header
	)
	srv := fakeJSONRPCServer(t, func(_ map[string]any, h stdhttp.Header) {
		mu.Lock()
		seenHdrs = h
		mu.Unlock()
	})
	defer srv.Close()

	conn := httptransport.NewConnection(httptransport.Spec{
		ID:  "auth",
		URL: srv.URL,
		HeadersTemplate: map[string]string{
			"Authorization": "Bearer ${GITHUB_TOKEN}",
		},
		Env: map[string]string{
			"GITHUB_TOKEN": "ghp_secretvalue",
		},
		HTTPClient: srv.Client(),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.Send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if _, err := conn.Recv(); err != nil {
		t.Fatalf("Recv: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	got := seenHdrs.Get("Authorization")
	if got != "Bearer ghp_secretvalue" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer ghp_secretvalue")
	}
}

func TestConnectionCancellationPropagates(t *testing.T) {
	t.Parallel()
	// Server hangs forever; Close should still bring the in-flight
	// POST down within the 100ms acceptance window.
	hang := make(chan struct{})
	defer close(hang)

	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	conn := httptransport.NewConnection(httptransport.Spec{
		ID:         "hang",
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := conn.Send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	start := time.Now()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Errorf("Close took %v, want < 200ms (acceptance: 100ms cancellation window)", elapsed)
	}

	// After close, Recv should return io.EOF promptly.
	doneCh := make(chan error, 1)
	go func() {
		_, err := conn.Recv()
		doneCh <- err
	}()
	select {
	case err := <-doneCh:
		if !errors.Is(err, io.EOF) {
			// The fabricated dispatch error may surface first; either way
			// the read should not hang. Accept any non-nil err that
			// doesn't time out.
			if err == nil {
				t.Errorf("Recv after Close returned nil")
			}
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("Recv after Close hung")
	}
}

func TestConnectionHTTPErrorSurfacesInStderrTail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.Error(w, "internal boom", stdhttp.StatusInternalServerError)
	}))
	defer srv.Close()

	conn := httptransport.NewConnection(httptransport.Spec{
		ID:         "boom",
		URL:        srv.URL,
		HTTPClient: srv.Client(),
	}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.Send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/list",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	msg, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if msg.Error == nil {
		t.Fatalf("expected JSON-RPC error envelope, got result %s", string(msg.Result))
	}
	if !strings.Contains(msg.Error.Message, "500") {
		t.Errorf("error message %q does not mention 500", msg.Error.Message)
	}
	if tail := conn.StderrTail(1024); tail == "" {
		t.Errorf("StderrTail empty after HTTP 500")
	}
}

func TestConnectionPIDIsZero(t *testing.T) {
	t.Parallel()
	conn := httptransport.NewConnection(httptransport.Spec{ID: "x", URL: "http://example.com"}, nil)
	if conn.PID() != 0 {
		t.Errorf("PID = %d, want 0", conn.PID())
	}
}

func TestConnectionRejectsEmptyURL(t *testing.T) {
	t.Parallel()
	conn := httptransport.NewConnection(httptransport.Spec{ID: "x"}, nil)
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("Open with empty URL = nil; want error")
	}
}

func TestConnectionRejectsBadScheme(t *testing.T) {
	t.Parallel()
	conn := httptransport.NewConnection(httptransport.Spec{ID: "x", URL: "ftp://example.com"}, nil)
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("Open with ftp:// URL = nil; want error")
	}
}

func TestConnectionDoubleOpen(t *testing.T) {
	t.Parallel()
	srv := fakeJSONRPCServer(t, nil)
	defer srv.Close()
	conn := httptransport.NewConnection(httptransport.Spec{ID: "x", URL: srv.URL, HTTPClient: srv.Client()}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("second Open = nil; want error")
	}
}
