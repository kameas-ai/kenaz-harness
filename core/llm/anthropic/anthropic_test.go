package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// fakeServer wraps httptest.Server with helpers for crafting Messages
// API responses (success SSE, JSON error envelopes, mid-stream
// disconnects). Each test constructs one via newFakeServer.
type fakeServer struct {
	*httptest.Server
	mu              sync.Mutex
	lastBody        []byte
	lastHeaders     http.Header
	requestCount    int
}

// newFakeServer builds a fakeServer whose handler is supplied by the
// test. The handler receives the (response, request) pair plus the
// per-request count (1-indexed) so multi-attempt tests can vary
// behaviour by attempt.
func newFakeServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request, attempt int)) *fakeServer {
	t.Helper()
	fs := &fakeServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fs.mu.Lock()
		fs.lastBody = body
		fs.lastHeaders = r.Header.Clone()
		fs.requestCount++
		attempt := fs.requestCount
		fs.mu.Unlock()
		handler(w, r, attempt)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// writeSSE emits a sequence of SSE frames terminated by an empty line
// after each. Each frame may carry an optional "event:" header (we set
// it for clarity, but the parser only inspects the JSON "type" key).
func writeSSE(w http.ResponseWriter, frames []sseFrame) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, f := range frames {
		if f.event != "" {
			fmt.Fprintf(w, "event: %s\n", f.event)
		}
		fmt.Fprintf(w, "data: %s\n\n", f.data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

type sseFrame struct {
	event string
	data  string
}

// happyPathFrames constructs a minimal but realistic stream that
// exercises message_start, content_block_delta (text), message_delta
// (usage + stop_reason), and message_stop.
func happyPathFrames() []sseFrame {
	return []sseFrame{
		{event: "message_start", data: `{"type":"message_start","message":{"id":"msg_x","role":"assistant","model":"claude-sonnet-4-5","usage":{"input_tokens":7,"output_tokens":1}}}`},
		{event: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
		{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":", world"}}`},
		{event: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
		{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":7,"output_tokens":3}}`},
		{event: "message_stop", data: `{"type":"message_stop"}`},
	}
}

// newAdapter constructs an Adapter pointed at fs with a permissive
// transport. The catalog defaults to the embedded one.
func newAdapter(fs *fakeServer) *Adapter {
	return New(WithEndpoint(fs.URL), WithHTTPClient(fs.Client()))
}

// stdReq is a minimal request good enough for the smoke tests.
func stdReq() (llm.GenerationRequest, llm.ProviderProfile) {
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	prof := llm.ProviderProfile{
		ID:    "p-anthropic",
		Kind:  Kind,
		Model: "claude-sonnet-4-5",
		Cred:  llm.CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	return req, prof
}

// drain reads every chunk and returns the concatenated text plus the
// final Response and any final error.
func drain(t *testing.T, s llm.Stream) (string, llm.Response, error) {
	t.Helper()
	var b strings.Builder
	for ev := range s.Events() {
		if ev.Kind == llm.StreamText {
			b.WriteString(ev.Text)
		}
	}
	resp, err := s.Final()
	return b.String(), resp, err
}

func TestAdapter_HappyPathStreaming(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-ant-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, resp, ferr := drain(t, stream)
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if text != "Hello, world" {
		t.Fatalf("text = %q, want %q", text, "Hello, world")
	}
	if resp.FinishReason != "end_turn" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if resp.Usage.OutputTokens != 3 || resp.Usage.InputTokens != 7 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// Header / body sanity: x-api-key, anthropic-version, stream=true.
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if got := fs.lastHeaders.Get("x-api-key"); got != "sk-ant-test" {
		t.Fatalf("x-api-key = %q", got)
	}
	if got := fs.lastHeaders.Get("anthropic-version"); got != defaultAPIVersion {
		t.Fatalf("anthropic-version = %q", got)
	}
	if !strings.Contains(string(fs.lastBody), `"stream":true`) {
		t.Fatalf("body should set stream=true, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"model":"claude-sonnet-4-5"`) {
		t.Fatalf("body should carry model, got %s", fs.lastBody)
	}
}

func TestAdapter_EmptyResponse(t *testing.T) {
	// Server writes a 200 with nothing on the body. Parser should
	// finish cleanly with no text and a synthesized finish reason.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, resp, ferr := drain(t, stream)
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if text != "" {
		t.Fatalf("text should be empty, got %q", text)
	}
	if resp.FinishReason != "" {
		t.Fatalf("expected blank finish reason on empty stream, got %q", resp.FinishReason)
	}
}

func TestAdapter_ErrorClassification(t *testing.T) {
	// Table-driven: status, body → expected typed-error matcher.
	cases := []struct {
		name       string
		status     int
		body       string
		assertErr  func(t *testing.T, err error)
	}{
		{
			name:   "401 auth",
			status: 401,
			body:   `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			assertErr: func(t *testing.T, err error) {
				var ae *llm.ErrAuth
				if !errors.As(err, &ae) {
					t.Fatalf("expected ErrAuth, got %T %v", err, err)
				}
				if ae.Status != 401 {
					t.Fatalf("status = %d", ae.Status)
				}
				if !strings.Contains(ae.Message, "authentication_error") {
					t.Fatalf("message = %q", ae.Message)
				}
			},
		},
		{
			name:   "403 auth",
			status: 403,
			body:   `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`,
			assertErr: func(t *testing.T, err error) {
				var ae *llm.ErrAuth
				if !errors.As(err, &ae) {
					t.Fatalf("expected ErrAuth, got %T %v", err, err)
				}
			},
		},
		{
			name:   "429 transient",
			status: 429,
			body:   `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`,
			assertErr: func(t *testing.T, err error) {
				if !llm.IsTransient(err) {
					t.Fatalf("expected transient, got %T %v", err, err)
				}
			},
		},
		{
			name:   "503 transient",
			status: 503,
			body:   `{"type":"error","error":{"type":"overloaded_error","message":"upstream down"}}`,
			assertErr: func(t *testing.T, err error) {
				if !llm.IsTransient(err) {
					t.Fatalf("expected transient, got %T %v", err, err)
				}
			},
		},
		{
			name:   "400 invalid request (non-retryable)",
			status: 400,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"bad shape"}}`,
			assertErr: func(t *testing.T, err error) {
				var ir *llm.ErrInvalidRequest
				if !errors.As(err, &ir) {
					t.Fatalf("expected ErrInvalidRequest, got %T %v", err, err)
				}
				if llm.IsTransient(err) {
					t.Fatalf("400 must not be transient")
				}
			},
		},
		{
			name:   "400 content policy refusal (non-retryable)",
			status: 400,
			body:   `{"type":"error","error":{"type":"invalid_request_error","message":"content_policy: refused"}}`,
			assertErr: func(t *testing.T, err error) {
				var ir *llm.ErrInvalidRequest
				if !errors.As(err, &ir) {
					t.Fatalf("expected ErrInvalidRequest, got %T %v", err, err)
				}
				if !strings.Contains(ir.Message, "content_policy") {
					t.Fatalf("expected content_policy message, got %q", ir.Message)
				}
				if llm.IsTransient(err) {
					t.Fatalf("content policy refusal must not be transient")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			a := newAdapter(fs)
			req, prof := stdReq()
			_, err := a.Stream(context.Background(), req, prof, []byte("k"))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			tc.assertErr(t, err)
		})
	}
}

func TestAdapter_MidStreamDisconnect(t *testing.T) {
	// Server writes one delta then forcibly closes the connection.
	// The pump must surface a StreamError chunk and Final must return
	// the partial Response with an error.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n",
			`{"type":"message_start","message":{"id":"x","model":"y","usage":{"input_tokens":1,"output_tokens":0}}}`)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"part"}}`)
		if flusher != nil {
			flusher.Flush()
		}
		// Hijack and close the underlying connection mid-stream.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatalf("server does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		_ = conn.Close()
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sawText, sawErr bool
	for ev := range stream.Events() {
		switch ev.Kind {
		case llm.StreamText:
			sawText = true
		case llm.StreamError:
			sawErr = true
		}
	}
	_, ferr := stream.Final()
	if !sawText {
		t.Fatal("expected at least one text chunk before disconnect")
	}
	if !sawErr && ferr == nil {
		t.Fatal("expected mid-stream error to surface (chunk OR Final)")
	}
}

func TestAdapter_CancelTerminatesStream(t *testing.T) {
	// Server writes one delta then sleeps. The adapter's Cancel()
	// should sever the connection within ~1s.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)
		if flusher != nil {
			flusher.Flush()
		}
		// Block until ctx is cancelled or a long timeout passes.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Read at least one chunk to confirm the stream is open.
	got := false
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	select {
	case ev, ok := <-stream.Events():
		if ok && ev.Kind == llm.StreamText {
			got = true
		}
	case <-deadline.C:
	}
	if !got {
		t.Fatal("expected an initial text chunk before cancel")
	}
	if err := stream.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	// Drain remaining events; should close quickly.
	closeDeadline := time.NewTimer(2 * time.Second)
	defer closeDeadline.Stop()
	doneCh := make(chan struct{})
	go func() {
		for range stream.Events() {
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-closeDeadline.C:
		t.Fatal("stream did not close within 2s of Cancel")
	}
	_, ferr := stream.Final()
	var ce *llm.ErrCancelled
	if !errors.As(ferr, &ce) {
		t.Fatalf("expected ErrCancelled after Cancel, got %v", ferr)
	}
}

func TestAdapter_ContextCancellationPropagates(t *testing.T) {
	// ctx-driven cancellation: ctx.Done() must close the stream.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`)
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := a.Stream(ctx, req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Read the first chunk before cancelling.
	select {
	case <-stream.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("no chunks received within 2s")
	}
	cancel()
	doneCh := make(chan struct{})
	go func() {
		for range stream.Events() {
		}
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close within 2s of ctx cancel")
	}
}

func TestAdapter_ToolUseAccumulation(t *testing.T) {
	// Validates that input_json_delta frames are reassembled into a
	// single ToolUse on content_block_stop.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, []sseFrame{
			{event: "message_start", data: `{"type":"message_start","message":{"id":"x","usage":{"input_tokens":3,"output_tokens":0}}}`},
			{event: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"calc","input":{}}}`},
			{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`},
			{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"42}"}}`},
			{event: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
			{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":3,"output_tokens":4}}`},
			{event: "message_stop", data: `{"type":"message_stop"}`},
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Tools = []llm.ToolSpec{{Name: "calc", Description: "add"}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var tools []*llm.ToolUse
	for ev := range stream.Events() {
		if ev.Kind == llm.StreamTool && ev.Tool != nil {
			tu := *ev.Tool
			tools = append(tools, &tu)
		}
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool event, got %d", len(tools))
	}
	if tools[0].Name != "calc" {
		t.Fatalf("tool name = %q", tools[0].Name)
	}
	if string(tools[0].Input) != `{"a":42}` {
		t.Fatalf("tool input = %s, want %q", tools[0].Input, `{"a":42}`)
	}
	if resp.FinishReason != "tool_use" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

func TestAdapter_EmptyCredentialRejected(t *testing.T) {
	// No fake server should be hit; the adapter rejects empty cred
	// before opening the connection.
	a := New()
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, nil)
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected ErrAuth, got %v", err)
	}
}

func TestAdapter_CapabilitiesFromCatalog(t *testing.T) {
	a := New()
	desc := a.Capabilities("claude-sonnet-4-5")
	// Anthropic catalog reports streaming + tool_calling for sonnet.
	if !desc.Has(llm.CapStreaming) {
		t.Fatal("expected streaming")
	}
	if !desc.Has(llm.CapToolCalling) {
		t.Fatal("expected tool_calling")
	}
}

func TestAdapter_KindIsAnthropic(t *testing.T) {
	if New().Kind() != "anthropic" {
		t.Fatal("Kind must be 'anthropic'")
	}
}

func TestAdapter_ListModels(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("expected x-api-key header to be forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"claude-sonnet-4-5","display_name":"Claude Sonnet 4.5","type":"model"},
			{"id":"claude-opus-4-7","display_name":"Claude Opus 4.7","type":"model"}
		]}`))
	})
	a := newAdapter(fs)
	models, err := a.ListModels(context.Background(), []byte("sk-test"))
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].ID != "claude-sonnet-4-5" || models[0].DisplayName != "Claude Sonnet 4.5" {
		t.Fatalf("first model = %+v", models[0])
	}
	// ContextWindow should be populated from the curated catalog.
	if models[0].ContextWindow != 200_000 {
		t.Errorf("expected ContextWindow=200000 for claude-sonnet-4-5, got %d", models[0].ContextWindow)
	}
}

func TestAdapter_ListModels_EmptyCredential(t *testing.T) {
	a := New()
	_, err := a.ListModels(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty credential")
	}
}

// TestAdapter_TextOnly_UsesStringContent verifies that a request with
// only text blocks emits the legacy string-shaped `content` field, not
// the array-of-blocks form. This is the wire-shape stability guarantee
// for the text-only path: existing Anthropic deployments continue to
// see the same body for plain chat turns.
func TestAdapter_TextOnly_UsesStringContent(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(body.Messages))
	}
	// content should be a JSON string, not an array.
	if !strings.HasPrefix(strings.TrimSpace(string(body.Messages[0].Content)), `"`) {
		t.Fatalf("expected string-shaped content for text-only message, got %s", body.Messages[0].Content)
	}
}

// TestAdapter_ImageBlock_Serialized verifies that a single image block
// is mapped to Anthropic's {type:"image", source:{type:"base64",
// media_type, data}} shape and rides inside an array-of-blocks content
// field (any non-text block forces the array form).
func TestAdapter_ImageBlock_Serialized(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: "text", Text: "describe this"},
			{Type: "image", Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "image/png",
				Data:      "iVBORw0KGgo=",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Messages) != 1 {
		t.Fatalf("messages = %d", len(body.Messages))
	}
	parts := body.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("content parts = %d, want 2 (body=%s)", len(parts), fs.lastBody)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "describe this" {
		t.Fatalf("text part wrong: %+v", parts[0])
	}
	if parts[1]["type"] != "image" {
		t.Fatalf("image part type = %v", parts[1]["type"])
	}
	src, ok := parts[1]["source"].(map[string]any)
	if !ok {
		t.Fatalf("image source missing: %+v", parts[1])
	}
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "iVBORw0KGgo=" {
		t.Fatalf("image source wrong: %+v", src)
	}
}

