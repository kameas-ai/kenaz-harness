package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
)

// The estimator is the shared tokenizer rule: ceil(runes / 4) per text,
// plus 4 framing tokens per message (and for the system slot).
func TestEstimateTokens_Table(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("x", 40), 10},
		{strings.Repeat("x", 41), 11},
		{"héllo", 2},                          // 5 runes (6 bytes): runes, not bytes
		{strings.Repeat("日", 8), 2},           // 8 runes, 24 bytes
		{strings.Repeat("x", 899192), 224798}, // the dogfood first-turn order of magnitude
	}
	for _, tc := range cases {
		if got := EstimateTokens(tc.text); got != tc.want {
			t.Errorf("EstimateTokens(%d runes) = %d, want %d", len([]rune(tc.text)), got, tc.want)
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
	// 18 + 52 + 17 = 87 runes -> ceil(87 / 4) = 22
	if got := EstimateToolSpecTokens(spec); got != 22 {
		t.Fatalf("EstimateToolSpecTokens = %d, want 22", got)
	}
	if got := ToolSpecTokens(spec); got != 22 {
		t.Fatalf("ToolSpecTokens without TokenEst = %d, want computed 22", got)
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
		NewTextMessage(RoleUser, strings.Repeat("u", 8)), // 2 + 4 framing
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: "text", Text: strings.Repeat("a", 6)},                                          // 6
			{Type: "tool_use", ToolUse: &ToolUse{Name: "abc", Input: json.RawMessage(`{"x":1}`)}}, // 3 + 7
		}}, // 16 runes -> 4 + 4 framing
		{Role: RoleTool, Content: []ContentBlock{
			{Type: "tool_result", ToolResult: &ToolResult{Content: json.RawMessage(strings.Repeat("r", 12))}},
		}}, // 3 + 4 framing
	}
	if got := MessagesTokens(msgs); got != 6+8+7 {
		t.Fatalf("MessagesTokens = %d, want 21", got)
	}
	if got := MessagesTokens(nil); got != 0 {
		t.Fatalf("MessagesTokens(nil) = %d, want 0", got)
	}
}

// System + history is exactly what the canonical counter reports for the
// same request, so the composition agrees with compaction and the
// request-too-large check.
func TestSystemPlusHistory_EqualsCanonicalCount(t *testing.T) {
	system := strings.Repeat("s", 2292)
	msgs := []Message{NewTextMessage(RoleUser, "hello there"), NewTextMessage(RoleAssistant, "hi")}
	want := tokenizer.CountRequestTokens(system, []tokenizer.Message{
		{Role: "user", Content: "hello there"}, {Role: "assistant", Content: "hi"},
	})
	if got := SystemTokens(system) + MessagesTokens(msgs); got != want {
		t.Fatalf("SystemTokens + MessagesTokens = %d, want CountRequestTokens = %d", got, want)
	}
}

func TestPromptTokensTotal_NormalisesProviderConventions(t *testing.T) {
	u := Usage{InputTokens: 12, CachedInputRead: 9000, CachedInputWrite: 300}
	if got := PromptTokensTotal(u, "anthropic"); got != 9312 {
		t.Errorf("anthropic total = %d, want input + read + write = 9312", got)
	}
	inclusive := Usage{InputTokens: 224798, CachedInputRead: 200000, CachedInputWrite: 24000}
	for _, kind := range []string{"openrouter", "openai", "gemini", ""} {
		if got := PromptTokensTotal(inclusive, kind); got != 224798 {
			t.Errorf("%q total = %d, want prompt_tokens as reported (224798)", kind, got)
		}
	}
}
