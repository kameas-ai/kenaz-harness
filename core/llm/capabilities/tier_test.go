package capabilities

import "testing"

// TestCatalog_Tier verifies the provider-owned tiers: table resolves the
// same tier classifications the old core/agentgraph tierFromModelID
// name-matching heuristic produced, now sourced from data instead of
// core string-matching (versioned-model-profile-01PMDL04 WP04).
func TestCatalog_Tier(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	cases := []struct {
		provider string
		model    string
		want     string
		wantOK   bool
	}{
		{"anthropic", "claude-haiku-4", "small", true},
		{"anthropic", "claude-sonnet-4", "medium", true},
		{"anthropic", "claude-opus-4", "large", true},
		// Glob rows classify sibling revisions of a named family.
		{"anthropic", "claude-3-5-haiku-20241022", "small", true},
		{"anthropic", "claude-3-5-sonnet-20240620", "medium", true},
		{"anthropic", "claude-3-opus-20240229", "large", true},
		{"openai", "gpt-4o-mini", "small", true},
		{"openai", "gpt-4o", "medium", true},
		{"openai", "o1-preview", "large", true},
		{"openai", "gpt-3.5-turbo", "small", true},
		// No tiers: row and no provider default → provider default applies.
		{"gemini", "gemini-2.5-pro", "medium", true},
		// Unknown provider — no data at all.
		{"nonexistent-provider", "some-model", "", false},
	}
	for _, tc := range cases {
		got, ok := c.Tier(tc.provider, tc.model)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("Tier(%q, %q) = (%q, %v), want (%q, %v)", tc.provider, tc.model, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestCatalog_TierAny verifies cross-provider tier resolution for a
// caller that only has a model id, not a provider (e.g. a "personal"
// or custom provider kind naming a model after a known family).
func TestCatalog_TierAny(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	if got, ok := c.TierAny("claude-opus-4"); !ok || got != "large" {
		t.Errorf("TierAny(claude-opus-4) = (%q, %v), want (large, true)", got, ok)
	}
	if got, ok := c.TierAny("gpt-4o-mini"); !ok || got != "small" {
		t.Errorf("TierAny(gpt-4o-mini) = (%q, %v), want (small, true)", got, ok)
	}
	// No provider's tiers: table has a glob or exact match for this —
	// TierAny does not consult provider-level defaults, unlike Tier.
	if _, ok := c.TierAny("totally-unrecognised-model-xyz"); ok {
		t.Errorf("TierAny(totally-unrecognised-model-xyz) unexpectedly resolved")
	}
}

// TestCatalog_KnownModels verifies exact-match (non-wildcard) rows are
// enumerable for the v1 branch-recommender stopgap wiring
// (core/rpc/branches_wiring.go), and that glob rows are excluded.
func TestCatalog_KnownModels(t *testing.T) {
	t.Parallel()
	c := mustCatalog(t)
	got := c.KnownModels("anthropic")
	want := map[string]string{
		"claude-haiku-4":  "small",
		"claude-sonnet-4": "medium",
		"claude-opus-4":   "large",
	}
	if len(got) != len(want) {
		t.Fatalf("KnownModels(anthropic) len = %d, want %d: %+v", len(got), len(want), got)
	}
	for _, km := range got {
		wantTier, ok := want[km.ModelID]
		if !ok {
			t.Errorf("unexpected known model %q", km.ModelID)
			continue
		}
		if km.Tier != wantTier {
			t.Errorf("KnownModels(anthropic)[%q].Tier = %q, want %q", km.ModelID, km.Tier, wantTier)
		}
		if got, want := km.ModelID, km.ModelID; got != want {
			t.Errorf("ModelID mismatch: %q", km.ModelID)
		}
	}
	for _, km := range got {
		if containsStar(km.ModelID) {
			t.Errorf("KnownModels returned a glob row: %q", km.ModelID)
		}
	}
	// Unknown provider — nil, not a panic.
	if got := c.KnownModels("nonexistent-provider"); got != nil {
		t.Errorf("KnownModels(nonexistent-provider) = %+v, want nil", got)
	}
}

func containsStar(s string) bool {
	for _, r := range s {
		if r == '*' {
			return true
		}
	}
	return false
}
