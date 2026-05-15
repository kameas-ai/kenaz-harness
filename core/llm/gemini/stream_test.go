package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// buildSSEFrame wraps a geminiResponse as an SSE data line.
func buildSSEFrame(gr geminiResponse) []byte {
	b, _ := json.Marshal(gr)
	return []byte("data: " + string(b) + "\n")
}

// TestGeminiStream_SimpleText verifies a simple text stream produces
// the correct events and Final response.
func TestGeminiStream_SimpleText(t *testing.T) {
	t.Parallel()
	frames := []geminiResponse{
		{
			Candidates: []geminiCandidate{{
				Content: &geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: "Hello"}},
				},
				Index: 0,
			}},
		},
		{
			Candidates: []geminiCandidate{{
				Content: &geminiContent{
					Role:  "model",
					Parts: []geminiPart{{Text: " world"}},
				},
				FinishReason: "STOP",
				Index:        0,
			}},
			UsageMetadata: &geminiUsage{
				PromptTokenCount:     5,
				CandidatesTokenCount: 2,
				TotalTokenCount:      7,
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = w.Write(buildSSEFrame(f))
		}
	}))
	defer srv.Close()

	a := New(WithHTTPClient(srv.Client()))
	prof := llm.ProviderProfile{
		Kind:  Kind,
		Model: "gemini-2.0-flash",
		Cred:  llm.CredentialReference{Kind: "keychain", Locator: "test"},
	}
	// Override AI Studio URL by using a custom HTTP client + an endpoint
	// that routes to our test server. We patch by overriding the profile endpoint.
	prof.Endpoint = srv.URL // not used by our adapter directly, but we need to
	// Bypass auth by using a custom round-tripper that skips real AI Studio auth.
	// For unit tests we inject a fake cred + intercept at the HTTP layer.
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	ctx := context.Background()
	stream, err := a.Stream(ctx, llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
	}, prof, []byte("fake-api-key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var texts []string
	for ev := range stream.Events() {
		if ev.Kind == llm.StreamText {
			texts = append(texts, ev.Text)
		}
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	got := strings.Join(texts, "")
	if got != "Hello world" {
		t.Errorf("got text %q, want %q", got, "Hello world")
	}
	if resp.Usage.InputTokens != 5 || resp.Usage.OutputTokens != 2 {
		t.Errorf("unexpected usage: %+v", resp.Usage)
	}
}

// TestGeminiStream_Cancel verifies that Cancel terminates within 1 second.
func TestGeminiStream_Cancel(t *testing.T) {
	t.Parallel()
	// Slow handler — writes nothing for a long time.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		// Block until the request context is cancelled.
		<-r.Context().Done()
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	prof := llm.ProviderProfile{
		Kind:  Kind,
		Model: "gemini-2.0-flash",
		Cred:  llm.CredentialReference{Kind: "keychain", Locator: "test"},
	}
	ctx := context.Background()
	stream, err := a.Stream(ctx, llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
	}, prof, []byte("fake-api-key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Drain events in background.
	go func() {
		for range stream.Events() {
		}
	}()

	start := time.Now()
	_ = stream.Cancel()
	_, _ = stream.Final()
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Errorf("Cancel took %v, want < 1s", elapsed)
	}
}

// TestGeminiStream_ToolCall verifies tool-call frames emit StreamTool events
// with synthesised positional IDs.
func TestGeminiStream_ToolCall(t *testing.T) {
	t.Parallel()
	frames := []geminiResponse{{
		Candidates: []geminiCandidate{{
			Content: &geminiContent{
				Role: "model",
				Parts: []geminiPart{{
					FunctionCall: &geminiFunctionCall{
						Name: "get_weather",
						Args: json.RawMessage(`{"location":"Paris"}`),
					},
				}},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: &geminiUsage{PromptTokenCount: 10, CandidatesTokenCount: 5},
	}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = w.Write(buildSSEFrame(f))
		}
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	prof := llm.ProviderProfile{
		Kind:  Kind,
		Model: "gemini-2.0-flash",
		Cred:  llm.CredentialReference{Kind: "keychain", Locator: "test"},
	}
	ctx := context.Background()
	stream, err := a.Stream(ctx, llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "what is the weather in Paris?")},
	}, prof, []byte("fake-api-key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var toolEvents []llm.StreamEvent
	for ev := range stream.Events() {
		if ev.Kind == llm.StreamTool {
			toolEvents = append(toolEvents, ev)
		}
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if len(toolEvents) != 1 {
		t.Fatalf("want 1 tool event, got %d", len(toolEvents))
	}
	if toolEvents[0].Tool == nil || toolEvents[0].Tool.ID != "call_0" {
		t.Errorf("unexpected tool event: %+v", toolEvents[0])
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "get_weather" {
		t.Errorf("unexpected tool calls: %+v", resp.ToolCalls)
	}
}

// TestGeminiStream_UsageMetadata verifies CachedInputRead and ReasoningTokens
// are populated from usageMetadata.
func TestGeminiStream_UsageMetadata(t *testing.T) {
	t.Parallel()
	frames := []geminiResponse{{
		Candidates: []geminiCandidate{{
			Content: &geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: "answer"}},
			},
			FinishReason: "STOP",
		}},
		UsageMetadata: &geminiUsage{
			PromptTokenCount:        100,
			CandidatesTokenCount:    50,
			CachedContentTokenCount: 30,
			ThoughtsTokenCount:      20,
		},
	}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = w.Write(buildSSEFrame(f))
		}
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}

	prof := llm.ProviderProfile{Kind: Kind, Model: "gemini-2.5-pro", Cred: llm.CredentialReference{Kind: "keychain", Locator: "test"}}
	stream, err := a.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
	}, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if resp.Usage.CachedInputRead != 30 {
		t.Errorf("CachedInputRead=%d, want 30", resp.Usage.CachedInputRead)
	}
	if resp.Usage.ReasoningTokens != 20 {
		t.Errorf("ReasoningTokens=%d, want 20", resp.Usage.ReasoningTokens)
	}
}

