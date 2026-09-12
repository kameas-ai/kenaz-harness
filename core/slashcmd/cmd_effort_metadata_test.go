package slashcmd

// cmd_effort_metadata_test.go — model-settings-reach-the-model-01PMZ101
// UNIT-6 / WP11, AC-010(a).
//
// /effort's Result.Metadata[MetaKeyReasoningKnob] must marshal to the
// exact camelCase keys SessionsView.vue's slash-result handler reads
// (openAIEffort / anthropicThinkingBudget) — not
// llm.ReasoningConfig's own snake_case JSON tags
// (openai_effort / anthropic_thinking_budget), which is what shipped
// before this WP and which every prior test asserting on the Go struct
// field name (ReasoningConfig.OpenAIEffort) could not have caught.
// Asserting on the MARSHALLED BYTES, not the struct, is the point (spec
// §8 rule 4 / AC-010(a)'s own "fails if it asserts on the Go struct
// field name" clause).

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEffortCommand_MetadataMarshalsCamelCaseKeys_EffortString(t *testing.T) {
	t.Parallel()
	res, err := (effortCommand{}).Run(context.Background(), Env{}, []string{"high"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	knob, ok := res.Metadata[MetaKeyReasoningKnob]
	if !ok {
		t.Fatalf("Metadata[%q] missing; got %#v", MetaKeyReasoningKnob, res.Metadata)
	}

	b, err := json.Marshal(knob)
	if err != nil {
		t.Fatalf("json.Marshal(knob): %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(marshalled knob): %v", err)
	}

	if raw, ok := decoded["openAIEffort"]; !ok {
		t.Errorf("marshalled knob %s has no \"openAIEffort\" key; want it present (frontend reads knob['openAIEffort'])", b)
	} else if string(raw) != `"high"` {
		t.Errorf("openAIEffort = %s, want \"high\"", raw)
	}

	// The bug this test exists to catch: llm.ReasoningConfig's own JSON
	// tag is "openai_effort", not "openAIEffort". If the fix regresses
	// to marshalling the struct directly, this snake_case key
	// reappears and the camelCase assertion above fails — but assert
	// its ABSENCE too so a future change that emits BOTH keys (masking
	// the regression) is still caught.
	if _, ok := decoded["openai_effort"]; ok {
		t.Errorf("marshalled knob %s carries the snake_case \"openai_effort\" key — the frontend never reads this key; its presence means the fix regressed to marshalling llm.ReasoningConfig directly", b)
	}
}

func TestEffortCommand_MetadataMarshalsCamelCaseKeys_TokenBudget(t *testing.T) {
	t.Parallel()
	res, err := (effortCommand{}).Run(context.Background(), Env{}, []string{"16000"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	knob, ok := res.Metadata[MetaKeyReasoningKnob]
	if !ok {
		t.Fatalf("Metadata[%q] missing; got %#v", MetaKeyReasoningKnob, res.Metadata)
	}

	b, err := json.Marshal(knob)
	if err != nil {
		t.Fatalf("json.Marshal(knob): %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(marshalled knob): %v", err)
	}

	if raw, ok := decoded["anthropicThinkingBudget"]; !ok {
		t.Errorf("marshalled knob %s has no \"anthropicThinkingBudget\" key; want it present (frontend reads knob['anthropicThinkingBudget'])", b)
	} else if string(raw) != "16000" {
		t.Errorf("anthropicThinkingBudget = %s, want 16000", raw)
	}
	if _, ok := decoded["anthropic_thinking_budget"]; ok {
		t.Errorf("marshalled knob %s carries the snake_case \"anthropic_thinking_budget\" key — the frontend never reads this key", b)
	}
}