// TestAdapter_DocumentBlock_Serialized verifies that a document block
// is mapped to Anthropic's {type:"document", source:{...}} shape with
// application/pdf media type.
func TestAdapter_DocumentBlock_Serialized(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Messages = []llm.Message{{
		Role: llm.RoleUser,
		Content: []llm.ContentBlock{
			{Type: "text", Text: "summarize"},
			{Type: "document", Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "application/pdf",
				Data:      "JVBERi0=",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Messages []struct {
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Messages) != 1 || len(body.Messages[0].Content) != 2 {
		t.Fatalf("messages/content shape unexpected: %s", fs.lastBody)
	}
	doc := body.Messages[0].Content[1]
	if doc["type"] != "document" {
		t.Fatalf("doc type = %v", doc["type"])
	}
	src, ok := doc["source"].(map[string]any)
	if !ok {
		t.Fatalf("doc source missing: %+v", doc)
	}
	if src["media_type"] != "application/pdf" || src["data"] != "JVBERi0=" {
		t.Fatalf("doc source wrong: %+v", src)
	}
}

// TestConvertContent_ToolResult_NormalizesObjectPayload guards the wire
// shape Anthropic requires for tool_result content blocks: a string OR a
// list of content blocks. A bare JSON object is rejected with
// "Found an object, but `tool_result` content must either be a string or
// a list of content blocks" — the bug surfaced from a tool returning a
// structured result on May 2 2026.
func TestConvertContent_ToolResult_NormalizesObjectPayload(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    func(t *testing.T, got any)
	}{
		{
			name:    "object payload wrapped as text block",
			payload: `{"foo":"bar","n":1}`,
			want: func(t *testing.T, got any) {
				blocks, ok := got.([]map[string]any)
				if !ok {
					t.Fatalf("want []map[string]any, got %T (%v)", got, got)
				}
				if len(blocks) != 1 || blocks[0]["type"] != "text" {
					t.Fatalf("want one text block, got %+v", blocks)
				}
				if blocks[0]["text"] != `{"foo":"bar","n":1}` {
					t.Fatalf("text payload not preserved: %v", blocks[0]["text"])
				}
			},
		},
		{
			name:    "string payload passes through",
			payload: `"hello world"`,
			want: func(t *testing.T, got any) {
				if got != "hello world" {
					t.Fatalf("want plain string, got %T %v", got, got)
				}
			},
		},
		{
			name:    "array payload passes through",
			payload: `[{"type":"text","text":"a"}]`,
			want: func(t *testing.T, got any) {
				arr, ok := got.([]any)
				if !ok || len(arr) != 1 {
					t.Fatalf("want []any of length 1, got %T %v", got, got)
				}
			},
		},
		{
			name:    "number payload wrapped as text block",
			payload: `42`,
			want: func(t *testing.T, got any) {
				blocks, ok := got.([]map[string]any)
				if !ok || len(blocks) != 1 || blocks[0]["text"] != "42" {
					t.Fatalf("want wrapped number, got %+v", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parts := []llm.ContentBlock{{
				Type: "tool_result",
				ToolResult: &llm.ToolResult{
					ToolUseID: "tu_1",
					Content:   json.RawMessage(tc.payload),
				},
			}}
			out := convertContent(parts)
			if len(out) != 1 || out[0]["type"] != "tool_result" {
				t.Fatalf("convertContent did not emit tool_result: %+v", out)
			}
			tc.want(t, out[0]["content"])
		})
	}
}

func TestAdapter_ListModels_HTTPError(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid x-api-key"}`))
	})
	a := newAdapter(fs)
	_, err := a.ListModels(context.Background(), []byte("sk-bad"))
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

// ── Structured-output tests (structured-output-and-grammar-01KX5R8A WP03a) ──

// TestApplyResponseFormat_JSONSchema_InjectsTool verifies that Mode="json_schema"
// injects the synthetic "_structured_output" tool and forces tool_choice.
func TestApplyResponseFormat_JSONSchema_InjectsTool(t *testing.T) {
	a := New()
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	body := map[string]any{}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools missing or wrong type: %+v", body)
	}
	lastTool := tools[len(tools)-1].(map[string]any)
	if lastTool["name"] != "_structured_output" {
		t.Fatalf("synthetic tool name = %v, want _structured_output", lastTool["name"])
	}
	tc, ok := body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice missing: %+v", body)
	}
	if tc["type"] != "tool" || tc["name"] != "_structured_output" {
		t.Fatalf("tool_choice = %+v, want type=tool name=_structured_output", tc)
	}
}

// TestApplyResponseFormat_Grammar_ReturnsUnsupported verifies that grammar mode
// returns ErrUnsupportedFormat for the Anthropic adapter.
func TestApplyResponseFormat_Grammar_ReturnsUnsupported(t *testing.T) {
	a := New()
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "grammar", Grammar: []byte("root ::= [a-z]+")},
	}
	body := map[string]any{}
	err := a.ApplyResponseFormat(&req, body)
	if err == nil {
		t.Fatal("expected ErrUnsupportedFormat, got nil")
	}
	if !llm.IsUnsupportedFormat(err) {
		t.Fatalf("expected ErrUnsupportedFormat, got %T: %v", err, err)
	}
}

