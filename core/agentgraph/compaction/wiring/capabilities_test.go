package wiring

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
)

// TestCapabilityLookup_MatchesPreConvergenceBuiltinTable pins the exact
// numbers the pre-01PMDL05 hardcoded `builtinContextWindows` table used
// to return, now sourced from core/llm/capabilities/data/*.yaml instead.
// This is the "no default-install behaviour change" evidence for
// collapsing the two context-window sources into one: every model the
// old table knew about must resolve to the identical budget through the
// new YAML-backed fallback path.
func TestCapabilityLookup_MatchesPreConvergenceBuiltinTable(t *testing.T) {
	lookup := NewCapabilityLookup()

	cases := []struct {
		provider string
		model    string
		want     int
	}{
		// Anthropic direct.
		{"anthropic", "claude-sonnet-4-5", 200000},
		{"anthropic", "claude-sonnet-4", 200000},
		{"anthropic", "claude-opus-4", 200000},
		{"anthropic", "claude-haiku-4", 200000},
		{"anthropic", "claude-3-5-sonnet", 200000},
		{"anthropic", "claude-3-5-haiku", 200000},
		{"anthropic", "claude-3-opus", 200000},
		{"anthropic", "claude-3-sonnet", 200000},
		{"anthropic", "claude-3-haiku", 200000},

		// OpenAI.
		{"openai", "gpt-4o", 128000},
		{"openai", "gpt-4o-mini", 128000},
		{"openai", "gpt-4-turbo", 128000},
		{"openai", "o1", 200000},
		{"openai", "o1-mini", 128000},
		{"openai", "o3", 200000},
		{"openai", "o3-mini", 200000},
		{"openai", "gpt-4.1", 1000000},
		{"openai", "gpt-4.1-mini", 1000000},

		// Bedrock.
		{"bedrock", "anthropic.claude-sonnet-4-5", 200000},
		{"bedrock", "anthropic.claude-3-5-sonnet", 200000},

		// OpenRouter.
		{"openrouter", "anthropic/claude-sonnet-4-5", 200000},
		{"openrouter", "openai/gpt-4o", 128000},
	}

	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			got, ok := lookup.MaxContextTokens(compaction.ProviderProfileRef{
				ProviderID: tc.provider,
				ModelID:    tc.model,
			})
			if !ok {
				t.Fatalf("MaxContextTokens(%s, %s): ok=false, want (%d, true)", tc.provider, tc.model, tc.want)
			}
			if got != tc.want {
				t.Errorf("MaxContextTokens(%s, %s) = %d, want %d", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}

// TestCapabilityLookup_OverrideTableWinsOverCatalog confirms the
// operator-supplied override table (SetTable / WithCustomTable) still
// takes precedence over the shared YAML catalog fallback — the
// override mechanism is a deliberate escape hatch, not the duplicate
// this mission removed.
func TestCapabilityLookup_OverrideTableWinsOverCatalog(t *testing.T) {
	lookup := NewCapabilityLookup()
	lookup.SetTable("openai", "gpt-4o", 999)

	got, ok := lookup.MaxContextTokens(compaction.ProviderProfileRef{
		ProviderID: "openai",
		ModelID:    "gpt-4o",
	})
	if !ok || got != 999 {
		t.Fatalf("MaxContextTokens after SetTable override = (%d, %v), want (999, true)", got, ok)
	}
}
