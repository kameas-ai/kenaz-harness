package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fixedAttachments resolves one system-kind attachment.
type fixedAttachments struct{ content string }

func (f fixedAttachments) ListResolved(context.Context, string) ([]ResolvedAttachment, error) {
	return []ResolvedAttachment{{Kind: "system", Content: f.content}}, nil
}

// Every Generate call writes exactly one llm.request.composition line
// carrying every part of the request and the provider's counts, and the
// same measurement travels on the response for the usage hook.
func TestGenerate_LogsRequestCompositionWithEveryField(t *testing.T) {
	tools := []corellm.ToolSpec{
		{Name: "outlook__send-mail", Description: strings.Repeat("d", 345), InputSchema: json.RawMessage(`{}`), TokenEst: 1000},
		{Name: "kenaz__bash", Description: "run", InputSchema: json.RawMessage(`{}`)}, // no TokenEst: computed
	}
	final := corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: "hi back"}},
		FinishReason: "stop",
		Usage:        corellm.Usage{InputTokens: 224798, OutputTokens: 9, CachedInputRead: 200000, CachedInputWrite: 24000},
	}
	reg := &usageStubRegistry{stream: &usageStubStream{final: final}}
	attachment := strings.Repeat("a", 70) // 20 tokens
	adapter := NewLLMProviderAdapter(reg, "p-openrouter", "anthropic/claude-haiku", tools, nil).
		WithSessionID("sess-comp").
		WithAttachments(fixedAttachments{content: attachment})

	history := strings.Repeat("h", 350) // 100 tokens
	logs := captureChatLog(t, func() {
		if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{
			SystemPrompt: "base",
			Messages:     []coreag.Message{{Role: "user", Content: history}},
		}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
	})

	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == "llm.request.composition" {
			lines = append(lines, rec)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("llm.request.composition lines = %d, want exactly 1:\n%s", len(lines), logs)
	}
	rec := lines[0]
	bashEst := corellm.EstimateToolSpecTokens(tools[1])
	want := map[string]float64{
		"tools_full":             2,
		"tools_summary":          0,
		"tools_tokens_est":       float64(1000 + bashEst),
		"attachments_tokens_est": 20,
		"history_tokens_est":     100,
		"prompt_tokens":          224798,
		"cached_tokens":          200000,
		"cache_write_tokens":     24000,
		"budget":                 0,
		"evicted":                0,
	}
	for k, v := range want {
		got, ok := rec[k].(float64)
		if !ok {
			t.Errorf("field %q missing from the composition line: %v", k, rec)
			continue
		}
		if got != v {
			t.Errorf("%s = %v, want %v", k, got, v)
		}
	}
	sysChars, ok := rec["system_chars"].(float64)
	if !ok || sysChars < float64(len("base")+len(attachment)) {
		t.Errorf("system_chars = %v, want at least the base prompt plus the attachment", rec["system_chars"])
	}
	if rec["outcome"] != "ok" || rec["session_id"] != "sess-comp" {
		t.Errorf("outcome/session_id = %v/%v, want ok/sess-comp", rec["outcome"], rec["session_id"])
	}
	// The system part excludes the attachment, so the parts do not overlap.
	if st, _ := rec["system_tokens_est"].(float64); st <= 0 || st >= sysChars/3.5 {
		t.Errorf("system_tokens_est = %v, want > 0 and less than the whole system prompt (%v chars)", st, sysChars)
	}

	comp := adapter.LastResponse().Composition
	if comp == nil {
		t.Fatal("response carries no composition")
	}
	if comp.Tools != 1000+bashEst || comp.History != 100 || comp.Attachments != 20 || comp.Cached != 200000 || comp.ToolsFull != 2 {
		t.Errorf("composition = %+v", *comp)
	}
}
