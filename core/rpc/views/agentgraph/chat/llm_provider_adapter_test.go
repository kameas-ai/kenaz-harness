package chat

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// TestGenerate_CarriesMaxTokensAndTemperature is WP01 of
// model-request-path-live-01PMDL01: before this fix Generate() built
// GenerationRequest without MaxTokens/Temperature at all, silently
// dropping every per-node sampling knob authored in ModelAttrs. Asserts
// a node with max_tokens/temperature set produces a request carrying
// them (via Params, the channel every production adapter reads), and
// that empty node values produce no override at all — not a forced
// zero/empty entry that would clobber a provider or profile default.
func TestGenerate_CarriesMaxTokensAndTemperature(t *testing.T) {
	temp := 0.42

	cases := []struct {
		name       string
		req        coreag.LLMRequest
		wantParams map[string]any
	}{
		{
			name: "max_tokens and temperature both set",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				MaxTokens:    2048,
				Temperature:  &temp,
			},
			wantParams: map[string]any{"max_tokens": 2048, "temperature": temp},
		},
		{
			name: "max_tokens only",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				MaxTokens:    512,
			},
			wantParams: map[string]any{"max_tokens": 512},
		},
		{
			name: "temperature only",
			req: coreag.LLMRequest{
				SystemPrompt: "base",
				Temperature:  &temp,
			},
			wantParams: map[string]any{"temperature": temp},
		},
		{
			name:       "empty node values yield provider defaults, not forced zeros",
			req:        coreag.LLMRequest{SystemPrompt: "base"},
			wantParams: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &capturingRegistry{}
			adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

			if _, err := adapter.Generate(context.Background(), tc.req); err != nil {
				t.Fatalf("Generate: %v", err)
			}

			gotParams := reg.snapshot().Params
			if len(tc.wantParams) == 0 {
				if len(gotParams) != 0 {
					t.Fatalf("expected no Params overrides (provider defaults), got %#v", gotParams)
				}
				return
			}
			if len(gotParams) != len(tc.wantParams) {
				t.Fatalf("Params length mismatch: got %#v want %#v", gotParams, tc.wantParams)
			}
			for k, want := range tc.wantParams {
				if got := gotParams[k]; got != want {
					t.Errorf("Params[%q] = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// TestGenerate_CarriesExpandedKnobSurface is WP05 of
// model-request-path-live-01PMDL01: extends the WP01 wiring to the rest
// of the ModelAttrs knob surface (TopP, TopK, FrequencyPenalty,
// PresencePenalty, Seed, ParallelToolCalls — routed through the same
// Params channel as MaxTokens/Temperature) plus StopSequences, which is
// a typed top-level field on GenerationRequest rather than a Params
// entry. Asserts each new knob reaches the seam→Generate() boundary
// correctly, and that omitted/zero-value knobs produce no override.
func TestGenerate_CarriesExpandedKnobSurface(t *testing.T) {
	topP := 0.9
	freqPenalty := 0.25
	presPenalty := -0.1

	topK := 40
	seed := 12345
	parallelToolCalls := true
	reasoningBudget := 4096

	req := coreag.LLMRequest{
		SystemPrompt:          "base",
		TopP:                  &topP,
		TopK:                  &topK,
		FrequencyPenalty:      &freqPenalty,
		PresencePenalty:       &presPenalty,
		Seed:                  &seed,
		ParallelToolCalls:     &parallelToolCalls,
		StopSequences:         []string{"STOP", "END"},
		ReasoningBudgetTokens: &reasoningBudget,
	}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()

	wantParams := map[string]any{
		"top_p":               topP,
		"top_k":               topK,
		"frequency_penalty":   freqPenalty,
		"presence_penalty":    presPenalty,
		"seed":                seed,
		"parallel_tool_calls": parallelToolCalls,
	}
	if len(gen.Params) != len(wantParams) {
		t.Fatalf("Params length mismatch: got %#v want %#v", gen.Params, wantParams)
	}
	for k, want := range wantParams {
		if got := gen.Params[k]; got != want {
			t.Errorf("Params[%q] = %v (%T), want %v (%T)", k, got, got, want, want)
		}
	}

	if len(gen.StopSequences) != 2 || gen.StopSequences[0] != "STOP" || gen.StopSequences[1] != "END" {
		t.Errorf("StopSequences = %#v, want [STOP END]", gen.StopSequences)
	}
	if gen.Reasoning == nil || !gen.Reasoning.Enabled || gen.Reasoning.BudgetTokens != 4096 {
		t.Errorf("Reasoning = %#v, want {Enabled:true BudgetTokens:4096}", gen.Reasoning)
	}
}

// TestGenerate_OmittedKnobsProduceNoOverride verifies that a node with
// none of the WP05 knobs set produces neither Params entries for them
// nor a StopSequences override — the zero-value-means-unset convention
// must not clobber provider/profile defaults with forced zeros.
func TestGenerate_OmittedKnobsProduceNoOverride(t *testing.T) {
	req := coreag.LLMRequest{SystemPrompt: "base"}

	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

	if _, err := adapter.Generate(context.Background(), req); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	gen := reg.snapshot()
	for _, k := range []string{"top_p", "top_k", "frequency_penalty", "presence_penalty", "seed", "parallel_tool_calls"} {
		if _, ok := gen.Params[k]; ok {
			t.Errorf("Params[%q] unexpectedly present: %#v", k, gen.Params)
		}
	}
	if len(gen.StopSequences) != 0 {
		t.Errorf("StopSequences = %#v, want empty", gen.StopSequences)
	}
	if gen.Reasoning != nil {
		t.Errorf("Reasoning = %#v, want nil", gen.Reasoning)
	}
}

// TestGenerate_ToolMessage_CarriesIsError is WP01 of
// tool-error-legibility-01PMDL02: before this fix, a Role="tool" Message
// with IsError=true was translated into a corellm.ToolResult without
// IsError, so a failed tool call reached the wire looking identical to a
// success. Asserts the seam's IsError flag threads through onto the
// tool_result content block for both a failing and a succeeding call.
func TestGenerate_ToolMessage_CarriesIsError(t *testing.T) {
	cases := []struct {
		name        string
		isError     bool
		wantIsError bool
	}{
		{name: "failing tool result sets IsError", isError: true, wantIsError: true},
		{name: "succeeding tool result leaves IsError false", isError: false, wantIsError: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := &capturingRegistry{}
			adapter := NewLLMProviderAdapter(reg, "profile-1", "openai/gpt-4o", nil, nil)

			req := coreag.LLMRequest{
				SystemPrompt: "base",
				Messages: []coreag.Message{
					{
						Role:       "tool",
						Name:       "svc__broken",
						ToolCallID: "call-1",
						Content:    "boom",
						IsError:    tc.isError,
					},
				},
			}

			if _, err := adapter.Generate(context.Background(), req); err != nil {
				t.Fatalf("Generate: %v", err)
			}

			gen := reg.snapshot()
			if len(gen.Messages) != 1 {
				t.Fatalf("expected 1 translated message, got %d: %#v", len(gen.Messages), gen.Messages)
			}
			blocks := gen.Messages[0].Content
			if len(blocks) != 1 || blocks[0].Type != "tool_result" || blocks[0].ToolResult == nil {
				t.Fatalf("expected one tool_result block, got %#v", blocks)
			}
			if got := blocks[0].ToolResult.IsError; got != tc.wantIsError {
				t.Errorf("ToolResult.IsError = %v, want %v", got, tc.wantIsError)
			}
		})
	}
}
