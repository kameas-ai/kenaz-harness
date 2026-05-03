package sse_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport/sse"
)

// fakeSSEServer is a minimal httptest.Server that speaks the SSE
// protocol. The GET /stream path returns a text/event-stream response
// and accepts JSON-RPC events through a fed channel. The POST /rpc
// path accepts outbound envelopes and responds with a server-initiated
// SSE event that mirrors the request as a result.
type fakeSSEServer struct {
	t   *testing.T
	srv *httptest.Server

	// outCh receives outbound JSON-RPC messages from the SSE scanner
	// goroutine so the test can inject SSE events.
	outCh chan []byte

	// postCapture captures POST bodies.
	mu       sync.Mutex
	postBods [][]byte
}

// newFakeSSEServer creates and starts a fake SSE server. The caller
// is responsible for calling srv.srv.Close().
//
// Protocol: the server sends events via the fanout loop. When the
// client POSTs to /rpc, the server fabricates a JSON-RPC response
// and pushes it as a `data:` event on the stream.
func newFakeSSEServer(t *testing.T) *fakeSSEServer {
	t.Helper()
	f := &fakeSSEServer{
		t:     t,
		outCh: make(chan []byte, 32),
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/stream", f.handleStream)
	mux.HandleFunc("/rpc", f.handleRPC)
	f.srv = httptest.NewServer(mux)
	return f
}

// streamURL returns the SSE stream URL.
func (f *fakeSSEServer) streamURL() string { return f.srv.URL + "/stream" }

// postURL returns the POST endpoint URL.
func (f *fakeSSEServer) postURL() string { return f.srv.URL + "/rpc" }

// handleStream handles the SSE GET request. It flushed events from
// outCh until the client disconnects.
func (f *fakeSSEServer) handleStream(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	flusher, ok := w.(stdhttp.Flusher)
	if !ok {
		stdhttp.Error(w, "streaming not supported", stdhttp.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(stdhttp.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-f.outCh:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleRPC handles POST /rpc. It reads the request body, fabricates
// a result envelope, and pushes it onto outCh so the SSE stream
// delivers it to the client.
func (f *fakeSSEServer) handleRPC(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		stdhttp.Error(w, err.Error(), stdhttp.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.postBods = append(f.postBods, body)
	f.mu.Unlock()

	// Parse the incoming request.
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		stdhttp.Error(w, "bad json", stdhttp.StatusBadRequest)
		return
	}

	// Fabricate a response and push it onto the SSE stream.
	var resultJSON json.RawMessage
	switch req.Method {
	case "initialize":
		resultJSON = jsonMust(map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "fake-sse", "version": "0.0.1"},
		})
	case "tools/list":
		resultJSON = jsonMust(map[string]any{
			"tools": []map[string]any{
				{
					"name":        "echo",
					"description": "echoes input",
					"inputSchema": map[string]any{"type": "object"},
				},
			},
		})
	case "tools/call":
		resultJSON = jsonMust(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "called"}},
			"isError": false,
		})
	default:
		resultJSON = jsonMust(map[string]any{"ok": true})
	}

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  json.RawMessage(resultJSON),
	}
	event, err := json.Marshal(resp)
	if err != nil {
		stdhttp.Error(w, err.Error(), stdhttp.StatusInternalServerError)
		return
	}
	f.outCh <- event

	// Acknowledge the POST with 202.
	w.WriteHeader(stdhttp.StatusAccepted)
}

// capturedPosts returns the POST bodies received so far.
func (f *fakeSSEServer) capturedPosts() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([][]byte, len(f.postBods))
	copy(cp, f.postBods)
	return cp
}

