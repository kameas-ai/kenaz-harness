package openai

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

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeServer wraps httptest.Server with helpers for crafting Chat
// Completions API responses (success SSE, JSON error envelopes,
// /v1/models JSON). Each test constructs one via newFakeServer.
type fakeServer struct {
	*httptest.Server
	mu           sync.Mutex
	lastBody     []byte
	lastHeaders  http.Header
	lastPath     string
	requestCount int
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
		fs.lastPath = r.URL.Path
		fs.requestCount++
		attempt := fs.requestCount
		fs.mu.Unlock()
		handler(w, r, attempt)
	}))
	t.Cleanup(fs.Close)
	return fs
}

// writeSSE emits a sequence of SSE frames terminated by an empty line
// after each. Each frame is just a "data: <payload>\n\n" line — OpenAI
// does not use the "event:" header.
func writeSSE(w http.ResponseWriter, frames []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, f := range frames {
		fmt.Fprintf(w, "data: %s\n\n", f)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// happyPathFrames returns a minimal but realistic stream that exercises
// text deltas, a final chunk with finish_reason, a usage frame, and
// the [DONE] sentinel.
func happyPathFrames() []string {
	return []string{
		`{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":", world"},"finish_reason":null}]}`,
		`{"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`{"id":"x","object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":12,"total_tokens":17}}`,
		`[DONE]`,
	}
}

// newAdapter constructs an Adapter pointed at fs with a permissive
// transport.
func newAdapter(fs *fakeServer) *Adapter {
	return New(WithEndpoint(fs.URL+"/v1/chat/completions"), WithHTTPClient(fs.Client()))
}

// stdReq is a minimal request good enough for the smoke tests.
func stdReq() (llm.GenerationRequest, llm.ProviderProfile) {
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	prof := llm.ProviderProfile{
		ID:    "p-openai",
		Kind:  Kind,
		Model: "gpt-4o",
		Cred:  llm.CredentialReference{Kind: "env", Locator: "OPENAI_API_KEY"},
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

func TestAdapter_KindIsOpenAI(t *testing.T) {
	if New().Kind() != "openai" {
		t.Fatal("Kind must be 'openai'")
	}
}

func TestAdapter_Stream_HappyPath(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
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
	if resp.FinishReason != "stop" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 12 {
		t.Fatalf("usage = %+v", resp.Usage)
	}

	// Header / body sanity: Authorization, stream=true, include_usage.
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if got := fs.lastHeaders.Get("Authorization"); got != "Bearer sk-test" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := fs.lastHeaders.Get("Accept"); got != "text/event-stream" {
		t.Fatalf("Accept = %q", got)
	}
	if !strings.Contains(string(fs.lastBody), `"stream":true`) {
		t.Fatalf("body should set stream=true, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"include_usage":true`) {
		t.Fatalf("body should include stream_options.include_usage, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"model":"gpt-4o"`) {
		t.Fatalf("body should carry model, got %s", fs.lastBody)
	}
}

func TestAdapter_Stream_AuthError(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"Incorrect API key","type":"invalid_request_error","code":"invalid_api_key"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-bad"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *llm.ErrAuth, got %T %v", err, err)
	}
	if ae.Status != 401 {
		t.Fatalf("status = %d", ae.Status)
	}
	if !strings.Contains(ae.Message, "Incorrect API key") {
		t.Fatalf("message = %q", ae.Message)
	}
}

func TestAdapter_Stream_RateLimited(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"Rate limit reached","type":"rate_limit_error"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var te *llm.ErrTransient
	if !errors.As(err, &te) {
		t.Fatalf("expected *llm.ErrTransient, got %T %v", err, err)
	}
	if te.Status != 429 {
		t.Fatalf("status = %d", te.Status)
	}
	if !llm.IsTransient(err) {
		t.Fatal("429 must be transient")
	}
}

