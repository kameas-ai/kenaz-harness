package openrouter

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

// fakeServer wraps httptest.Server with helpers for crafting
// chat-completions responses (success SSE, JSON error envelopes).
type fakeServer struct {
	*httptest.Server
	mu           sync.Mutex
	lastBody     []byte
	lastHeaders  http.Header
	lastPath     string
	requestCount int
}

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

// writeSSEFrames emits a sequence of raw SSE lines verbatim (each
// element is written followed by a single "\n"). Tests use this to
// emit a mix of "data: ..." records, blank record terminators, and
// ":" comment frames.
func writeSSEFrames(w http.ResponseWriter, lines []string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for _, l := range lines {
		fmt.Fprint(w, l, "\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func newAdapter(fs *fakeServer) *Adapter {
	// Point endpoint at <fs>/api/v1/chat/completions so modelsURL()
	// derivation also resolves to <fs>/api/v1/models in ListModels tests.
	return New(
		WithEndpoint(fs.URL+"/api/v1/chat/completions"),
		WithHTTPClient(fs.Client()),
	)
}

func stdReq() (llm.GenerationRequest, llm.ProviderProfile) {
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
	prof := llm.ProviderProfile{
		ID:    "p-openrouter",
		Kind:  Kind,
		Model: "anthropic/claude-3.5-sonnet",
		Cred:  llm.CredentialReference{Kind: "env", Locator: "OPENROUTER_API_KEY"},
	}
	return req, prof
}

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

func TestAdapter_Stream_HappyPath(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSEFrames(w, []string{
			`data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}`,
			``,
			`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
			``,
			`data: {"choices":[{"delta":{"content":", world"},"finish_reason":null}]}`,
			``,
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			``,
			`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":12,"total_tokens":17}}`,
			``,
			`data: [DONE]`,
			``,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-or-test"))
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

	fs.mu.Lock()
	defer fs.mu.Unlock()
	if got := fs.lastHeaders.Get("Authorization"); got != "Bearer sk-or-test" {
		t.Fatalf("Authorization = %q", got)
	}
	if !strings.Contains(string(fs.lastBody), `"stream":true`) {
		t.Fatalf("body should set stream=true, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"model":"anthropic/claude-3.5-sonnet"`) {
		t.Fatalf("body should carry model, got %s", fs.lastBody)
	}
	if !strings.Contains(string(fs.lastBody), `"usage":{"include":true}`) {
		t.Fatalf("body should opt into usage reporting, got %s", fs.lastBody)
	}
}

func TestAdapter_Stream_SkipsPingComments(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSEFrames(w, []string{
			`: openrouter ping`,
			``,
			`data: {"choices":[{"delta":{"content":"foo"},"finish_reason":null}]}`,
			``,
			`: another comment`,
			``,
			`data: {"choices":[{"delta":{"content":"bar"},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	text, _, ferr := drain(t, stream)
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if text != "foobar" {
		t.Fatalf("text = %q, want %q", text, "foobar")
	}
}

func TestAdapter_Stream_AuthError(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"authentication_error"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-or-bad"))
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
	if !strings.Contains(ae.Message, "authentication_error") {
		t.Fatalf("message = %q", ae.Message)
	}
}

func TestAdapter_Stream_RateLimited(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, []byte("sk-or-test"))
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
		t.Fatal("429 must classify as transient")
	}
}

func TestAdapter_Stream_SetsRankingHeaders(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeSSEFrames(w, []string{
			`data: {"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}]}`,
			``,
			`data: [DONE]`,
			``,
		})
	})
	a := New(
		WithEndpoint(fs.URL+"/api/v1/chat/completions"),
		WithHTTPClient(fs.Client()),
		WithReferer("https://example.test/app"),
		WithAppTitle("UnitTestHarness"),
	)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _, ferr := drain(t, stream)
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if got := fs.lastHeaders.Get("HTTP-Referer"); got != "https://example.test/app" {
		t.Fatalf("HTTP-Referer = %q", got)
	}
	if got := fs.lastHeaders.Get("X-Title"); got != "UnitTestHarness" {
		t.Fatalf("X-Title = %q", got)
	}
}

func TestAdapter_ListModels_HappyPath(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("expected Authorization Bearer header, got %q", r.Header.Get("Authorization"))
		}
		if !strings.HasSuffix(r.URL.Path, "/api/v1/models") {
			t.Errorf("expected /api/v1/models path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"anthropic/claude-3.5-sonnet","name":"Claude 3.5 Sonnet","description":"Anthropic's flagship","context_length":200000},
			{"id":"openai/gpt-4o","name":"GPT-4o","description":"OpenAI omni","context_length":128000},
			{"id":"meta-llama/llama-3-70b","name":"","description":"Meta Llama 3 70B"}
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
	// Sorted alphabetically by display name (case-insensitive). With
	// the empty name, fallback is the model id "meta-llama/llama-3-70b".
	// Order: "Claude 3.5 Sonnet", "GPT-4o", "meta-llama/llama-3-70b".
	if models[0].DisplayName != "Claude 3.5 Sonnet" {
		t.Fatalf("first model = %+v", models[0])
	}
	if models[0].Description != "Anthropic's flagship" {
		t.Fatalf("first description = %q", models[0].Description)
	}
	if models[1].DisplayName != "GPT-4o" {
		t.Fatalf("second model = %+v", models[1])
	}
	// Empty-name model should fall back to its id.
	if models[2].DisplayName != "meta-llama/llama-3-70b" {
		t.Fatalf("third model display name = %q (want fallback to id)", models[2].DisplayName)
	}
	if models[2].ID != "meta-llama/llama-3-70b" {
		t.Fatalf("third model id = %q", models[2].ID)
	}
	// ContextWindow should be passed through from the API response.
	if models[0].ContextWindow != 200_000 {
		t.Errorf("expected ContextWindow=200000 for claude-3.5-sonnet, got %d", models[0].ContextWindow)
	}
	if models[1].ContextWindow != 128_000 {
		t.Errorf("expected ContextWindow=128000 for gpt-4o, got %d", models[1].ContextWindow)
	}
	// Model without context_length → 0.
	if models[2].ContextWindow != 0 {
		t.Errorf("expected ContextWindow=0 for llama (missing), got %d", models[2].ContextWindow)
	}
}

func TestAdapter_ListModels_EmptyCredentialAllowed(t *testing.T) {
	// OpenRouter's /api/v1/models endpoint is publicly accessible, so an
	// empty credential is valid: the request goes out without an
	// Authorization header and the server still returns the catalog.
	// This lets ListProviders refresh the cache before the user has
	// saved a key (and lets the AddProvider modal populate the model
	// picker on first launch).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("expected no Authorization header for empty cred call, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	a := New(WithEndpoint(srv.URL + "/chat/completions"))
	models, err := a.ListModels(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error for empty cred (public endpoint), got %v", err)
	}
	if len(models) != 0 {
		t.Errorf("expected empty model list, got %d", len(models))
	}
}

func TestAdapter_LookupModelInfo_CachePopulatedByListModels(t *testing.T) {
	// Verifies that ListModels populates the per-adapter cache so
	// LookupModelInfo can serve ListProviders without re-fetching. The
	// dynamic source is the upstream /models response — the cached
	// values must match the wire shape verbatim.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"deepseek/deepseek-chat-v3","name":"DeepSeek Chat V3","description":"d","context_length":131072},
			{"id":"anthropic/claude-3.5-sonnet","name":"Claude","description":"a","context_length":200000}
		]}`))
	}))
	defer srv.Close()
	a := New(WithEndpoint(srv.URL + "/chat/completions"))

	// Lookup before ListModels: miss.
	if _, ok := a.LookupModelInfo("deepseek/deepseek-chat-v3"); ok {
		t.Fatal("expected miss before ListModels populates cache")
	}

	if _, err := a.ListModels(context.Background(), []byte("sk-test")); err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	info, ok := a.LookupModelInfo("deepseek/deepseek-chat-v3")
	if !ok {
		t.Fatal("expected hit after ListModels populates cache")
	}
	if info.ContextWindow != 131072 {
		t.Errorf("ContextWindow = %d, want 131072 (wire value)", info.ContextWindow)
	}
	if info.DisplayName != "DeepSeek Chat V3" {
		t.Errorf("DisplayName = %q, want %q", info.DisplayName, "DeepSeek Chat V3")
	}

	// Unknown model still misses after cache populated.
	if _, ok := a.LookupModelInfo("bogus/model"); ok {
		t.Error("expected miss for unknown model id")
	}
}

func TestAdapter_RefreshModelsAsync_DedupesAndBacksOff(t *testing.T) {
	var hitCount int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hitCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"x","name":"X","context_length":1000}]}`))
	}))
	defer srv.Close()
	a := New(WithEndpoint(srv.URL + "/chat/completions"))

	a.RefreshModelsAsync(nil)
	a.RefreshModelsAsync(nil) // dedupe — in flight
	a.RefreshModelsAsync(nil) // dedupe — in flight

	// Wait for the in-flight to finish.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := a.LookupModelInfo("x"); ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := hitCount
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected exactly 1 upstream hit (dedupe), got %d", got)
	}
}

