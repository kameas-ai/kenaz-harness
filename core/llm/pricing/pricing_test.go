package pricing_test

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/pricing"
)

func TestLookup_AnthropicSonnet(t *testing.T) {
	e, ok := pricing.Lookup("anthropic", "claude-sonnet-4-5")
	if !ok {
		t.Fatal("expected match for anthropic/claude-sonnet-4-5")
	}
	if e.InputPer1MUSD <= 0 || e.OutputPer1MUSD <= 0 {
		t.Fatalf("expected positive rates, got input=%v output=%v", e.InputPer1MUSD, e.OutputPer1MUSD)
	}
}

func TestLookup_UnknownModelReturnsFalse(t *testing.T) {
	_, ok := pricing.Lookup("anthropic", "claude-future-9000-ultra")
	if ok {
		t.Fatal("expected no match for unknown model")
	}
}

func TestLookup_UnknownProviderReturnsFalse(t *testing.T) {
	_, ok := pricing.Lookup("someunknownprovider", "claude-sonnet-4-5")
	if ok {
		t.Fatal("expected no match for unknown provider")
	}
}

func TestLookup_OpenRouterWildcard(t *testing.T) {
	e, ok := pricing.Lookup("openrouter", "any-random-model")
	if !ok {
		t.Fatal("expected wildcard match for openrouter")
	}
	if e.InputPer1MUSD < 0 {
		t.Fatalf("negative rate: %v", e.InputPer1MUSD)
	}
}

func TestLookup_OllamaZeroRate(t *testing.T) {
	e, ok := pricing.Lookup("ollama", "llama3")
	if !ok {
		t.Fatal("expected wildcard match for ollama")
	}
	if e.InputPer1MUSD != 0 || e.OutputPer1MUSD != 0 {
		t.Fatalf("expected zero rates for ollama, got input=%v output=%v", e.InputPer1MUSD, e.OutputPer1MUSD)
	}
}

func TestLastUpdatedDate_Parses(t *testing.T) {
	d := pricing.LastUpdatedDate()
	if d.IsZero() {
		t.Fatal("expected non-zero last updated date")
	}
	// Should be in the past relative to the test's own moment (sanity check).
	if !d.Before(time.Now().Add(24 * time.Hour)) {
		t.Fatalf("last_updated date %v looks wrong", d)
	}
}

func TestLastUpdatedString_NonEmpty(t *testing.T) {
	s := pricing.LastUpdatedString()
	if s == "" {
		t.Fatal("expected non-empty last_updated string")
	}
}

func TestLookup_BedrockClaude(t *testing.T) {
	e, ok := pricing.Lookup("bedrock", "anthropic.claude-3-sonnet-20240229-v1:0")
	if !ok {
		t.Fatal("expected match for bedrock anthropic.claude-3-sonnet")
	}
	if e.InputPer1MUSD <= 0 {
		t.Fatal("expected positive bedrock rate")
	}
}

func TestLookup_OpenAIGPT4O(t *testing.T) {
	e, ok := pricing.Lookup("openai", "gpt-4o-2024-05-13")
	if !ok {
		t.Fatal("expected match for gpt-4o*")
	}
	if e.InputPer1MUSD <= 0 {
		t.Fatal("expected positive openai rate")
	}
}

func TestLookup_OpenAIO1HasReasoning(t *testing.T) {
	e, ok := pricing.Lookup("openai", "o1-2024-12-17")
	if !ok {
		t.Fatal("expected match for o1")
	}
	if e.ReasoningPer1MUSD <= 0 {
		t.Fatalf("expected positive reasoning rate for o1, got %v", e.ReasoningPer1MUSD)
	}
}