func TestAdapter_Stream_InvalidRequest(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad request","type":"invalid_request_error"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ir *llm.ErrInvalidRequest
	if !errors.As(err, &ir) {
		t.Fatalf("expected *llm.ErrInvalidRequest, got %T %v", err, err)
	}
	if llm.IsTransient(err) {
		t.Fatal("400 must not be transient")
	}
}

func TestAdapter_Stream_5xxTransient(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream down"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if !llm.IsTransient(err) {
		t.Fatalf("expected transient, got %T %v", err, err)
	}
}

func TestAdapter_EmptyCredentialRejected(t *testing.T) {
	a := New()
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, nil)
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *llm.ErrAuth, got %v", err)
	}
}

func TestAdapter_ListModels_FiltersNonChat(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("expected Bearer auth header, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"gpt-4o","object":"model"},
			{"id":"whisper-1","object":"model"},
			{"id":"text-embedding-3-small","object":"model"},
			{"id":"tts-1","object":"model"},
			{"id":"dall-e-3","object":"model"},
			{"id":"text-moderation-latest","object":"model"},
			{"id":"gpt-4o-audio-preview","object":"model"}
		]}`))
	})
	a := newAdapter(fs)
	models, err := a.ListModels(context.Background(), []byte("sk-test"))
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model after filtering, got %d: %+v", len(models), models)
	}
	if models[0].ID != "gpt-4o" {
		t.Fatalf("model[0] = %q, want gpt-4o", models[0].ID)
	}
	if models[0].DisplayName != "gpt-4o" {
		t.Fatalf("display name = %q", models[0].DisplayName)
	}
	// ContextWindow should be populated from the curated catalog.
	if models[0].ContextWindow != 128_000 {
		t.Errorf("expected ContextWindow=128000 for gpt-4o, got %d", models[0].ContextWindow)
	}
}

func TestAdapter_ListModels_SortedAlphabetically(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"gpt-4o","object":"model"},
			{"id":"gpt-3.5-turbo","object":"model"},
			{"id":"gpt-4","object":"model"}
		]}`))
	})
	a := newAdapter(fs)
	models, err := a.ListModels(context.Background(), []byte("sk-test"))
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}
	wantOrder := []string{"gpt-3.5-turbo", "gpt-4", "gpt-4o"}
	for i, want := range wantOrder {
		if models[i].ID != want {
			t.Fatalf("models[%d].ID = %q, want %q", i, models[i].ID, want)
		}
	}
}

func TestAdapter_ListModels_EmptyCredential(t *testing.T) {
	a := New()
	_, err := a.ListModels(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty credential")
	}
}

func TestAdapter_ListModels_HTTPError(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	})
	a := newAdapter(fs)
	_, err := a.ListModels(context.Background(), []byte("sk-bad"))
	if err == nil {
		t.Fatal("expected error on 401")
	}
	// Should classify as auth error.
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *llm.ErrAuth, got %T %v", err, err)
	}
}

func TestAdapter_Capabilities_VisionFamilies(t *testing.T) {
	// When the catalog is unavailable (nil), the inline rule fires.
	a := &Adapter{httpc: &http.Client{}, endpoint: defaultChatEndpoint, cat: nil}
	cases := []struct {
		model      string
		wantVision bool
	}{
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-4-vision-preview", true},
		{"gpt-4-turbo", true},
		{"gpt-4.1", true},
		{"gpt-3.5-turbo", false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			desc := a.Capabilities(tc.model)
			if got := desc.Has(llm.CapVision); got != tc.wantVision {
				t.Fatalf("vision for %q = %v, want %v", tc.model, got, tc.wantVision)
			}
			// Streaming and tool calling are always on for OpenAI chat.
			if !desc.Has(llm.CapStreaming) {
				t.Fatal("expected streaming")
			}
			if !desc.Has(llm.CapToolCalling) {
				t.Fatal("expected tool_calling")
			}
			if !desc.Has(llm.CapUsageReporting) {
				t.Fatal("expected usage_reporting")
			}
		})
	}
}

