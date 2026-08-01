package agentgraph

import "testing"

// Reuses fakeTierSource from branch_recommender_test.go rather than
// declaring a second one — same seam, same shape.

// fakeActiveModelProvider is an LLMProvider that also advertises the
// concrete model a run would dispatch to, exercising the optional
// ActiveModelSource seam.
type fakeActiveModelProvider struct {
	LLMProvider
	kind  string
	model string
}

func (f *fakeActiveModelProvider) ProviderKind() string  { return f.kind }
func (f *fakeActiveModelProvider) ActiveModelID() string { return f.model }

func TestVerbosityForTier(t *testing.T) {
	for _, tc := range []struct {
		tier ModelTier
		want string
	}{
		{ModelTierSmall, "terse"},
		{ModelTierMedium, "standard"},
		{ModelTierLarge, "verbose"},
		{ModelTier("unknown-future-tier"), "standard"},
	} {
		if got := verbosityForTier(tc.tier); got != tc.want {
			t.Errorf("verbosityForTier(%q) = %q, want %q", tc.tier, got, tc.want)
		}
	}
}

func TestMaxIterationsForTier(t *testing.T) {
	for _, tc := range []struct {
		tier ModelTier
		want int
	}{
		{ModelTierSmall, 5},
		{ModelTierMedium, 3},
		{ModelTierLarge, 2},
		{ModelTier("unknown-future-tier"), 3},
	} {
		if got := maxIterationsForTier(tc.tier); got != tc.want {
			t.Errorf("maxIterationsForTier(%q) = %d, want %d", tc.tier, got, tc.want)
		}
	}
}

// TestResolveNodeTier_FallsBackToMediumWithoutData pins the
// no-behaviour-change guarantee: every path that lacks tier data must
// resolve to ModelTierMedium, whose derived defaults ("standard", 3) are
// exactly the values the executors hardcoded before WP05. A regression
// here would silently re-tier every existing graph.
func TestResolveNodeTier_FallsBackToMediumWithoutData(t *testing.T) {
	withSource := &Env{TierSource: &fakeTierSource{table: map[string]ModelTier{}}}

	for _, tc := range []struct {
		name          string
		env           *Env
		provider, mdl string
	}{
		{"nil env", nil, "anthropic", "claude-x"},
		{"nil TierSource", &Env{}, "anthropic", "claude-x"},
		{"empty model, no ActiveModelSource", withSource, "anthropic", ""},
		{"\"default\" sentinel, no ActiveModelSource", withSource, "anthropic", "default"},
		{"TierSource has no opinion", withSource, "anthropic", "unknown-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveNodeTier(tc.env, tc.provider, tc.mdl); got != ModelTierMedium {
				t.Fatalf("resolveNodeTier = %q, want %q (pre-WP05 behaviour)", got, ModelTierMedium)
			}
		})
	}
}

func TestResolveNodeTier_UsesExplicitNodeModel(t *testing.T) {
	env := &Env{TierSource: &fakeTierSource{table: map[string]ModelTier{
		"anthropic/big-model": ModelTierLarge,
	}}}
	if got := resolveNodeTier(env, "anthropic", "big-model"); got != ModelTierLarge {
		t.Fatalf("resolveNodeTier = %q, want %q", got, ModelTierLarge)
	}
}

// TestResolveNodeTier_FallsBackToActiveModelSource covers the case the
// shipped graphs actually hit: they hardcode model "default", so the tier
// must come from whatever model the provider will really dispatch to.
func TestResolveNodeTier_FallsBackToActiveModelSource(t *testing.T) {
	env := &Env{
		TierSource: &fakeTierSource{table: map[string]ModelTier{
			"openai/small-model": ModelTierSmall,
		}},
		LLM: &fakeActiveModelProvider{kind: "openai", model: "small-model"},
	}
	for _, nodeModel := range []string{"", "default"} {
		if got := resolveNodeTier(env, "", nodeModel); got != ModelTierSmall {
			t.Fatalf("resolveNodeTier(model=%q) = %q, want %q", nodeModel, got, ModelTierSmall)
		}
	}
}

// TestResolveNodeTier_ExplicitModelBeatsActiveModelSource pins that a
// node author's explicit model attr is authoritative — the run's default
// model must not override it.
func TestResolveNodeTier_ExplicitModelBeatsActiveModelSource(t *testing.T) {
	env := &Env{
		TierSource: &fakeTierSource{table: map[string]ModelTier{
			"anthropic/authored": ModelTierLarge,
			"openai/ambient":     ModelTierSmall,
		}},
		LLM: &fakeActiveModelProvider{kind: "openai", model: "ambient"},
	}
	if got := resolveNodeTier(env, "anthropic", "authored"); got != ModelTierLarge {
		t.Fatalf("resolveNodeTier = %q, want %q (explicit node model must win)", got, ModelTierLarge)
	}
}
