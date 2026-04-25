package openai

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
			{Role: llm.RoleUser, Content: []llm.ContentPart{{Type: "text", Text: "hi"}}},
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
