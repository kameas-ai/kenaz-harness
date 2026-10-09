package chat

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// The mid-stream usage frame (which drives the live context meter) carries
// the whole prompt under the provider's convention: a warm Anthropic cache
// does not drop the meter to the uncached remainder.
func TestTranslateLLMStreamEvent_UsageIsPromptTotal(t *testing.T) {
	u := &corellm.Usage{InputTokens: 300, OutputTokens: 7, CachedInputRead: 29_000, CachedInputWrite: 700}
	ev := corellm.StreamEvent{Kind: corellm.StreamUsage, Usage: u}
	if got := translateLLMStreamEvent(ev, "anthropic").UsageInputTokens; got != 30_000 {
		t.Errorf("anthropic usage input = %d, want 30000", got)
	}
	inclusive := &corellm.Usage{InputTokens: 30_000, OutputTokens: 7, CachedInputRead: 29_000}
	if got := translateLLMStreamEvent(corellm.StreamEvent{Kind: corellm.StreamUsage, Usage: inclusive}, "openrouter").UsageInputTokens; got != 30_000 {
		t.Errorf("openrouter usage input = %d, want 30000", got)
	}
}

// The run token budget (TokensUsed → env.Counters) counts the whole
// prompt, so Anthropic and OpenRouter calls weigh the same.
func TestGenerate_TokensUsedIsPromptTotal(t *testing.T) {
	final := corellm.Response{
		FinishReason: "stop",
		Usage:        corellm.Usage{InputTokens: 300, OutputTokens: 10, CachedInputRead: 29_000, CachedInputWrite: 700},
	}
	reg := &kindStubRegistry{usageStubRegistry: usageStubRegistry{stream: &usageStubStream{final: final}}, kind: "anthropic"}
	adapter := NewLLMProviderAdapter(reg, "p-ant", "claude-sonnet-4-5", nil, nil)
	out, err := adapter.Generate(context.Background(), coreag.LLMRequest{
		SystemPrompt: "base",
		Messages:     []coreag.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TokensUsed != 30_010 {
		t.Errorf("TokensUsed = %d, want 30010", out.TokensUsed)
	}
}

// kindStubRegistry is usageStubRegistry with a chosen provider kind.
type kindStubRegistry struct {
	usageStubRegistry
	kind string
}

func (r *kindStubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{ID: "p", Kind: r.kind, Model: "m"}, nil
}
