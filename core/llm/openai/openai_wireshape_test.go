package openai

// Wire-shape contract tests for the OpenAI adapter.
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

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func stdProfOAI(model string) llm.ProviderProfile {
	return llm.ProviderProfile{
		ID:    "p-oai",
		Kind:  Kind,
		Model: model,
		Cred:  llm.CredentialReference{Kind: "env", Locator: "OPENAI_API_KEY"},
	}
}

func minReqOAI() llm.GenerationRequest {
	return llm.GenerationRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}},
		},
	}
}

func serialiseOAI(t *testing.T, req llm.GenerationRequest, prof llm.ProviderProfile) []byte {
	t.Helper()
	body, err := buildRequestBody(req, prof)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	return body
}

func writeToolCallSSEOAI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	frames := []string{
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_oai_1","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]},"finish_reason":null}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"foo.txt\"}"}}]},"finish_reason":null}]}`,
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

// TestOpenAIAdapter_ModelOverride_ModelSerialized verifies model appears in wire body.
func TestOpenAIAdapter_ModelOverride_ModelSerialized(t *testing.T) {
	body := serialiseOAI(t, minReqOAI(), stdProfOAI("gpt-4o-mini"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantString: "gpt-4o-mini"},
	})
}

// ── System ────────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_SystemPrompt_SystemSerialized verifies the system prompt
// is emitted as a role=system message in the messages array.
func TestOpenAIAdapter_SystemPrompt_SystemSerialized(t *testing.T) {
	req := minReqOAI()
	req.System = "You are a helpful assistant."
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages/0/role", WantString: "system"},
		{Pointer: "/messages/0/content", WantString: "You are a helpful assistant."},
	})
}

// ── Messages / History ────────────────────────────────────────────────────────

// TestOpenAIAdapter_History_MessagesSerialized verifies multi-turn history
// is serialised in order, including the system message when present.
func TestOpenAIAdapter_History_MessagesSerialized(t *testing.T) {
	req := llm.GenerationRequest{
		System: "Be helpful.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "First"}}},
			{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: "text", Text: "Reply"}}},
			{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "Second"}}},
		},
	}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	// System + 3 user/assistant = 4 messages total
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages", WantPresent: true, WantArrayLen: 4, WantArrayLenSet: true},
		{Pointer: "/messages/0/role", WantString: "system"},
		{Pointer: "/messages/1/role", WantString: "user"},
		{Pointer: "/messages/2/role", WantString: "assistant"},
		{Pointer: "/messages/3/role", WantString: "user"},
	})
}

// ── Tools ─────────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_Tools_ToolsSerialized verifies that tools are emitted
// in the OpenAI {type:"function", function:{name,description,parameters}} envelope.
// This catches the silent-drop bug shape from commit 4185933.
func TestOpenAIAdapter_Tools_ToolsSerialized(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)
	req := minReqOAI()
	req.Tools = []llm.ToolSpec{
		{Name: "read_file", Description: "Reads a file", InputSchema: schema},
	}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
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

// TestOpenAIAdapter_Attachments_AttachmentsSerialized verifies that image
// content blocks are serialised as image_url parts in the messages array.
func TestOpenAIAdapter_Attachments_AttachmentsSerialized(t *testing.T) {
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
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages/0/content", WantPresent: true, WantArrayLen: 2, WantArrayLenSet: true},
		{Pointer: "/messages/0/content/0/type", WantString: "text"},
		{Pointer: "/messages/0/content/1/type", WantString: "image_url"},
		{Pointer: "/messages/0/content/1/image_url/url", WantPresent: true},
	})
}

// ── JSONMode ──────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_JSONMode_JSONModeSerialized verifies the request body
// is valid when JSONMode is set (the field is handled at the registry layer).
func TestOpenAIAdapter_JSONMode_JSONModeSerialized(t *testing.T) {
	req := minReqOAI()
	req.JSONMode = &llm.JSONModeSpec{Enabled: true}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantString: "gpt-4o"},
		{Pointer: "/messages", WantPresent: true},
		{Pointer: "/stream", WantBool: wirecheck.BoolPtr(true)},
	})
}

// ── Reasoning ─────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_Reasoning_ReasoningSerialized verifies Reasoning does
// not corrupt the wire body.
func TestOpenAIAdapter_Reasoning_ReasoningSerialized(t *testing.T) {
	req := minReqOAI()
	req.Reasoning = &llm.ReasoningSpec{Enabled: true}
	body := serialiseOAI(t, req, stdProfOAI("o1-preview"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/model", WantString: "o1-preview"},
		{Pointer: "/messages", WantPresent: true},
	})
}

// ── ResponseFormat ────────────────────────────────────────────────────────────

// TestOpenAIAdapter_ResponseFormat_ResponseFormatSerialized verifies that
// ResponseFormat=json sets response_format.type="json_object" on the wire.
func TestOpenAIAdapter_ResponseFormat_ResponseFormatSerialized(t *testing.T) {
	req := minReqOAI()
	req.ResponseFormat = &llm.ResponseFormat{Mode: "json"}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/response_format/type", WantString: "json_object"},
	})
}

// ── Params ────────────────────────────────────────────────────────────────────

// TestOpenAIAdapter_Params_ParamsSerialized verifies temperature and
// max_tokens from Params appear in the wire body.
func TestOpenAIAdapter_Params_ParamsSerialized(t *testing.T) {
	req := minReqOAI()
	req.Params = map[string]any{
		"temperature": 0.5,
		"max_tokens":  256,
	}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/temperature", WantNumber: wirecheck.Float64Ptr(0.5)},
		{Pointer: "/max_tokens", WantNumber: wirecheck.Float64Ptr(256)},
	})
}

// TestOpenAIAdapter_StopSequences_StopSequencesSerialized verifies that the
// typed GenerationRequest.StopSequences field maps onto OpenAI's stop wire
// key (model-request-path-live-01PMDL01 WP05).
func TestOpenAIAdapter_StopSequences_StopSequencesSerialized(t *testing.T) {
	req := minReqOAI()
	req.StopSequences = []string{"STOP", "END"}
	body := serialiseOAI(t, req, stdProfOAI("gpt-4o"))
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/stop", WantPresent: true},
		{Pointer: "/stop/0", WantString: "STOP"},
		{Pointer: "/stop/1", WantString: "END"},
	})
}

// ── Response / StreamEvent parsing ───────────────────────────────────────────

// TestOpenAIAdapter_ChatDefault_ResponseParsed verifies the happy-path SSE
// stream produces the expected events and a correct Final() Response.
func TestOpenAIAdapter_ChatDefault_ResponseParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, happyPathFrames())
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamText, Index: -1, WantText: "Hello"},
		{Kind: llm.StreamFinish, Index: -1, WantFinish: "stop"},
	})

	if resp.FinishReason != "stop" {
		t.Errorf("Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 {
		t.Log("Usage tokens are zero — usage stream frame may not have fired (OK for -short tests)")
	}
}

// TestOpenAIAdapter_StreamedToolCalls_ToolCallsParsed verifies that streamed
// tool_calls deltas are assembled into StreamTool events and Response.ToolCalls.
// This catches the bug shape from commit 2c710ae.
func TestOpenAIAdapter_StreamedToolCalls_ToolCallsParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeToolCallSSEOAI(w)
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	req.Tools = []llm.ToolSpec{
		{Name: "read_file", Description: "Read", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamTool, Index: -1, WantToolName: "read_file", WantToolID: "call_oai_1"},
	})

	if len(resp.ToolCalls) == 0 {
		t.Fatal("Response.ToolCalls is empty — streamed tool_calls were dropped (2c710ae regression)")
	}
	if resp.ToolCalls[0].Name != "read_file" {
		t.Errorf("ToolCalls[0].Name = %q, want %q", resp.ToolCalls[0].Name, "read_file")
	}
}

// TestOpenAIAdapter_ToolCall_ToolCallsParsed is an alias for the streamed tool
// call test (OpenAI always streams tool calls).
func TestOpenAIAdapter_ToolCall_ToolCallsParsed(t *testing.T) {
	TestOpenAIAdapter_StreamedToolCalls_ToolCallsParsed(t)
}

// TestOpenAIAdapter_StopSequences_FinishParsed verifies finish reason
// is correctly parsed from the stream.
func TestOpenAIAdapter_StopSequences_FinishParsed(t *testing.T) {
	fs := newFakeServer(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		writeSSE(w, []string{
			`{"choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":null}]}`,
			`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`[DONE]`,
		})
	})
	a := newAdapter(fs)
	req, prof := stdReq()
	stream, err := a.Stream(context.Background(), req, prof, []byte("sk-test"))
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := wirecheck.CollectEvents(t, stream)
	resp, ferr := stream.Final()
	if ferr != nil {
		t.Fatalf("Final: %v", ferr)
	}

	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
		{Kind: llm.StreamFinish, Index: -1, WantFinish: "stop"},
	})
	if resp.FinishReason != "stop" {
		t.Errorf("Response.FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
}
