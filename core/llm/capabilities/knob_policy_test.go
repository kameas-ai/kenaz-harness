package capabilities_test

// Tests for KnobPolicy.Apply (provider-implementation-uniformity-01KQ8V4F WP08).

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

func ptr[T any](v T) *T { return &v }

// baseCaps returns a ProviderCapabilities record with all boolean knobs
// enabled so individual tests can selectively disable what they're testing.
func baseCaps() llm.ProviderCapabilities {
	return llm.ProviderCapabilities{
		Provider:          "openai",
		Model:             "gpt-4o",
		Seed:              true,
		TopK:              true,
		TopP:              true,
		FrequencyPenalty:  true,
		PresencePenalty:   true,
		ParallelToolCalls: true,
		Reasoning:         false, // default off; tests that need it flip it
	}
}

// TestKnobPolicy_NilKnobs verifies Apply is a no-op for nil input.
func TestKnobPolicy_NilKnobs(t *testing.T) {
	t.Parallel()
	p := capabilities.NewKnobPolicy(baseCaps())
	cleaned, dropped, err := p.Apply(nil)
	if err != nil {
		t.Fatalf("Apply(nil): unexpected error: %v", err)
	}
	if cleaned != nil {
		t.Errorf("Apply(nil): want nil cleaned, got %+v", cleaned)
	}
	if len(dropped) != 0 {
		t.Errorf("Apply(nil): want 0 dropped, got %d", len(dropped))
	}
}

// TestKnobPolicy_AllSupported verifies no knobs are dropped when the model
// supports all of them.
func TestKnobPolicy_AllSupported(t *testing.T) {
	t.Parallel()
	p := capabilities.NewKnobPolicy(baseCaps())
	knobs := &llm.RequestKnobs{
		Seed:              ptr(42),
		TopP:              ptr(0.9),
		TopK:              ptr(50),
		FrequencyPenalty:  ptr(0.1),
		PresencePenalty:   ptr(0.1),
		ParallelToolCalls: ptr(true),
	}
	_, dropped, err := p.Apply(knobs)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("want 0 dropped, got %d: %v", len(dropped), dropped)
	}
}

// TestKnobPolicy_SeedDropped verifies seed is silently removed when unsupported.
func TestKnobPolicy_SeedDropped(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Seed = false
	p := capabilities.NewKnobPolicy(caps)
	knobs := &llm.RequestKnobs{Seed: ptr(7)}
	cleaned, dropped, err := p.Apply(knobs)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if cleaned.Seed != nil {
		t.Errorf("Seed should be nil after drop, got %v", cleaned.Seed)
	}
	if len(dropped) != 1 || dropped[0].Name != "seed" {
		t.Errorf("want [seed] dropped, got %v", dropped)
	}
}

// TestKnobPolicy_TopKDropped verifies top_k is silently dropped when unsupported.
func TestKnobPolicy_TopKDropped(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.TopK = false
	p := capabilities.NewKnobPolicy(caps)
	knobs := &llm.RequestKnobs{TopK: ptr(50)}
	cleaned, dropped, err := p.Apply(knobs)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if cleaned.TopK != nil {
		t.Errorf("TopK should be nil after drop, got %v", cleaned.TopK)
	}
	if len(dropped) != 1 || dropped[0].Name != "top_k" {
		t.Errorf("want [top_k] dropped, got %v", dropped)
	}
}

// TestKnobPolicy_ReasoningDroppedOnNonReasoningModel verifies reasoning is
// silently dropped when the model has Reasoning=false.
func TestKnobPolicy_ReasoningDroppedOnNonReasoningModel(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Reasoning = false
	p := capabilities.NewKnobPolicy(caps)
	knobs := &llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{OpenAIEffort: "high"},
	}
	cleaned, dropped, err := p.Apply(knobs)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if cleaned.Reasoning != nil {
		t.Errorf("Reasoning should be nil after drop, got %+v", cleaned.Reasoning)
	}
	if len(dropped) != 1 || dropped[0].Name != "reasoning" {
		t.Errorf("want [reasoning] dropped, got %v", dropped)
	}
}

