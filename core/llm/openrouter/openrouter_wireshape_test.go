package openrouter

// Wire-shape contract tests for the OpenRouter adapter.
//
// Naming convention: Test<Adapter>_<Scenario>_<FieldDirection>
//   Serialized  — request body assertion
//   Parsed      — streaming response assertion (events + Final)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/wirecheck"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func stdProfOR(model string) llm.ProviderProfile {
	return llm.ProviderProfile{
		ID:    "p-or",
		Kind:  Kind,
		Model: model,
		Cred:  llm.CredentialReference{Kind: "env", Locator: "OPENROUTER_API_KEY"},
	}
}

func minReqOR() llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
}

func serialiseOR(t *testing.T, req llm.GenerationRequest, prof llm.ProviderProfile) []byte {
	t.Helper()
	body, err := buildRequestBody(req, prof)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	return body
}

func writeToolCallSSEOR(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	frames := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_or_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"bar.txt\"}"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`[DONE]`,
	}
	for _, f := range frames {
		fmt.Fprintf(w, "data: %s\n\n", f)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// ── ModelOverride ─────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_ModelOverride_ModelSerialized verifies model appears on wire.
func TestOpenRouterAdapter_ModelOverride_ModelSerialized(t *testing.T) {
	body := serialiseOR(t, minReqOR(), stdProfOR("anthropic/claude-3-5-sonnet"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantString: "anthropic/claude-3-5-sonnet"},
	})
}

// ── System ────────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_SystemPrompt_SystemSerialized verifies system prompt
// is emitted as a role=system message.
func TestOpenRouterAdapter_SystemPrompt_SystemSerialized(t *testing.T) {
	req := minReqOR()
	req.System = "You are a helpful assistant."
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages/0/role", WantString: "system"},
		{Pointer: "/messages/0/content", WantString: "You are a helpful assistant."},
	})
}

// ── Messages / History ────────────────────────────────────────────────────────

// TestOpenRouterAdapter_History_MessagesSerialized verifies multi-turn history
// is serialised correctly including a system message.
func TestOpenRouterAdapter_History_MessagesSerialized(t *testing.T) {
	req := llm.GenerationRequest{
		System: "Be helpful.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "First"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "Reply"}}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "Second"}}},
		},
	}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages", WantPresent: true, WantArrayLen: 4, WantArrayLenSet: true},
		{Pointer: "/messages/0/role", WantString: "system"},
		{Pointer: "/messages/1/role", WantString: "user"},
		{Pointer: "/messages/2/role", WantString: "assistant"},
		{Pointer: "/messages/3/role", WantString: "user"},
	})
}

// ── Tools ─────────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_Tools_ToolsSerialized verifies tools are emitted in
// the OpenAI-compatible {type:"function", function:{...}} envelope.
// This catches the silent-drop bug shape from commit 4185933.
func TestOpenRouterAdapter_Tools_ToolsSerialized(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	req := minReqOR()
	req.Tools = []llm.ToolSpec{
		{Name: "read_file", Description: "Reads a file", InputSchema: schema},
	}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/tools", WantPresent: true, WantArrayLen: 1, WantArrayLenSet: true},
		{Pointer: "/tools/0/type", WantString: "function"},
		{Pointer: "/tools/0/function/name", WantString: "read_file"},
		{Pointer: "/tools/0/function/description", WantString: "Reads a file"},
		{Pointer: "/tools/0/function/parameters", WantPresent: true},
		{Pointer: "/tools/0/function/parameters/type", WantString: "object"},
	})
}

// ── Attachments ───────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_Attachments_AttachmentsSerialized verifies image
// blocks are serialised as image_url parts.
func TestOpenRouterAdapter_Attachments_AttachmentsSerialized(t *testing.T) {
	req := llm.GenerationRequest{
		Messages: []llm.Message{
			{
				Role: llm.RoleUser,
				Content: []llm.ContentBlock{
					{Type: "text", Text: "Describe this image."},
					{
						Type: "image",
						Source: &llm.MediaSource{
							Kind:      "base64",
							MediaType: "image/png",
							Data:      "iVBORw0KGgo=",
						},
					},
				},
			},
		},
	}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages/0/content", WantPresent: true, WantArrayLen: 2, WantArrayLenSet: true},
		{Pointer: "/messages/0/content/0/type", WantString: "text"},
		{Pointer: "/messages/0/content/1/type", WantString: "image_url"},
	})
}

// ── JSONMode ──────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_JSONMode_JSONModeSerialized verifies JSONMode does
// not corrupt the wire body.
func TestOpenRouterAdapter_JSONMode_JSONModeSerialized(t *testing.T) {
	req := minReqOR()
	req.JSONMode = &llm.JSONModeSpec{Enabled: true}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantString: "openai/gpt-4o"},
		{Pointer: "/messages", WantPresent: true},
		{Pointer: "/stream", WantBool: wirecheck.BoolPtr(true)},
	})
}

