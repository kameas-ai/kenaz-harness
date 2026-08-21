package bedrock

// AC-003 (model-settings-reach-the-model-01PMZ101 WP04 / FR-003): Bedrock
// must round-trip a tool_use / tool_result pair on BOTH transports —
// the AWS-SDK ConverseStream path (toBedrockMessages / toBedrockContentBlocks,
// reached when ProviderProfile.Cred.Kind != "keychain") and the bearer/REST
// Converse path (toConverseJSON / toConverseContentBlocksJSON, reached when
// Cred.Kind == "keychain"). Before this WP both silently dropped every
// RoleTool message ("Tool results aren't yet wired through the harness —
// skip"), so the model saw a dangling tool_use with no answer on every
// tool-bearing turn.
//
// Per spec §8 rule 4 ("wire-shape tests assert bytes, not structs") and
// AC-003's explicit table requirement ("parameterise one table over both
// encoders... a single-transport test lets bearer.go keep the defect"):
// the bearer/REST path is asserted on the actual marshalled HTTP request
// body (via the existing capturedBody + wirecheck harness used throughout
// this file). The SDK path's union types
// (types.ContentBlockMemberToolUse / ...ToolResult) do not implement
// encoding/json.Marshaler — the AWS SDK protocol layer serializes them
// internally — so the SDK-path assertions call the union's own
// document.Interface.MarshalSmithyDocument() to obtain the exact bytes
// the wire encoder would produce for the tool_use input / tool_result
// content, rather than reading the unexported Go struct fields.

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/wirecheck"
)

// toolRoundTripMessages builds a minimal three-turn conversation: a user
// question, an assistant tool_use call, and a tool_result answering it.
// isError controls the ToolResult.IsError flag so both success and error
// paths are covered by the same table.
func toolRoundTripMessages(isError bool) []llm.Message {
	return []llm.Message{
		bedrockUserMsg("what's the weather in Boston?"),
		{
			Role: llm.RoleAssistant,
			Content: []llm.ContentBlock{
				{Type: "tool_use", ToolUse: &llm.ToolUse{
					ID:    "call_1",
					Name:  "get_weather",
					Input: json.RawMessage(`{"city":"Boston"}`),
				}},
			},
		},
		{
			Role: llm.RoleTool,
			Content: []llm.ContentBlock{
				{Type: "tool_result", ToolResult: &llm.ToolResult{
					ToolUseID: "call_1",
					Content:   json.RawMessage(`{"tempF":72}`),
					IsError:   isError,
				}},
			},
		},
	}
}

// --- SDK transport (toBedrockMessages / toBedrockContentBlocks) ---