// TestApplyResponseFormat_JSONMode_AppendsSysPrompt verifies that Mode="json"
// appends a JSON instruction to the system string.
func TestApplyResponseFormat_JSONMode_AppendsSysPrompt(t *testing.T) {
	a := New()
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json"},
	}
	body := map[string]any{"system": "Be helpful."}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sys, ok := body["system"].(string)
	if !ok {
		t.Fatalf("system missing: %+v", body)
	}
	if !contains(sys, "JSON") {
		t.Fatalf("system does not contain JSON instruction: %q", sys)
	}
}

// ── WP04 synthetic tool normalization tests (multimodal-io-extended-01KQ8TD2) ─

// TestSyntheticToolNormalization_JSONSchema verifies that when the model
// responds with the _structured_output tool_use block (due to json_schema mode),
// Final().Content[0].Type == "text" and the text is the raw JSON input.
func TestSyntheticToolNormalization_JSONSchema(t *testing.T) {
	// Simulate the model responding with the synthetic _structured_output tool.
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, []sseFrame{
			{event: "message_start", data: `{"type":"message_start","message":{"id":"x","usage":{"input_tokens":10,"output_tokens":0}}}`},
			{event: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_so","name":"_structured_output","input":{}}}`},
			{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"name\":\"Alice\""}}`},
			{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"}"}}`},
			{event: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
			{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":10,"output_tokens":5}}`},
			{event: "message_stop", data: `{"type":"message_stop"}`},
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.ResponseFormat = &llm.ResponseFormat{
		Mode:   "json_schema",
		Schema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`),
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	// Acceptance: Final().Content[0].Type == "text" with valid JSON.
	if len(resp.Content) != 1 {
		t.Fatalf("content blocks = %d, want 1", len(resp.Content))
	}
	if resp.Content[0].Type != "text" {
		t.Fatalf("content[0].Type = %q, want \"text\"", resp.Content[0].Type)
	}
	if !json.Valid([]byte(resp.Content[0].Text)) {
		t.Fatalf("content[0].Text is not valid JSON: %q", resp.Content[0].Text)
	}
	// Synthetic tool must NOT appear in ToolCalls.
	for _, tc := range resp.ToolCalls {
		if tc.Name == "_structured_output" {
			t.Fatalf("_structured_output tool leaked into ToolCalls: %+v", tc)
		}
	}
}

// TestSyntheticToolNormalization_JSONMode verifies the same normalization
// applies when the request uses JSONModeSpec.Schema (WP03/WP04 path).
func TestSyntheticToolNormalization_JSONMode(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, []sseFrame{
			{event: "message_start", data: `{"type":"message_start","message":{"id":"x","usage":{"input_tokens":8,"output_tokens":0}}}`},
			{event: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"t1","name":"_my_schema","input":{}}}`},
			{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"count\":3}"}}`},
			{event: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
			{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":8,"output_tokens":3}}`},
			{event: "message_stop", data: `{"type":"message_stop"}`},
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.JSONMode = &llm.JSONModeSpec{
		Enabled: true,
		Schema:  json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`),
		Name:    "my_schema",
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("k"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if len(resp.Content) != 1 || resp.Content[0].Type != "text" {
		t.Fatalf("expected text content, got: %+v", resp.Content)
	}
	if !json.Valid([]byte(resp.Content[0].Text)) {
		t.Fatalf("not valid JSON: %q", resp.Content[0].Text)
	}
}

// ── WP03 JSONMode wire-shape tests (multimodal-io-extended-01KQ8TD2) ─────────

// TestJSONModeAnthropic_NoSchema_AppendsSysPrompt verifies that JSONModeSpec
// without a schema appends a JSON instruction to the system string.
func TestJSONModeAnthropic_NoSchema_AppendsSysPrompt(t *testing.T) {
	body := map[string]any{"system": "Be concise."}
	jm := &llm.JSONModeSpec{Enabled: true}
	if err := applyJSONModeAnthropic(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sys, ok := body["system"].(string)
	if !ok {
		t.Fatalf("system missing: %+v", body)
	}
	if !contains(sys, "JSON") {
		t.Fatalf("system does not contain JSON instruction: %q", sys)
	}
}

// TestJSONModeAnthropic_WithSchema_InjectsTool verifies that JSONModeSpec
// with a schema injects a synthetic tool and forces tool_choice.
func TestJSONModeAnthropic_WithSchema_InjectsTool(t *testing.T) {
	body := map[string]any{}
	schema := json.RawMessage(`{"type":"object","properties":{"result":{"type":"string"}},"required":["result"]}`)
	jm := &llm.JSONModeSpec{Enabled: true, Schema: schema, Name: "my_result"}
	if err := applyJSONModeAnthropic(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools not injected: %+v", body)
	}
	toolMap, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool not a map: %+v", tools[0])
	}
	if toolMap["name"] != "_my_result" {
		t.Fatalf("tool name = %v want _my_result", toolMap["name"])
	}
	tc, ok := body["tool_choice"].(map[string]any)
	if !ok {
		t.Fatalf("tool_choice missing: %+v", body)
	}
	if tc["name"] != "_my_result" {
		t.Fatalf("tool_choice.name = %v want _my_result", tc["name"])
	}
}

// TestJSONModeAnthropic_DefaultToolName verifies that Name="" uses
// "_structured_output" as the tool name.
func TestJSONModeAnthropic_DefaultToolName(t *testing.T) {
	body := map[string]any{}
	schema := json.RawMessage(`{"type":"object"}`)
	jm := &llm.JSONModeSpec{Enabled: true, Schema: schema}
	if err := applyJSONModeAnthropic(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := body["tools"].([]any)
	toolMap := tools[0].(map[string]any)
	if toolMap["name"] != "_structured_output" {
		t.Fatalf("default tool name = %v want _structured_output", toolMap["name"])
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
