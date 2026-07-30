package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestKnobs_RequestBody_StopSequences verifies the typed
// GenerationRequest.StopSequences field maps onto Azure's OpenAI-compatible
// `stop` wire key (model-request-path-live-01PMDL01 WP05).
func TestKnobs_RequestBody_StopSequences(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("answer")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "gpt-4o",
		Endpoint: srv.URL + "/openai/deployments/prod-gpt-4o-eastus/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID:     "test",
		Messages:      []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		StopSequences: []string{"STOP", "END"},
	}

	stream, err := a.Stream(context.Background(), req, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Final(); err != nil {
		t.Fatalf("Final: %v", err)
	}

	got, ok := reqBody["stop"].([]any)
	if !ok {
		t.Fatalf("request body missing 'stop' (or wrong type): %#v", reqBody["stop"])
	}
	if len(got) != 2 || got[0] != "STOP" || got[1] != "END" {
		t.Errorf("stop = %#v, want [STOP END]", got)
	}
}

// TestKnobs_RequestBody_SeedAndParallelToolCalls verifies the WP05-widened
// Params key list (seed, parallel_tool_calls) reaches the Azure wire body —
// these were absent from buildRequestBody's key loop before
// model-request-path-live-01PMDL01 WP05.
func TestKnobs_RequestBody_SeedAndParallelToolCalls(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("answer")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "gpt-4o",
		Endpoint: srv.URL + "/openai/deployments/prod-gpt-4o-eastus/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "hi")},
		Params: map[string]any{
			"seed":                float64(12345),
			"parallel_tool_calls": true,
		},
	}

	stream, err := a.Stream(context.Background(), req, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	if _, err := stream.Final(); err != nil {
		t.Fatalf("Final: %v", err)
	}

	if got := reqBody["seed"]; got != float64(12345) {
		t.Errorf("seed = %v, want 12345", got)
	}
	if got := reqBody["parallel_tool_calls"]; got != true {
		t.Errorf("parallel_tool_calls = %v, want true", got)
	}
}