// roundTripperFunc is a one-off http.RoundTripper backed by a function.
type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}

// TestAdapterKind verifies Kind() returns "gemini".
func TestAdapterKind(t *testing.T) {
	t.Parallel()
	a := New()
	if a.Kind() != "gemini" {
		t.Errorf("want kind=gemini, got %q", a.Kind())
	}
}

// TestAdapterCapabilities verifies catalog-backed capabilities for gemini-2.5-pro.
func TestAdapterCapabilities(t *testing.T) {
	t.Parallel()
	a := New()
	d := a.Capabilities("gemini-2.5-pro-preview-04-09")
	if !d.Has(llm.CapReasoning) {
		t.Errorf("gemini-2.5-pro: expected CapReasoning=true")
	}
	if !d.Has(llm.CapVision) {
		t.Errorf("gemini-2.5-pro: expected CapVision=true")
	}
	if d.Has(llm.CapGrammar) {
		t.Errorf("gemini-2.5-pro: expected CapGrammar=false")
	}
}

// TestGeminiStream_FinishReason verifies finish_reason is lowercased.
func TestGeminiStream_FinishReason(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		gr := geminiResponse{
			Candidates: []geminiCandidate{{
				Content:      &geminiContent{Parts: []geminiPart{{Text: "hi"}}},
				FinishReason: "MAX_TOKENS",
			}},
			UsageMetadata: &geminiUsage{PromptTokenCount: 1, CandidatesTokenCount: 1},
		}
		_, _ = w.Write(buildSSEFrame(gr))
	}))
	defer srv.Close()

	a := New()
	a.httpc = &http.Client{
		Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
			req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
			req.URL.Scheme = "http"
			return srv.Client().Do(req)
		}},
	}
	prof := llm.ProviderProfile{Kind: Kind, Model: "gemini-2.0-flash", Cred: llm.CredentialReference{Kind: "keychain", Locator: "t"}}
	stream, err := a.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "t", Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
	}, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if resp.FinishReason != "max_tokens" {
		t.Errorf("want finish_reason=max_tokens, got %q", resp.FinishReason)
	}
}

// TestAdapterEmptyCred verifies that an empty credential returns ErrAuth for AI Studio.
func TestAdapterEmptyCred(t *testing.T) {
	t.Parallel()
	a := New()
	prof := llm.ProviderProfile{Kind: Kind, Model: "gemini-2.0-flash", Cred: llm.CredentialReference{Kind: "keychain", Locator: "t"}}
	_, err := a.Stream(context.Background(), llm.GenerationRequest{
		ProfileID: "t", Messages: []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
	}, prof, nil)
	if err == nil {
		t.Fatal("expected ErrAuth for empty cred")
	}
	var authErr *llm.ErrAuth
	if !errorsAs(err, &authErr) {
		t.Errorf("want *llm.ErrAuth, got %T: %v", err, err)
	}
}

func errorsAs(err error, target any) bool {
	// Use the errors package via the llm package.
	switch t := target.(type) {
	case **llm.ErrAuth:
		if e, ok := err.(*llm.ErrAuth); ok {
			*t = e
			return true
		}
	}
	return false
}

// Ensure fmt is used (needed by buildSSEFrame helper).
var _ = fmt.Sprintf
