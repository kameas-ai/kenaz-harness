package capabilities

import (
	"errors"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestGate_ReasoningFalseModel_StillRefusesAfterWP08 is WP08's gate-level
// regression test (model-settings-reach-the-model-01PMZ101, tasks.md WP08
// "Tests" bullet 3): "a gate-level test that a reasoning: false model
// still refuses before the encoder runs — the encoder change must not
// become a way around the gate."
//
// WP08 taught openaiwire.BuildRequestBody to map req.Reasoning to
// reasoning_effort even without Knobs. That change lives inside the body
// encoder, which the registry only calls *after* capabilities.Gate.Check
// has already run (core/llm/registry/registry.go:384). azure-openai and
// custom-openai both declare reasoning: false (core/llm/capabilities/
// data/custom-openai.yaml; azure-openai aliases to openai, whose gpt-4o
// row is also reasoning: false) — this test asserts the gate refuses
// those requests, so the new encoder capability is never reached for a
// model that doesn't advertise it.
func TestGate_ReasoningFalseModel_StillRefusesAfterWP08(t *testing.T) {
	c := mustCatalog(t)
	g := NewGate(c)
	req := llm.GenerationRequest{
		ProfileID: "test",
		Messages:  []llm.Message{llm.NewTextMessage(llm.RoleUser, "think about it")},
		Reasoning: &llm.ReasoningSpec{Enabled: true, BudgetTokens: 8192},
	}
	cases := []struct{ kind, model string }{
		{"azure-openai", "gpt-4o"},
		{"custom-openai", "llama3.1"},
	}
	for _, tc := range cases {
		prof := llm.ProviderProfile{Kind: tc.kind, Model: tc.model}
		_, err := g.Check(req, prof)
		if err == nil {
			t.Errorf("%s/%s: Gate.Check(reasoning) = nil; want refusal (reasoning: false)", tc.kind, tc.model)
			continue
		}
		var capErr *llm.ErrCapabilityUnsupported
		if !errors.As(err, &capErr) {
			t.Errorf("%s/%s: err = %T; want *llm.ErrCapabilityUnsupported", tc.kind, tc.model, err)
		}
	}
}
