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
