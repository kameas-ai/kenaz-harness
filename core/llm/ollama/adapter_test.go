package ollama_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/ollama"
)

func TestAdapter_Kind(t *testing.T) {
	a := ollama.New()
	if a == nil {
		t.Fatal("New() returned nil with HARNESS_OLLAMA unset")
	}
	if got := a.Kind(); got != "ollama" {
		t.Fatalf("Kind() = %q, want %q", got, ollama.Kind)
	}
}

func TestNew_EnvFlagDisables(t *testing.T) {
	t.Setenv("HARNESS_OLLAMA", "0")
	if a := ollama.New(); a != nil {
		t.Fatal("New() with HARNESS_OLLAMA=0 should return nil")
	}
}

// TestStream_ToolRoundTrip drives a real (httptest) SSE round trip
// through openaiwire.BuildRequestBody and asserts the assistant's
// tool_use block and the following tool_result both marshal correctly
// on the wire — the same class of assertion WP03's AC-002 required for
// azure/custom, applied here because this adapter routes through the
// identical shared encoder (spec §5.2's DRY reasoning).
func TestStream_ToolRoundTrip(t *testing.T) {
	var capturedBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		frames := []string{
			`{"choices":[{"delta":{"content":"ok"},"finish_reason":null}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	a := ollama.New()
	prof := llm.ProviderProfile{ID: "ollama-test", Kind: ollama.Kind, Model: "llama3.1", Endpoint: srv.URL}
	req := llm.GenerationRequest{
		ProfileID: "ollama-test",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "call the tool"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "tool_use", ToolUse: &llm.ToolUse{ID: "call_1", Name: "search", Input: json.RawMessage(`{}`)}},
			}},
			{Role: llm.RoleTool, Content: []llm.ContentBlock{
				{Type: "tool_result", ToolResult: &llm.ToolResult{ToolUseID: "call_1", Content: json.RawMessage(`"result text"`)}},
			}},
		},
	}

	stream, err := a.Stream(context.Background(), req, prof, nil)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}
	if resp.Content[0].Text != "ok" {
		t.Fatalf("got text %q, want %q", resp.Content[0].Text, "ok")
	}

	// Assert on the wire bytes, not structs (spec §8 rule 4): the
	// assistant tool_use must marshal to a tool_calls array, and the
	// tool_result must marshal to tool_call_id — matching what WP02/
	// WP03 fixed for azure/custom-openai.
	msgs, ok := capturedBody["messages"].([]any)
	if !ok {
		t.Fatalf("captured body has no messages array: %+v", capturedBody)
	}
	foundToolCalls := false
	foundToolCallID := false
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if tc, ok := mm["tool_calls"].([]any); ok && len(tc) > 0 {
			foundToolCalls = true
		}
		if mm["role"] == "tool" {
			if id, ok := mm["tool_call_id"].(string); ok && id == "call_1" {
				foundToolCallID = true
			}
		}
	}
	if !foundToolCalls {
		t.Errorf("wire body missing assistant tool_calls array: %+v", capturedBody)
	}
	if !foundToolCallID {
		t.Errorf("wire body missing tool_call_id=call_1 on the tool message: %+v", capturedBody)
	}
}

func TestListModels_ParsesNativeTagsEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"llama3.1:8b"},{"name":"bakllava:latest"}]}`))
	}))
	t.Cleanup(srv.Close)

	a := ollama.New(ollama.WithHTTPClient(srv.Client()), ollama.WithProbeBaseURL(srv.URL))
	models, err := a.ListModels(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(models), models)
	}
	// sorted alphabetically
	if models[0].ID != "bakllava:latest" || models[1].ID != "llama3.1:8b" {
		t.Errorf("unexpected model set/order: %+v", models)
	}
}

// TestProviderCapabilities_ProbesLiveShowEndpoint exercises the R-4/R-5-
// adjacent probe path this adapter implements for WP14: a live
// /api/show call determines ToolCalling/Vision, overlaid onto the
// static DescribeRich baseline for every other field.
func TestProviderCapabilities_ProbesLiveShowEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"capabilities":["completion","tools","vision"]}`))
	}))
	t.Cleanup(srv.Close)

	a := ollama.New(ollama.WithHTTPClient(srv.Client()), ollama.WithProbeBaseURL(srv.URL))
	caps, err := a.ProviderCapabilities(context.Background(), "llava")
	if err != nil {
		t.Fatalf("ProviderCapabilities: %v", err)
	}
	if !caps.ToolCalling {
		t.Error("want ToolCalling=true from a live /api/show reporting \"tools\"")
	}
	if !caps.Vision {
		t.Error("want Vision=true from a live /api/show reporting \"vision\"")
	}
	if caps.Streaming != true {
		t.Errorf("want Streaming to come from the static catalog baseline (true), got %v", caps.Streaming)
	}
}

// TestProviderCapabilities_ProbeFailureReturnsError asserts AC-017(c)'s
// invariant from the adapter side: a probe failure must be a
// returned error, never a ProviderCapabilities value with everything
// zeroed — Registry only Puts into the cache on a nil error, so an
// error here is what keeps a probe failure from ever overlaying a
// false-everything record onto the static baseline.
func TestProviderCapabilities_ProbeFailureReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	a := ollama.New(ollama.WithHTTPClient(srv.Client()), ollama.WithProbeBaseURL(srv.URL))
	if _, err := a.ProviderCapabilities(context.Background(), "llama3.1"); err == nil {
		t.Fatal("want an error from a 500 /api/show response, got nil")
	}
}

// TestProviderCapabilities_PreV04DaemonReturnsError covers the daemon
// that answers 200 but omits "capabilities" entirely (pre-0.4 Ollama).
// This must ALSO be an error, not a false-everything overlay — the
// probe genuinely cannot determine anything in that case.
func TestProviderCapabilities_PreV04DaemonReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"license":"...","modelfile":"..."}`))
	}))
	t.Cleanup(srv.Close)

	a := ollama.New(ollama.WithHTTPClient(srv.Client()), ollama.WithProbeBaseURL(srv.URL))
	if _, err := a.ProviderCapabilities(context.Background(), "llama3.1"); err == nil {
		t.Fatal("want an error from a pre-0.4 /api/show response with no capabilities field, got nil")
	}
}
