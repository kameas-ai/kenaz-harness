package agentgraph

import "testing"

func TestRecommender_ContainedTask_StepsDown(t *testing.T) {
	t.Parallel()
	rec := NewBranchRecommender([]ModelInfo{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4", Tier: ModelTierMedium},
		{ProviderID: "anthropic", ModelID: "claude-haiku-4", Tier: ModelTierSmall},
		{ProviderID: "anthropic", ModelID: "claude-opus-4", Tier: ModelTierLarge},
	})
	got := rec.Recommend("anthropic", "claude-sonnet-4",
		"what's the latest version of dep X", "")
	if got.Tier != ModelTierSmall {
		t.Errorf("tier = %q, want small", got.Tier)
	}
	if got.Reason != ReasonContainedTask {
		t.Errorf("reason = %q, want contained_task", got.Reason)
	}
}

func TestRecommender_DeepDesign_StepsUp(t *testing.T) {
	t.Parallel()
	rec := NewBranchRecommender([]ModelInfo{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4", Tier: ModelTierMedium},
		{ProviderID: "anthropic", ModelID: "claude-opus-4", Tier: ModelTierLarge},
	})
	got := rec.Recommend("anthropic", "claude-sonnet-4",
		"deep dive on the architecture trade-offs", "")
	if got.Tier != ModelTierLarge {
		t.Errorf("tier = %q, want large", got.Tier)
	}
	if got.Reason != ReasonDeepDesign {
		t.Errorf("reason = %q, want deep_design", got.Reason)
	}
}

func TestRecommender_UserOverride(t *testing.T) {
	t.Parallel()
	rec := NewBranchRecommender([]ModelInfo{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4", Tier: ModelTierMedium},
		{ProviderID: "anthropic", ModelID: "claude-haiku-4", Tier: ModelTierSmall},
	})
	got := rec.Recommend("anthropic", "claude-sonnet-4",
		"deep dive on architecture", ForkPrefSmaller)
	if got.Tier != ModelTierSmall {
		t.Errorf("tier = %q, want small (user override beats heuristic)", got.Tier)
	}
	if got.Reason != ReasonUserOverride {
		t.Errorf("reason = %q, want user_override", got.Reason)
	}
}

func TestRecommender_FallbackToParentWhenNoCandidate(t *testing.T) {
	t.Parallel()
	rec := NewBranchRecommender([]ModelInfo{
		{ProviderID: "anthropic", ModelID: "claude-sonnet-4", Tier: ModelTierMedium},
	})
	got := rec.Recommend("anthropic", "claude-sonnet-4",
		"what's the latest version", "")
	// No "small" tier configured; should fall back to parent.
	if got.ProviderID != "anthropic" || got.ModelID != "claude-sonnet-4" {
		t.Errorf("expected fallback to parent, got %+v", got)
	}
	if got.Reason != ReasonDefault {
		t.Errorf("reason = %q, want default", got.Reason)
	}
}

// fakeTierSource is a minimal TierSource stand-in so this package's tests
// don't need to import core/llm/capabilities (which would risk an import
// cycle back into agentgraph — charter DIRECTIVE_001). It maps a small
// fixed set of (providerKind, modelID) pairs, mirroring the shape of the
// real capabilities.Catalog.Tier data without duplicating it.
type fakeTierSource struct {
	table map[string]ModelTier
}

func (f *fakeTierSource) Tier(providerKind, modelID string) (ModelTier, bool) {
	t, ok := f.table[providerKind+"/"+modelID]
	return t, ok
}

// TestRecommender_TierSource_UnlistedModel verifies tier resolution for a
// (provider, model) pair absent from the recommender's directly-configured
// ModelInfo table now comes from the injected TierSource (versioned-
// model-profile-01PMDL04 WP04) rather than a core string-matching
// heuristic on the model id — that heuristic (tierFromModelID) was the
// frozen-core violation this WP closes; the equivalent data now lives in
// core/llm/capabilities/data/*.yaml (see capabilities_test.go for
// coverage that the real Catalog resolves the same tiers this fake
// stands in for).
func TestRecommender_TierSource_UnlistedModel(t *testing.T) {
	t.Parallel()
	ts := &fakeTierSource{table: map[string]ModelTier{
		"anthropic/claude-haiku-4":  ModelTierSmall,
		"openai/gpt-4o-mini":        ModelTierSmall,
		"anthropic/claude-opus-4":   ModelTierLarge,
		"openai/o1-preview":         ModelTierLarge,
		"anthropic/claude-sonnet-4": ModelTierMedium,
	}}
	rec := NewBranchRecommenderWithTierSource(nil, ts)

	got := rec.Recommend("anthropic", "claude-haiku-4", "", ForkPrefSame)
	if got.Tier != ModelTierSmall {
		t.Errorf("claude-haiku-4 tier = %q, want small", got.Tier)
	}
	got = rec.Recommend("openai", "gpt-4o-mini", "", ForkPrefSame)
	if got.Tier != ModelTierSmall {
		t.Errorf("gpt-4o-mini tier = %q, want small", got.Tier)
	}
	got = rec.Recommend("anthropic", "claude-opus-4", "", ForkPrefSame)
	if got.Tier != ModelTierLarge {
		t.Errorf("claude-opus-4 tier = %q, want large", got.Tier)
	}
	got = rec.Recommend("openai", "o1-preview", "", ForkPrefSame)
	if got.Tier != ModelTierLarge {
		t.Errorf("o1-preview tier = %q, want large", got.Tier)
	}
	got = rec.Recommend("anthropic", "claude-sonnet-4", "", ForkPrefSame)
	if got.Tier != ModelTierMedium {
		t.Errorf("claude-sonnet-4 tier = %q, want medium", got.Tier)
	}
}

// TestRecommender_TierSource_NoOpinion_DefaultsMedium verifies that when
// neither the ModelInfo table nor the TierSource has an opinion, the
// recommender falls back to ModelTierMedium (tierOf's documented final
// default) instead of erroring or guessing from the model id.
func TestRecommender_TierSource_NoOpinion_DefaultsMedium(t *testing.T) {
	t.Parallel()
	ts := &fakeTierSource{table: map[string]ModelTier{}}
	rec := NewBranchRecommenderWithTierSource(nil, ts)
	got := rec.Recommend("someprovider", "some-unknown-model", "", ForkPrefSame)
	if got.Tier != ModelTierMedium {
		t.Errorf("tier = %q, want medium default", got.Tier)
	}
}

// TestRecommender_NilTierSource_DefaultsMedium mirrors the above for a
// recommender built via the plain NewBranchRecommender constructor (no
// TierSource at all — nil is a supported, non-panicking configuration).
func TestRecommender_NilTierSource_DefaultsMedium(t *testing.T) {
	t.Parallel()
	rec := NewBranchRecommender(nil)
	got := rec.Recommend("anthropic", "claude-haiku-4", "", ForkPrefSame)
	if got.Tier != ModelTierMedium {
		t.Errorf("tier = %q, want medium default (no tier source configured)", got.Tier)
	}
}
