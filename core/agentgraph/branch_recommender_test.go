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

func TestRecommender_TierFromModelID_Heuristic(t *testing.T) {
	t.Parallel()
	if tierFromModelID("claude-haiku-4") != ModelTierSmall {
		t.Error("haiku should be small")
	}
	if tierFromModelID("gpt-4o-mini") != ModelTierSmall {
		t.Error("mini should be small")
	}
	if tierFromModelID("claude-opus-4") != ModelTierLarge {
		t.Error("opus should be large")
	}
	if tierFromModelID("o1-preview") != ModelTierLarge {
		t.Error("o1 should be large")
	}
	if tierFromModelID("claude-sonnet-4") != ModelTierMedium {
		t.Error("sonnet should be medium (default)")
	}
}