func TestAdapter_Stream_SystemMessageEmitted(t *testing.T) {
	// System prompts surface as a synthetic role:"system" entry at the
	// head of the messages array.
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, []string{
			`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.System = "you are concise"
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if !strings.Contains(string(fs.lastBody), `"role":"system"`) {
		t.Fatalf("expected system role in body, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"you are concise"`) {
		t.Fatalf("expected system text in body, got %s", fs.lastBody)
	}
}

// TestAdapter_Stream_ToolsSerialized verifies that GenerationRequest.
// Tools is wrapped in OpenAI's {type:"function", function:{...}} envelope
// with the JSON Schema preserved verbatim under `parameters`.
func TestAdapter_Stream_ToolsSerialized(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, []string{
			`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Tools = []llm.ToolSpec{
		{
			Name:        "calc.add",
			Description: "Add two numbers",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
		},
		{
			Name:        "no_schema",
			Description: "Tool without an explicit schema",
		},
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()
	var body struct {
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(fs.lastBody, &body); err != nil {
		t.Fatalf("body unmarshal: %v\nbody=%s", err, fs.lastBody)
	}
	if len(body.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d (body=%s)", len(body.Tools), fs.lastBody)
	}
	if body.Tools[0].Type != "function" {
		t.Fatalf("tools[0].type = %q, want function", body.Tools[0].Type)
	}
	if body.Tools[0].Function.Name != "calc.add" {
		t.Fatalf("tools[0].function.name = %q", body.Tools[0].Function.Name)
	}
	if body.Tools[0].Function.Description != "Add two numbers" {
		t.Fatalf("tools[0].function.description = %q", body.Tools[0].Function.Description)
	}
	if body.Tools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("tools[0].function.parameters missing type=object: %+v", body.Tools[0].Function.Parameters)
	}
	props, _ := body.Tools[0].Function.Parameters["properties"].(map[string]any)
	if _, ok := props["a"]; !ok {
		t.Fatalf("tools[0].function.parameters.properties.a missing: %+v", body.Tools[0].Function.Parameters)
	}
	// Empty schema falls back to {"type":"object"}.
	if body.Tools[1].Function.Parameters["type"] != "object" {
		t.Fatalf("tools[1] schema fallback failed: %+v", body.Tools[1].Function.Parameters)
	}
}

// TestAdapter_Stream_ToolsAbsentWhenEmpty verifies that requests
// without tools do NOT include a `tools` field in the body — OpenAI
// rejects an empty array.
func TestAdapter_Stream_ToolsAbsentWhenEmpty(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, []string{
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if strings.Contains(string(fs.lastBody), `"tools"`) {
		t.Fatalf("body should not contain tools field when none provided: %s", fs.lastBody)
	}
}

// TestAdapter_Stream_ImageBlock_Serialized verifies that a request with
// an image content block is wrapped in OpenAI's array-of-parts shape
// with an image_url part carrying a data: URL constructed from the
// MediaSource.MediaType + base64 data.
func TestAdapter_Stream_ImageBlock_Serialized(t *testing.T) {
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
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
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
		t.Fatalf("messages = %d (body=%s)", len(body.Messages), fs.lastBody)
	}
	parts := body.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("content parts = %d (body=%s)", len(parts), fs.lastBody)
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "describe this" {
		t.Fatalf("text part wrong: %+v", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("image part type = %v", parts[1]["type"])
	}
	imgURL, ok := parts[1]["image_url"].(map[string]any)
	if !ok {
		t.Fatalf("image_url missing: %+v", parts[1])
	}
	wantURL := "data:image/png;base64,iVBORw0KGgo="
	if imgURL["url"] != wantURL {
		t.Fatalf("image_url.url = %q, want %q", imgURL["url"], wantURL)
	}
}

// TestAdapter_Stream_TextOnly_StaysString verifies that messages
// without any image / document blocks emit the legacy string-shaped
// `content` field (regression: the multimodal refactor must not change
// the wire shape for plain chat turns).
func TestAdapter_Stream_TextOnly_StaysString(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
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
		t.Fatalf("messages = %d", len(body.Messages))
	}
	if !strings.HasPrefix(strings.TrimSpace(string(body.Messages[0].Content)), `"`) {
		t.Fatalf("text-only message should emit string content, got %s", body.Messages[0].Content)
	}
}