// ── Caching ───────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_Caching_CachingSerialized verifies Caching does
// not corrupt the wire body.
func TestOpenRouterAdapter_Caching_CachingSerialized(t *testing.T) {
	req := minReqOR()
	req.Caching = &llm.CachingSpec{Enabled: true}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantPresent: true},
		{Pointer: "/messages", WantPresent: true},
	})
}

// ── Reasoning ─────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_Reasoning_ReasoningSerialized verifies Reasoning does
// not corrupt the wire body.
func TestOpenRouterAdapter_Reasoning_ReasoningSerialized(t *testing.T) {
	req := minReqOR()
	req.Reasoning = &llm.ReasoningSpec{Enabled: true}
	body := serialiseOR(t, req, stdProfOR("anthropic/claude-sonnet-4-5"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantPresent: true},
		{Pointer: "/messages", WantPresent: true},
	})
}

// ── ResponseFormat ────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_ResponseFormat_ResponseFormatSerialized verifies
// ResponseFormat=json sets response_format.type="json_object".
func TestOpenRouterAdapter_ResponseFormat_ResponseFormatSerialized(t *testing.T) {
	req := minReqOR()
	req.ResponseFormat = &llm.ResponseFormat{Mode: "json"}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/response_format/type", WantString: "json_object"},
	})
}

// ── Params ────────────────────────────────────────────────────────────────────

// TestOpenRouterAdapter_Params_ParamsSerialized verifies temperature and
// max_tokens from Params appear in the wire body.
func TestOpenRouterAdapter_Params_ParamsSerialized(t *testing.T) {
	req := minReqOR()
	req.Params = map[string]any{
		"temperature": 0.3,
		"max_tokens":  128,
	}
	body := serialiseOR(t, req, stdProfOR("openai/gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/temperature", WantNumber: wirecheck.Float64Ptr(0.3)},
		{Pointer: "/max_tokens", WantNumber: wirecheck.Float64Ptr(128)},
	})
}

// ── Response / StreamEvent parsing ───────────────────────────────────────────

// TestOpenRouterAdapter_ChatDefault_ResponseParsed verifies the happy-path SSE
// stream produces correct events and a Final() Response.
func TestOpenRouterAdapter_ChatDefault_ResponseParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hi there"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
			if flusher != nil {
				flusher.Flush()
			}
		}
	})
	a := New(WithEndpoint(fs.URL), WithHTTPClient(fs.Client()))
	req := minReqOR()
	stream, err := a.Stream(context.Background(), req, stdProfOR("openai/gpt-4o"), []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamText, Index: -1, WantText: "Hi there"},
		{Kind: llm.StreamFinish, Index: -1, WantFinish: "stop"},
	})
	if resp.FinishReason != "stop" {
		t.Errorf("Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}

// TestOpenRouterAdapter_StreamedToolCalls_ToolCallsParsed verifies that
// streamed tool_call deltas are accumulated into Response.ToolCalls.
//
// Unlike the anthropic/openai adapters, the openrouter adapter does NOT emit
// StreamTool events during the stream — it accumulates tool_calls and surfaces
// them only in Final(). The important contract is that tool_calls are NOT
// silently dropped from the wire (the regression shape from commit 2c710ae).
func TestOpenRouterAdapter_StreamedToolCalls_ToolCallsParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeToolCallSSEOR(w)
	})
	a := New(WithEndpoint(fs.URL), WithHTTPClient(fs.Client()))
	req := minReqOR()
	req.Tools = []llm.ToolSpec{
		{Name: "read_file", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	stream, err := a.Stream(context.Background(), req, stdProfOR("openai/gpt-4o"), []byte("sk-or-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// Drain all events (may not include StreamTool; the adapter accumulates
	// tool_calls and only surfaces them in Final()).
	for range stream.Events() {
	}
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	// The key contract: Response.ToolCalls must be populated.
	if len(resp.ToolCalls) == 0 {
		t.Fatal("Response.ToolCalls is empty — streamed tool_calls were dropped (2c710ae regression)")
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "read_file")
	}
	if resp.ToolCalls[0].ID != "call_or_1" {
		t.Errorf("ToolCalls[0].ID = %q, want %q", resp.ToolCalls[0].ID, "call_or_1")
	}
}

// TestOpenRouterAdapter_ToolCall_ToolCallsParsed is an alias for the streamed
// tool call test.
func TestOpenRouterAdapter_ToolCall_ToolCallsParsed(t *testing.T) {
	TestOpenRouterAdapter_StreamedToolCalls_ToolCallsParsed(t)
}