func TestBedrockSDK_ToolRoundTrip_ToolUseIdPairing(t *testing.T) {
	cases := []struct {
		name       string
		isError    bool
		wantStatus types.ToolResultStatus
	}{
		{"success", false, types.ToolResultStatusSuccess},
		{"error", true, types.ToolResultStatusError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msgs, _, err := toBedrockMessages(toolRoundTripMessages(tc.isError))
			if err != nil {
				t.Fatalf("toBedrockMessages: %v", err)
			}
			if len(msgs) != 3 {
				t.Fatalf("want 3 messages (user, assistant tool_use, tool_result), got %d: %+v", len(msgs), msgs)
			}

			// Assistant message carries the toolUse block.
			assistant := msgs[1]
			if assistant.Role != types.ConversationRoleAssistant {
				t.Fatalf("assistant message role = %v, want assistant", assistant.Role)
			}
			if len(assistant.Content) != 1 {
				t.Fatalf("assistant content = %+v, want exactly the toolUse block", assistant.Content)
			}
			tu, ok := assistant.Content[0].(*types.ContentBlockMemberToolUse)
			if !ok {
				t.Fatalf("assistant.Content[0] = %T, want *types.ContentBlockMemberToolUse", assistant.Content[0])
			}
			if got := aws_stringVal(tu.Value.ToolUseId); got != "call_1" {
				t.Fatalf("toolUse.ToolUseId = %q, want %q", got, "call_1")
			}
			if got := aws_stringVal(tu.Value.Name); got != "get_weather" {
				t.Fatalf("toolUse.Name = %q, want %q", got, "get_weather")
			}
			inputBytes, err := tu.Value.Input.MarshalSmithyDocument()
			if err != nil {
				t.Fatalf("toolUse.Input.MarshalSmithyDocument: %v", err)
			}
			if string(inputBytes) != `{"city":"Boston"}` {
				t.Fatalf("toolUse.Input wire bytes = %s, want %s", inputBytes, `{"city":"Boston"}`)
			}

			// Tool-result message: role MUST be "user" (Converse requires
			// it) and its toolUseId MUST equal the prior toolUse's id — an
			// unpaired id is a 400, which presence alone cannot catch.
			toolMsg := msgs[2]
			if toolMsg.Role != types.ConversationRoleUser {
				t.Fatalf("tool-result message role = %v, want user (Converse requires it)", toolMsg.Role)
			}
			if len(toolMsg.Content) != 1 {
				t.Fatalf("tool-result content = %+v, want exactly the toolResult block", toolMsg.Content)
			}
			tr, ok := toolMsg.Content[0].(*types.ContentBlockMemberToolResult)
			if !ok {
				t.Fatalf("toolMsg.Content[0] = %T, want *types.ContentBlockMemberToolResult", toolMsg.Content[0])
			}
			if got, want := aws_stringVal(tr.Value.ToolUseId), aws_stringVal(tu.Value.ToolUseId); got != want {
				t.Fatalf("toolResult.ToolUseId = %q, want it to pair with the prior toolUse id %q", got, want)
			}
			if tr.Value.Status != tc.wantStatus {
				t.Fatalf("toolResult.Status = %q, want %q", tr.Value.Status, tc.wantStatus)
			}
			if len(tr.Value.Content) != 1 {
				t.Fatalf("toolResult.Content = %+v, want exactly one block", tr.Value.Content)
			}
			jsonBlock, ok := tr.Value.Content[0].(*types.ToolResultContentBlockMemberJson)
			if !ok {
				t.Fatalf("toolResult.Content[0] = %T, want *types.ToolResultContentBlockMemberJson", tr.Value.Content[0])
			}
			resultBytes, err := jsonBlock.Value.MarshalSmithyDocument()
			if err != nil {
				t.Fatalf("toolResult content MarshalSmithyDocument: %v", err)
			}
			if string(resultBytes) != `{"tempF":72}` {
				t.Fatalf("toolResult content wire bytes = %s, want %s", resultBytes, `{"tempF":72}`)
			}
		})
	}
}

// aws_stringVal dereferences a *string, returning "" for nil — a local
// helper so this file does not need to import the aws SDK's own ToString
// just for test assertions.
func aws_stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// --- Bearer/REST transport (toConverseJSON / toConverseContentBlocksJSON) ---

// TestBedrockBearer_ToolRoundTrip_ToolUseIdPairing drives the REAL adapter
// (Cred.Kind == "keychain" selects streamWithBearer) through capturedBody,
// exactly like every other *_Serialized test in this package, and asserts
// on the marshalled HTTP request bytes.
func TestBedrockBearer_ToolRoundTrip_ToolUseIdPairing(t *testing.T) {
	req := llm.GenerationRequest{Messages: toolRoundTripMessages(false)}
	body := capturedBody(t, req, "anthropic.claude-3-haiku-20240307-v1:0")
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		(wirecheck.FieldExpectation{Pointer: "/messages", WantPresent: true}).ArrayLen(3),
		{Pointer: "/messages/1/content/0/toolUse/toolUseId", WantString: "call_1"},
		{Pointer: "/messages/1/content/0/toolUse/name", WantString: "get_weather"},
		{Pointer: "/messages/2/role", WantString: "user",
			Label: "tool-result message role must be \"user\" (Converse requires it)"},
		{Pointer: "/messages/2/content/0/toolResult/toolUseId", WantString: "call_1",
			Label: "toolResult.toolUseId must pair with the prior toolUse id — an unpaired id is a 400"},
		{Pointer: "/messages/2/content/0/toolResult/content/0/json/tempF", WantNumber: float64Ptr(72)},
		{Pointer: "/messages/2/content/0/toolResult/status", WantString: "success"},
	})
}

func TestBedrockBearer_ToolRoundTrip_ErrorStatus(t *testing.T) {
	req := llm.GenerationRequest{Messages: toolRoundTripMessages(true)}
	body := capturedBody(t, req, "anthropic.claude-3-haiku-20240307-v1:0")
	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
		{Pointer: "/messages/2/content/0/toolResult/status", WantString: "error"},
	})
}
