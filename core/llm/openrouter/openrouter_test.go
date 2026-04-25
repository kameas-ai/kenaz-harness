package openrouter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

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
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Type: "text", Text: "hi"}}},
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
}

func TestAdapter_ListModels_EmptyCredential(t *testing.T) {
	a := New()
	_, err := a.ListModels(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty credential")
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