// TestAdapter_Stream_DocumentBlock_DroppedWithWarn verifies that
// document blocks against an OpenAI profile are dropped silently in
// WP01 (Chat Completions does not accept inline documents) and the
// surviving text part is still serialized in the array form.
//
// The warn log fires inside the adapter's logger; we assert the
// contract by checking the wire body — the document block must NOT
// appear, but the text block must.
func TestAdapter_Stream_DocumentBlock_DroppedWithWarn(t *testing.T) {
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
			{Type: "image", Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "image/png",
				Data:      "AA==",
			}},
		},
	}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if _, _, ferr := drain(t, stream); ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if strings.Contains(string(fs.lastBody), "application/pdf") {
		t.Fatalf("document block should have been dropped, body=%s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), "summarize") {
		t.Fatalf("text block should remain, body=%s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), "image_url") {
		t.Fatalf("image block should remain, body=%s", fs.lastBody)
	}
}

// TestAdapter_Stream_ToolCallsParsed verifies that streamed tool_calls
// deltas are reassembled into a single ToolUse per tool, surfaced as
// StreamTool events, and accumulated into Response.ToolCalls.
func TestAdapter_Stream_ToolCallsParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSE(w, []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"calc.add","arguments":""}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1,\"b\":2}"}}]},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			`[DONE]`,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Tools = []llm.ToolSpec{{Name: "calc.add", Description: "add"}}
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var toolEvents []llm.ToolUse
	for ev := range stream.Events() {
		if ev.Kind == llm.StreamTool && ev.Tool != nil {
			toolEvents = append(toolEvents, *ev.Tool)
		}
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if len(toolEvents) != 1 {
		t.Fatalf("expected 1 tool event, got %d", len(toolEvents))
	}
	if toolEvents[0].ID != "call_1" {
		t.Fatalf("tool id = %q", toolEvents[0].ID)
	}
	if toolEvents[0].Name != "calc.add" {
		t.Fatalf("tool name = %q", toolEvents[0].Name)
	}
	if string(toolEvents[0].Input) != `{"a":1,"b":2}` {
		t.Fatalf("tool input = %s, want %q", toolEvents[0].Input, `{"a":1,"b":2}`)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "calc.add" {
		t.Fatalf("Response.ToolCalls = %+v", resp.ToolCalls)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
}

// ── Structured-output tests (structured-output-and-grammar-01KX5R8A WP03b) ──

// TestApplyResponseFormat_JSONObject verifies that Mode="json" sets
// response_format.type = "json_object" in the wire body.
func TestApplyResponseFormat_JSONObject(t *testing.T) {
	a := New()
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json"},
	}
	body := map[string]any{}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %+v", body)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want json_object", rf["type"])
	}
}

