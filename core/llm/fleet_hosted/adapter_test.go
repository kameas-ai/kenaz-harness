package fleet_hosted

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func TestKind(t *testing.T) {
	a := New("https://fleet.example.com", func() (string, error) { return "tok", nil }, func() bool { return true })
	if a.Kind() != Kind {
		t.Errorf("Kind() = %q, want %q", a.Kind(), Kind)
	}
	if Kind != "fleet-hosted" {
		t.Errorf("Kind constant = %q, want 'fleet-hosted'", Kind)
	}
}

func TestAdapter_Capabilities(t *testing.T) {
	a := New("https://fleet.example.com", func() (string, error) { return "tok", nil }, func() bool { return true })
	desc := a.Capabilities("test-model")
	if desc.Provider != Kind {
		t.Errorf("Capabilities.Provider = %q, want %q", desc.Provider, Kind)
	}
	if !desc.Supported[llm.CapStreaming] {
		t.Error("Capabilities should support streaming")
	}
}

func TestAdapter_Enabled(t *testing.T) {
	enabled := true
	a := New("https://fleet.example.com", func() (string, error) { return "tok", nil }, func() bool { return enabled })
	if !a.Enabled() {
		t.Error("Enabled() = false, want true")
	}
	enabled = false
	if a.Enabled() {
		t.Error("Enabled() = true, want false")
	}
}

func TestAdapter_Enabled_NilFunc(t *testing.T) {
	a := &Adapter{enabledFn: nil}
	if a.Enabled() {
		t.Error("nil enabledFn: Enabled() = true, want false")
	}
}

func TestAdapter_Stream_WhenDisabled(t *testing.T) {
	a := New("https://fleet.example.com", func() (string, error) { return "tok", nil }, func() bool { return false })
	req := llm.GenerationRequest{Messages: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}}}}
	_, err := a.Stream(context.Background(), req, llm.ProviderProfile{}, nil)
	if err == nil {
		t.Fatal("Stream with disabled capability should return error")
	}
}

func TestAdapter_Stream_NoBearerProvider(t *testing.T) {
	a := New("https://fleet.example.com", nil, func() bool { return true })
	req := llm.GenerationRequest{Messages: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hello"}}}}}
	_, err := a.Stream(context.Background(), req, llm.ProviderProfile{}, nil)
	if err == nil {
		t.Fatal("Stream with nil bearer should return error")
	}
}

func TestAdapter_Stream_BearerError(t *testing.T) {
	a := New("https://fleet.example.com", func() (string, error) { return "", fmt.Errorf("no token") }, func() bool { return true })
	req := llm.GenerationRequest{Messages: []llm.Message{{Role: "user", Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}}}
	_, err := a.Stream(context.Background(), req, llm.ProviderProfile{}, nil)
	if err == nil {
		t.Fatal("bearer error should propagate")
	}
}

// TestAdapter_Stream_RoundTrip exercises a successful SSE round-trip against
// a fake fleet server.
func TestAdapter_Stream_RoundTrip(t *testing.T) {
	const wantText = "Hello from fleet"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llm/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		// Verify Bearer token forwarding.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Write SSE frames.
		for _, word := range strings.Fields(wantText) {
			frame := map[string]any{
				"choices": []map[string]any{
					{"delta": map[string]any{"content": word + " "}, "finish_reason": nil},
				},
			}
			data, _ := json.Marshal(frame)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
		// Write terminal finish frame.
		finishFrame := map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]any{}, "finish_reason": "stop"},
			},
		}
		finishData, _ := json.Marshal(finishFrame)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", finishData)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	a := New(srv.URL, func() (string, error) { return "test-token", nil }, func() bool { return true },
		WithHTTPClient(srv.Client()))

	req := llm.GenerationRequest{
		Messages: []llm.Message{{
			Role:    "user",
			Content: []llm.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}
	stream, err := a.Stream(context.Background(), req, llm.ProviderProfile{Model: "fleet-default"}, nil)
	if err != nil {
		t.Fatalf("Stream() = %v", err)
	}

	var got strings.Builder
	for ev := range stream.Events() {
		if ev.Kind == llm.StreamText {
			got.WriteString(ev.Text)
		}
	}

	resp, finalErr := stream.Final()
	if finalErr != nil {
		t.Fatalf("Final() = %v", finalErr)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want 'stop'", resp.FinishReason)
	}
	// Text is accumulated in the response content.
	if len(resp.Content) == 0 {
		t.Error("Final response has no content blocks")
	}
}

// TestAdapter_Stream_404 verifies 404 is classified correctly.
func TestAdapter_Stream_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a := New(srv.URL, func() (string, error) { return "tok", nil }, func() bool { return true },
		WithHTTPClient(srv.Client()))

	_, err := a.Stream(context.Background(), llm.GenerationRequest{}, llm.ProviderProfile{}, nil)
	if err == nil {
		t.Fatal("404 should return error")
	}
}

