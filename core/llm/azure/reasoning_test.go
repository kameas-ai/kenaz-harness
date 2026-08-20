package azure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestReasoning_RequestBody_High checks that a request with Reasoning.BudgetTokens
// causing mapReasoningEffort to return "high" posts the correct wire body.
func TestReasoning_RequestBody_High(t *testing.T) {
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
		Model:    "o1",
		Endpoint: srv.URL + "/openai/deployments/prod-o1-eastus/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "solve it")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 20000}, // high
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

	if got, ok := reqBody["reasoning_effort"]; !ok {
		t.Error("request body missing 'reasoning_effort'")
	} else if got != "high" {
		t.Errorf("reasoning_effort = %q; want 'high'", got)
	}
}

// TestReasoning_RequestBody_Medium checks medium effort (4000–14999 tokens).
func TestReasoning_RequestBody_Medium(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("answer")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "o1",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "solve it")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 8000},
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

	if got := reqBody["reasoning_effort"]; got != "medium" {
		t.Errorf("reasoning_effort = %q; want 'medium'", got)
	}
}

// TestReasoning_RequestBody_Low checks low effort (<4000 tokens).
func TestReasoning_RequestBody_Low(t *testing.T) {
	var reqBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&reqBody)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("answer")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "o1",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "quick?")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 2000},
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

	if got := reqBody["reasoning_effort"]; got != "low" {
		t.Errorf("reasoning_effort = %q; want 'low'", got)
	}
}

// TestReasoning_SSEParse_o1 replays the o1_reasoning.sse fixture and checks
// that the stream parses correctly (text content preserved, usage populated).
func TestReasoning_SSEParse_o1(t *testing.T) {
	srv := serveSSE(o1ReasoningFixture)
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "o1",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "why is 6*7=42?")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 0}, // default = high
	}

	stream, err := a.Stream(context.Background(), req, prof, []byte("key"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	resp, err := stream.Final()
	if err != nil {
		t.Fatalf("Final: %v", err)
	}

	want := "The answer is 42."
	if len(resp.Content) == 0 || resp.Content[0].Text != want {
		t.Errorf("Content = %v; want %q", resp.Content, want)
	}
	// usage.InputTokens = 8, OutputTokens = 6, from o1_reasoning.sse
	if resp.Usage.InputTokens != 8 {
		t.Errorf("InputTokens = %d; want 8", resp.Usage.InputTokens)
	}
}

// TestReasoning_Capability_Gate_o1 checks that the azure adapter reports
// CapReasoning=true for an o1 deployment via the openai catalog alias.
func TestReasoning_Capability_Gate_o1(t *testing.T) {
	a := New()
	desc := a.Capabilities("o1")
	if !desc.Has(llm.CapReasoning) {
		t.Errorf("Capabilities(o1): CapReasoning=false; want true")
	}
}

// mapReasoningEffort's own boundary-value coverage moved to
// core/llm/openaiwire (model-settings-reach-the-model-01PMZ101 WP08,
// spec D-14): TestMapReasoningEffort in
// core/llm/openaiwire/reasoning_effort_internal_test.go. The tests above
// in this file (TestReasoning_RequestBody_High/Medium/Low) still cover
// the integration — that azure's Stream() produces the mapped value on
// the wire — through openaiwire.BuildRequestBody.