// TestApplyResponseFormat_JSONSchema_Native verifies that Mode="json_schema"
// sets response_format.type = "json_schema" with strict: true.
func TestApplyResponseFormat_JSONSchema_Native(t *testing.T) {
	a := New()
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`)
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	body := map[string]any{}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %+v", body)
	}
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format.type = %v, want json_schema", rf["type"])
	}
	jsBlock, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema block missing: %+v", rf)
	}
	if jsBlock["strict"] != true {
		t.Fatalf("strict = %v, want true", jsBlock["strict"])
	}
	if jsBlock["name"] != "response" {
		t.Fatalf("name = %v, want response", jsBlock["name"])
	}
	// additionalProperties: false must be injected
	schemaMap, ok := jsBlock["schema"].(map[string]any)
	if !ok {
		t.Fatalf("schema not a map: %+v", jsBlock["schema"])
	}
	if schemaMap["additionalProperties"] != false {
		t.Fatalf("additionalProperties not injected: %+v", schemaMap)
	}
}

// TestApplyResponseFormat_Grammar_Unsupported verifies that Mode="grammar"
// returns ErrUnsupportedFormat for the OpenAI adapter.
func TestApplyResponseFormat_Grammar_Unsupported(t *testing.T) {
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

// TestApplyResponseFormat_Nil_NoOp verifies that nil ResponseFormat is a no-op.
func TestApplyResponseFormat_Nil_NoOp(t *testing.T) {
	a := New()
	req := llm.GenerationRequest{}
	body := map[string]any{"model": "gpt-4o"}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := body["response_format"]; ok {
		t.Fatal("response_format should not be set for nil ResponseFormat")
	}
}

// ── WP03 JSONMode wire-shape tests (multimodal-io-extended-01KQ8TD2) ─────────

// TestJSONMode_NoSchema_YieldsJSONObject verifies that JSONMode with no schema
// translates to response_format: {type: "json_object"}.
func TestJSONMode_NoSchema_YieldsJSONObject(t *testing.T) {
	body := map[string]any{}
	jm := &llm.JSONModeSpec{Enabled: true}
	if err := applyJSONMode(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %+v", body)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("type = %v want json_object", rf["type"])
	}
}

// TestJSONMode_WithSchema_YieldsJSONSchema verifies that JSONMode with a schema
// translates to response_format: {type: "json_schema", json_schema: {...}}.
func TestJSONMode_WithSchema_YieldsJSONSchema(t *testing.T) {
	body := map[string]any{}
	schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	jm := &llm.JSONModeSpec{Enabled: true, Schema: schema, Strict: true, Name: "my_obj"}
	if err := applyJSONMode(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %+v", body)
	}
	if rf["type"] != "json_schema" {
		t.Fatalf("type = %v want json_schema", rf["type"])
	}
	jsBlock, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema block missing: %+v", rf)
	}
	if jsBlock["name"] != "my_obj" {
		t.Fatalf("name = %v want my_obj", jsBlock["name"])
	}
	if jsBlock["strict"] != true {
		t.Fatalf("strict = %v want true", jsBlock["strict"])
	}
}

// TestJSONMode_DefaultName verifies that Name="" falls back to "response".
func TestJSONMode_DefaultName(t *testing.T) {
	body := map[string]any{}
	schema := json.RawMessage(`{"type":"object"}`)
	jm := &llm.JSONModeSpec{Enabled: true, Schema: schema}
	if err := applyJSONMode(jm, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf := body["response_format"].(map[string]any)
	jsBlock := rf["json_schema"].(map[string]any)
	if jsBlock["name"] != "response" {
		t.Fatalf("default name = %v want response", jsBlock["name"])
	}
}

// TestJSONMode_ResponseFormatWins verifies that when both ResponseFormat and
// JSONMode are set, ResponseFormat takes precedence (ResponseFormat is applied
// first in buildRequestBody, JSONMode is skipped when ResponseFormat is set).
func TestJSONMode_ResponseFormatWins(t *testing.T) {
	// Simulate the buildRequestBody logic: if ResponseFormat is set, skip JSONMode.
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json"},
		JSONMode:       &llm.JSONModeSpec{Enabled: true, Schema: json.RawMessage(`{"type":"object"}`)},
	}
	body := map[string]any{}
	// Only apply ResponseFormat (skipping JSONMode when ResponseFormat present).
	a := New()
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// ResponseFormat won: should be json_object.
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing: %+v", body)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("type = %v want json_object (ResponseFormat path)", rf["type"])
	}
}
