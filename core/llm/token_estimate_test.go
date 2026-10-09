package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEstimateTokens_Table(t *testing.T) {
	cases := []struct{ bytes, want int }{
		{-5, 0},
		{0, 0},
		{1, 1},   // ceil(0.29)
		{3, 1},   // ceil(0.86)
		{4, 2},   // ceil(1.14)
		{7, 2},   // exactly 2
		{8, 3},   // ceil(2.29)
		{35, 10}, // exactly 10
		{36, 11}, // ceil(10.29)
		{3500, 1000},
		{786793, 224798}, // ceil(224798) — the dogfood first-turn order of magnitude
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.bytes); got != tc.want {
			t.Errorf("EstimateTokens(%d) = %d, want %d", tc.bytes, got, tc.want)
		}
	}
}

func TestEstimateToolSpecTokens_CountsNameDescriptionSchema(t *testing.T) {
	spec := ToolSpec{
		Name:        "outlook__send-mail",                             // 18
		Description: strings.Repeat("d", 52),                          // 52
		InputSchema: json.RawMessage(`{"type":"object"}`),             // 17
		Server:      "outlook-with-a-very-long-server-id-not-counted", // metadata, not sent
	}
	// 18 + 52 + 17 = 87 bytes -> ceil(87 / 3.5) = ceil(24.86) = 25
	if got := EstimateToolSpecTokens(spec); got != 25 {
		t.Fatalf("EstimateToolSpecTokens = %d, want 25", got)
	}
	if got := ToolSpecTokens(spec); got != 25 {
		t.Fatalf("ToolSpecTokens without TokenEst = %d, want computed 25", got)
	}
	spec.TokenEst = 40
	if got := ToolSpecTokens(spec); got != 40 {
		t.Fatalf("ToolSpecTokens with TokenEst = %d, want the carried 40", got)
	}
	if got := ToolsTokens([]ToolSpec{spec, {Name: "abcdefg"}}); got != 42 {
		t.Fatalf("ToolsTokens = %d, want 40 + 2", got)
	}
}

// The catalog metadata never reaches a provider: a marshalled ToolSpec is
// byte-identical with and without it.
func TestToolSpec_MetadataNotOnTheWire(t *testing.T) {
	bare := ToolSpec{Name: "n", Description: "d", InputSchema: json.RawMessage(`{}`)}
	withMeta := bare
	withMeta.Server, withMeta.TokenEst = "srv", 9
	a, _ := json.Marshal(bare)
	b, _ := json.Marshal(withMeta)
	if string(a) != string(b) {
		t.Fatalf("wire shape changed: %s vs %s", a, b)
	}
}

func TestMessagesTokens_CountsEveryTextBearingBlock(t *testing.T) {
	msgs := []Message{
		NewTextMessage(RoleUser, strings.Repeat("u", 7)), // 2
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "text", Text: strings.Repeat("a", 7)},                                         // 7
			{Type: "tool_use", ToolUse: &ToolUse{Name: "abc", Input: json.RawMessage(`{"x":1}`)}}, // 3 + 7
		}}, // 17 bytes -> 5
		{Role: RoleTool, Content: []ContentBlock{
			{Type: "tool_result", ToolResult: &ToolResult{Content: json.RawMessage(strings.Repeat("r", 14))}},
		}}, // 4
	}
	if got := MessagesTokens(msgs); got != 2+5+4 {
		t.Fatalf("MessagesTokens = %d, want 11", got)
	}
	if got := MessagesTokens(nil); got != 0 {
		t.Fatalf("MessagesTokens(nil) = %d, want 0", got)
	}
}