func jsonMust(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---

func TestConnectionRoundTrip(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	conn := sse.NewConnection(sse.Spec{
		ID:         "test",
		URL:        fake.streamURL(),
		PostURL:    fake.postURL(),
		HTTPClient: fake.srv.Client(),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Send tools/list and expect a result back via the SSE stream.
	if err := conn.Send(transport.RequestEnvelope{
		JSONRPC: "2.0",
		ID:      1,
		Method:  transport.MethodToolsList,
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
	var result transport.ToolsListResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatalf("expected tools, got none")
	}
	if result.Tools[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", result.Tools[0].Name, "echo")
	}
}

func TestConnectionInitializeToolsListToolCallRoundTrip(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	conn := sse.NewConnection(sse.Spec{
		ID:         "integration",
		URL:        fake.streamURL(),
		PostURL:    fake.postURL(),
		HTTPClient: fake.srv.Client(),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// 1. initialize
	if err := conn.Send(transport.RequestEnvelope{
		JSONRPC: "2.0",
		ID:      1,
		Method:  transport.MethodInitialize,
		Params: transport.InitializeParams{
			ProtocolVersion: transport.SupportedProtocolVersion,
			ClientInfo:      transport.Implementation{Name: "test", Version: "0"},
		},
	}); err != nil {
		t.Fatalf("Send initialize: %v", err)
	}
	initMsg, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv initialize: %v", err)
	}
	if initMsg.Error != nil {
		t.Fatalf("initialize error: %v", initMsg.Error)
	}
	var initResult transport.InitializeResult
	if err := json.Unmarshal(initMsg.Result, &initResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initResult.ProtocolVersion == "" {
		t.Error("empty protocolVersion in initialize result")
	}

	// 2. tools/list
	if err := conn.Send(transport.RequestEnvelope{
		JSONRPC: "2.0",
		ID:      2,
		Method:  transport.MethodToolsList,
	}); err != nil {
		t.Fatalf("Send tools/list: %v", err)
	}
	tlMsg, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv tools/list: %v", err)
	}
	var tlResult transport.ToolsListResult
	if err := json.Unmarshal(tlMsg.Result, &tlResult); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(tlResult.Tools) == 0 {
		t.Fatalf("tools/list returned no tools")
	}

	// 3. tools/call
	args, _ := json.Marshal(map[string]any{"input": "hello"})
	if err := conn.Send(transport.RequestEnvelope{
		JSONRPC: "2.0",
		ID:      3,
		Method:  transport.MethodToolsCall,
		Params:  transport.ToolsCallParams{Name: "echo", Arguments: args},
	}); err != nil {
		t.Fatalf("Send tools/call: %v", err)
	}
	tcMsg, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv tools/call: %v", err)
	}
	if tcMsg.Error != nil {
		t.Fatalf("tools/call error: %v", tcMsg.Error)
	}

	// Verify 3 POSTs were received.
	posts := fake.capturedPosts()
	if len(posts) != 3 {
		t.Errorf("expected 3 POSTs, got %d", len(posts))
	}
}

func TestConnectionEnvSubstitution(t *testing.T) {
	t.Parallel()
	var (
		mu       sync.Mutex
		seenHdrs stdhttp.Header
	)

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("/stream", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		mu.Lock()
		seenHdrs = r.Header.Clone()
		mu.Unlock()
		flusher := w.(stdhttp.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(stdhttp.StatusOK)
		flusher.Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/rpc", func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		mu.Lock()
		seenHdrs = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(stdhttp.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	conn := sse.NewConnection(sse.Spec{
		ID:      "auth",
		URL:     srv.URL + "/stream",
		PostURL: srv.URL + "/rpc",
		HeadersTemplate: map[string]string{
			"Authorization": "Bearer ${API_KEY}",
		},
		Env: map[string]string{
			"API_KEY": "secret123",
		},
		HTTPClient: srv.Client(),
	}, nil)

	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer conn.Close()

	// Verify the Authorization header was sent on the SSE GET.
	mu.Lock()
	auth := seenHdrs.Get("Authorization")
	mu.Unlock()
	if auth != "Bearer secret123" {
		t.Errorf("Authorization = %q, want %q", auth, "Bearer secret123")
	}
}

func TestConnectionPIDIsZero(t *testing.T) {
	t.Parallel()
	conn := sse.NewConnection(sse.Spec{ID: "x", URL: "http://example.com", PostURL: "http://example.com/rpc"}, nil)
	if conn.PID() != 0 {
		t.Errorf("PID = %d, want 0", conn.PID())
	}
}

func TestConnectionRejectsEmptyURL(t *testing.T) {
	t.Parallel()
	conn := sse.NewConnection(sse.Spec{ID: "x", PostURL: "http://example.com/rpc"}, nil)
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("Open with empty URL = nil; want error")
	}
}

func TestConnectionRejectsEmptyPostURL(t *testing.T) {
	t.Parallel()
	conn := sse.NewConnection(sse.Spec{ID: "x", URL: "http://example.com/stream"}, nil)
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("Open with empty PostURL = nil; want error")
	}
}

func TestConnectionDoubleOpen(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	conn := sse.NewConnection(sse.Spec{
		ID:         "x",
		URL:        fake.streamURL(),
		PostURL:    fake.postURL(),
		HTTPClient: fake.srv.Client(),
	}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Open(context.Background()); err == nil {
		t.Fatalf("second Open = nil; want error")
	}
}

func TestConnectionRecvReturnsEOFAfterClose(t *testing.T) {
	t.Parallel()
	fake := newFakeSSEServer(t)
	t.Cleanup(fake.srv.Close)

	conn := sse.NewConnection(sse.Spec{
		ID:         "eof",
		URL:        fake.streamURL(),
		PostURL:    fake.postURL(),
		HTTPClient: fake.srv.Client(),
	}, nil)
	if err := conn.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}

	_ = conn.Close()

	doneCh := make(chan error, 1)
	go func() {
		_, err := conn.Recv()
		doneCh <- err
	}()
	select {
	case err := <-doneCh:
		if !errors.Is(err, io.EOF) {
			t.Errorf("Recv after Close = %v, want io.EOF", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Recv after Close hung")
	}
}

func TestConnectionStderrTailOnHTTPError(t *testing.T) {
	t.Parallel()
	// SSE stream endpoint returns 500.
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.URL.Path == "/stream" {
			stdhttp.Error(w, "stream boom", stdhttp.StatusInternalServerError)
			return
		}
		w.WriteHeader(stdhttp.StatusAccepted)
	}))
	defer srv.Close()

	conn := sse.NewConnection(sse.Spec{
		ID:         "boom",
		URL:        srv.URL + "/stream",
		PostURL:    srv.URL + "/rpc",
		HTTPClient: srv.Client(),
	}, nil)
	err := conn.Open(context.Background())
	if err == nil {
		t.Fatalf("Open with 500 stream = nil; want error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention 500", err.Error())
	}
	if tail := conn.StderrTail(1024); tail == "" {
		t.Errorf("StderrTail empty after HTTP 500 on stream open")
	}
}

func TestConnectionFactoryRequiresURL(t *testing.T) {
	t.Parallel()
	f := &sse.ConnectionFactory{
		Spec: sse.Spec{ID: "x", PostURL: "http://example.com/rpc"},
	}
	if _, err := f.NewConnection("x"); err == nil {
		t.Fatalf("NewConnection with empty URL = nil; want error")
	}
}

func TestConnectionFactoryRequiresPostURL(t *testing.T) {
	t.Parallel()
	f := &sse.ConnectionFactory{
		Spec: sse.Spec{ID: "x", URL: "http://example.com/stream"},
	}
	if _, err := f.NewConnection("x"); err == nil {
		t.Fatalf("NewConnection with empty PostURL = nil; want error")
	}
}