func TestAdapter_KindIsOpenRouter(t *testing.T) {
	if got := New().Kind(); got != "openrouter" {
		t.Fatalf("Kind = %q, want %q", got, "openrouter")
	}
}

func TestAdapter_EmptyCredentialRejected(t *testing.T) {
	a := New()
	req, prof := stdReq()
	_, err := a.Stream(context.Background(), req, prof, nil)
	if err == nil {
		t.Fatal("expected error for empty credential")
	}
	var ae *llm.ErrAuth
	if !errors.As(err, &ae) {
		t.Fatalf("expected *llm.ErrAuth, got %T %v", err, err)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	desc := New().Capabilities("anthropic/claude-3.5-sonnet")
	if !desc.Has(llm.CapStreaming) {
		t.Fatal("expected streaming")
	}
	if !desc.Has(llm.CapToolCalling) {
		t.Fatal("expected tool_calling")
	}
	if !desc.Has(llm.CapUsageReporting) {
		t.Fatal("expected usage_reporting")
	}
}

// ── multimodal-io-01KQ8TDF WP03: image serialization tests ───────────────

// TestBuildOpenRouterContent_ImageBlock verifies that an image ContentBlock
// is serialized to the OpenAI image_url content-array shape.
func TestBuildOpenRouterContent_ImageBlock(t *testing.T) {
	parts := []llm.ContentBlock{
		{Type: "text", Text: "describe this"},
		{
			Type: "image",
			Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "image/png",
				Data:      "aGVsbG8=",
			},
		},
	}
	result := buildOpenRouterContent(parts)
	arr, ok := result.([]map[string]any)
	if !ok {
		t.Fatalf("expected []map[string]any, got %T: %v", result, result)
	}
	if len(arr) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(arr))
	}
	if arr[0]["type"] != "text" {
		t.Errorf("part[0]: expected type=text, got %v", arr[0]["type"])
	}
	if arr[1]["type"] != "image_url" {
		t.Errorf("part[1]: expected type=image_url, got %v", arr[1]["type"])
	}
	iu, _ := arr[1]["image_url"].(map[string]any)
	if iu == nil {
		t.Fatal("part[1].image_url is nil")
	}
	want := "data:image/png;base64,aGVsbG8="
	if got := iu["url"]; got != want {
		t.Errorf("image_url.url = %q, want %q", got, want)
	}
}

