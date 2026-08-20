package custom

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
// and a separate {"role":"tool", "tool_call_id": ...} entry. The old
// private flattenContent encoder only ever read ContentBlock.Text, so a
// tool_use/tool_result-only message produced empty "" content and neither
// the tool call nor its id ever reached the wire — an OpenAI-compatible
// provider would 400 on the follow-up turn ("tool_call_id is not found").
// Asserted on the marshalled wire bytes, not on a Go struct (spec §8 rule
// 4).
//
// Mutation: reverting custom's buildRequestBody to the pre-WP03 private
// encoder must make this test fail.
func TestBuildRequestBody_ToolCallRoundTrip(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	a := &Adapter{
		httpc:     srv.Client(),
		templates: &Registry{templates: nil, byID: map[string]*Template{}},
	}
	prof := llm.ProviderProfile{
		ID:       "test",
		Kind:     Kind,
		Model:    "llama3.1",
		Endpoint: srv.URL + "/v1",
		Defaults: map[string]any{"auth_scheme": "none"},
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

	stream, err := a.Stream(context.Background(), req, prof, nil)
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
