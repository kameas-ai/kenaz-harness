package azure

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestBuildRequestBody_ToolCallRoundTrip is AC-002
// (model-settings-reach-the-model-01PMZ101 WP03): a message carrying an
// assistant tool_use block followed by a tool_result block must marshal to
// an OpenAI-shape "tool_calls" array (with the id) on the assistant entry
// and a separate {"role":"tool", "tool_call_id": ...} entry — not silently
// dropped, which is what the old private buildContent/flattenText encoder
// did (it only ever read ContentBlock.Text, so a tool_use/tool_result-only
// message produced empty content and the tool round trip was invisible to
// the provider). Asserted on the marshalled wire bytes, not on a Go
// struct, per spec §8 rule 4.
//
// Mutation: reverting azure's buildRequestBody to the pre-WP03 private
// encoder must make this test fail.
func TestBuildRequestBody_ToolCallRoundTrip(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("ok")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "gpt-4o",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages: []llm.Message{
			llm.NewTextMessage(llm.RoleUser, "list files"),
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
				{Type: "tool_use", ToolUse: &llm.ToolUse{
					ID: "call_abc", Name: "list_dir", Input: json.RawMessage(`{"path":"."}`),
				}},
			}},
			{Role: "tool", Content: []llm.ContentBlock{
				{Type: "tool_result", ToolResult: &llm.ToolResult{
					ToolUseID: "call_abc", Content: json.RawMessage(`"a.txt\nb.txt"`),
				}},
			}},
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

	var wire map[string]any
	if err := json.Unmarshal(received, &wire); err != nil {
		t.Fatalf("parse wire body: %v (raw: %s)", err, received)
	}
	msgs, ok := wire["messages"].([]any)
	if !ok || len(msgs) != 3 {
		t.Fatalf("messages = %v, want 3 entries", wire["messages"])
	}
	asst, ok := msgs[1].(map[string]any)
	if !ok {
		t.Fatalf("msgs[1] is %T, want object", msgs[1])
	}
	if asst["role"] != "assistant" {
		t.Fatalf("msgs[1].role = %v, want assistant", asst["role"])
	}
	tcs, ok := asst["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("assistant tool_calls = %v, want one entry", asst["tool_calls"])
	}
	tc0, _ := tcs[0].(map[string]any)
	if tc0["id"] != "call_abc" {
		t.Errorf("tool_calls[0].id = %v, want call_abc", tc0["id"])
	}

	toolMsg, ok := msgs[2].(map[string]any)
	if !ok {
		t.Fatalf("msgs[2] is %T, want object", msgs[2])
	}
	if toolMsg["role"] != "tool" {
		t.Fatalf("msgs[2].role = %v, want tool", toolMsg["role"])
	}
	if toolMsg["tool_call_id"] != "call_abc" {
		t.Errorf("msgs[2].tool_call_id = %v, want call_abc", toolMsg["tool_call_id"])
	}
}

// TestBuildRequestBody_ImageRoundTrip is WP03's image-shape regression
// test: azure's private buildContent produced
// {"type":"image_url","image_url":{"url":"data:<mime>;base64,<data>"}} for
// an image block; the shared openaiwire encoder must produce the
// identical shape now that azure routes through it (spec §5.2 hazard 1 —
// "RESOLVED, no work needed" — this test is the confirmation).
func TestBuildRequestBody_ImageRoundTrip(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseChunks("ok")))
	}))
	defer srv.Close()

	a := New(WithHTTPClient(&http.Client{}))
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "gpt-4o",
		Endpoint: srv.URL + "/openai/deployments/dep/chat/completions",
	}
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{
				{Type: "text", Text: "what is this?"},
				{Type: "image", Source: &llm.MediaSource{
					Kind: "base64", MediaType: "image/png", Data: "QUJD",
				}},
			}},
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

	var wire map[string]any
	if err := json.Unmarshal(received, &wire); err != nil {
		t.Fatalf("parse wire body: %v (raw: %s)", err, received)
	}
	msgs, ok := wire["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("messages = %v, want 1 entry", wire["messages"])
	}
	m0, _ := msgs[0].(map[string]any)
	parts, ok := m0["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("content = %v, want 2-part array (text + image_url)", m0["content"])
	}
	imgPart, _ := parts[1].(map[string]any)
	if imgPart["type"] != "image_url" {
		t.Fatalf("parts[1].type = %v, want image_url", imgPart["type"])
	}
	imgURL, _ := imgPart["image_url"].(map[string]any)
	want := "data:image/png;base64,QUJD"
	if imgURL["url"] != want {
		t.Errorf("image_url.url = %v, want %q", imgURL["url"], want)
	}
}