// TestKnobPolicy_EffortStringOnTokenBudgetModel verifies ErrUnsupportedFeature
// is returned when an effort string is used on an Anthropic token-budget model.
func TestKnobPolicy_EffortStringOnTokenBudgetModel(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Reasoning = true
	caps.Reasoning_ = llm.ReasoningTokenBudget
	p := capabilities.NewKnobPolicy(caps)
	knobs := &llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{OpenAIEffort: "high"},
	}
	_, _, err := p.Apply(knobs)
	if err == nil {
		t.Fatal("Apply: want ErrUnsupportedFeature, got nil")
	}
	if !llm.IsUnsupportedFeature(err) {
		t.Errorf("Apply: want ErrUnsupportedFeature, got %T: %v", err, err)
	}
}

// TestKnobPolicy_TokenBudgetOnEffortStringModel verifies ErrUnsupportedFeature
// is returned when a token budget is used on an OpenAI effort-string model.
func TestKnobPolicy_TokenBudgetOnEffortStringModel(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Reasoning = true
	caps.Reasoning_ = llm.ReasoningEffortString
	p := capabilities.NewKnobPolicy(caps)
	knobs := &llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{AnthropicThinkingBudget: 16000},
	}
	_, _, err := p.Apply(knobs)
	if err == nil {
		t.Fatal("Apply: want ErrUnsupportedFeature, got nil")
	}
	if !llm.IsUnsupportedFeature(err) {
		t.Errorf("Apply: want ErrUnsupportedFeature, got %T: %v", err, err)
	}
}

// TestKnobPolicy_BothStyleAcceptsEither verifies ReasoningBoth style accepts
// either effort strings or token budgets.
func TestKnobPolicy_BothStyleAcceptsEither(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Reasoning = true
	caps.Reasoning_ = llm.ReasoningBoth

	p := capabilities.NewKnobPolicy(caps)

	// Effort string
	cleaned, dropped, err := p.Apply(&llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{OpenAIEffort: "medium"},
	})
	if err != nil {
		t.Fatalf("effort string on Both: unexpected error: %v", err)
	}
	if cleaned.Reasoning == nil {
		t.Error("effort string on Both: Reasoning should be retained")
	}
	if len(dropped) != 0 {
		t.Errorf("effort string on Both: want 0 dropped, got %v", dropped)
	}

	// Token budget
	cleaned, dropped, err = p.Apply(&llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{AnthropicThinkingBudget: 8000},
	})
	if err != nil {
		t.Fatalf("token budget on Both: unexpected error: %v", err)
	}
	if cleaned.Reasoning == nil {
		t.Error("token budget on Both: Reasoning should be retained")
	}
	if len(dropped) != 0 {
		t.Errorf("token budget on Both: want 0 dropped, got %v", dropped)
	}
}

// TestKnobPolicy_CorrectStylePassesThrough verifies that a matching
// reasoning style is passed through without modification.
func TestKnobPolicy_CorrectStylePassesThrough(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Reasoning = true
	caps.Reasoning_ = llm.ReasoningEffortString
	p := capabilities.NewKnobPolicy(caps)

	knobs := &llm.RequestKnobs{
		Reasoning: &llm.ReasoningConfig{OpenAIEffort: "low"},
	}
	cleaned, dropped, err := p.Apply(knobs)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if cleaned.Reasoning == nil || cleaned.Reasoning.OpenAIEffort != "low" {
		t.Errorf("Apply: reasoning should be retained, got %+v", cleaned.Reasoning)
	}
	if len(dropped) != 0 {
		t.Errorf("want 0 dropped, got %v", dropped)
	}
}

// TestKnobPolicy_OriginalNotMutated verifies Apply does not mutate the
// input knobs struct.
func TestKnobPolicy_OriginalNotMutated(t *testing.T) {
	t.Parallel()
	caps := baseCaps()
	caps.Seed = false
	p := capabilities.NewKnobPolicy(caps)

	seed := 99
	knobs := &llm.RequestKnobs{Seed: &seed}
	_, _, _ = p.Apply(knobs)

	// Original should still point to seed.
	if knobs.Seed == nil || *knobs.Seed != 99 {
		t.Errorf("Apply mutated input: seed = %v", knobs.Seed)
	}
}