// TestBuildOpenRouterContent_TextOnly verifies pure-text returns a string.
func TestBuildOpenRouterContent_TextOnly(t *testing.T) {
	parts := []llm.ContentBlock{
		{Type: "text", Text: "hello"},
	}
	result := buildOpenRouterContent(parts)
	if s, ok := result.(string); !ok || s != "hello" {
		t.Fatalf("expected string \"hello\", got %T %v", result, result)
	}
}

// TestBuildOpenRouterContent_DocumentDropped verifies that a document block
// in the array is dropped (gate-rejected pre-flight in production, but
// defensive drop here).
func TestBuildOpenRouterContent_DocumentDropped(t *testing.T) {
	parts := []llm.ContentBlock{
		{Type: "text", Text: "some text"},
		{
			Type: "document",
			Source: &llm.MediaSource{
				Kind:      "base64",
				MediaType: "application/pdf",
				Data:      "cGRm",
			},
		},
	}
	result := buildOpenRouterContent(parts)
	// No image block → flattenContent path: plain string.
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T: %v", result, result)
	}
	if s != "some text" {
		t.Errorf("expected \"some text\", got %q", s)
	}
}

// TestAdapter_Stream_WithImageBlock verifies that the adapter serializes an
// image block into the image_url content-array when the request is streamed.
func TestAdapter_Stream_WithImageBlock(t *testing.T) {
	srv := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSEFrames(w, []string{
			`data: {"id":"1","choices":[{"delta":{"content":"ok"}}]}`,
			`data: {"id":"1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		})
	})
	a := newAdapter(srv)
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					{Type: "text", Text: "what is this"},
					{
						Type: "image",
						Source: &llm.MediaSource{
							Kind:      "base64",
							MediaType: "image/png",
							Data:      "aGVsbG8=",
						},
					},
				},
			},
		},
	}
	prof := llm.ProviderProfile{
		ID:    "p",
		Kind:  Kind,
		Model: "openai/gpt-4o",
		Cred:  llm.CredentialReference{Kind: "env", Locator: "OPENROUTER_KEY"},
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_, _, serr := drain(t, stream)
	if serr != nil {
		t.Fatalf("drain: %v", serr)
	}
	// Verify the image_url payload made it into the request body via the
	// fakeServer's lastBody capture.
	srv.mu.Lock()
	body := string(srv.lastBody)
	srv.mu.Unlock()
	if !strings.Contains(body, "image_url") {
		t.Errorf("expected image_url in request body; got: %s", body)
	}
	if !strings.Contains(body, "data:image/png;base64,") {
		t.Errorf("expected data URL in request body; got: %s", body)
	}
}

// ── Structured-output tests (structured-output-and-grammar-01KX5R8A WP03c) ──

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
		t.Fatalf("response_format missing: %+v", body)
	}
	if rf["type"] != "json_object" {
		t.Fatalf("response_format.type = %v, want json_object", rf["type"])
	}
}

// TestApplyResponseFormat_JSONSchema verifies that Mode="json_schema"
// sets response_format.type = "json_schema" with strict: true.
func TestApplyResponseFormat_JSONSchema(t *testing.T) {
	a := New()
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema},
	}
	body := map[string]any{}
	if err := a.ApplyResponseFormat(&req, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_schema" {
		t.Fatalf("response_format = %+v, want type=json_schema", body)
	}
	js := rf["json_schema"].(map[string]any)
	if js["strict"] != true {
		t.Fatalf("strict = %v, want true", js["strict"])
	}
}

// TestApplyResponseFormat_Grammar_Unsupported verifies grammar mode returns
// ErrUnsupportedFormat for OpenRouter.
func TestApplyResponseFormat_Grammar_Unsupported(t *testing.T) {
	a := New()
	req := llm.GenerationRequest{
		ResponseFormat: &llm.ResponseFormat{Mode: "grammar"},
	}
	body := map[string]any{}
	err := a.ApplyResponseFormat(&req, body)
	if !llm.IsUnsupportedFormat(err) {
		t.Fatalf("expected ErrUnsupportedFormat, got %T: %v", err, err)
	}
}
