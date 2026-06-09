package sessions

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// TestResolveAutonomy_InheritanceFlow is the WP08 acceptance smoke for
// the autonomy-dial-01KR3M2A mission's three-layer override chain.
//
// Walks the canonical scenario from plan §Resolution semantics:
//
//  1. Empty layers everywhere → resolves to TierDefault preset.
//  2. Set global=Default → resolves to TierDefault from "global".
//  3. Add project=Bold → MaxIterations switches to "project" (Bold).
//  4. Add session.Overrides[MaxIterations]=1 → MaxIterations resolves
//     to 1 from "session" (override beats Level).
//  5. Clear session → falls back to project-level Bold preset.
//  6. Set global.Overrides[TokenCeilingPerTurn]=42 → TokenCeiling
//     resolves to 42 from "global" even with project=Bold (the
//     two-pass walk: Overrides first across all layers, then Levels).
func TestResolveAutonomy_InheritanceFlow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	// Build a mutable global + project layer the AutonomyContextProvider
	// can serve. The provider's closures read from these vars by
	// reference so successive panel writes show up on the next Resolve.
	globalLayer := autonomy.Layer{}
	projectLayer := autonomy.Layer{}
	api = WithAutonomyContext(api, AutonomyContextFunc{
		Global: func(_ context.Context) (autonomy.Layer, error) {
			return globalLayer, nil
		},
		ProjectForSession: func(_ context.Context, _ string) (autonomy.Layer, error) {
			return projectLayer, nil
		},
	})

	sess, err := api.Create(ctx, "test-session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// (1) Empty layers — resolves to TierDefault preset.
	r, err := api.ResolveAutonomy(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ResolveAutonomy: %v", err)
	}
	defaultMaxIters := r.Resolved.MaxIterations
	if defaultMaxIters <= 0 {
		t.Errorf("expected TierDefault MaxIterations > 0, got %d", defaultMaxIters)
	}
	if r.Resolved.SourceTrace["maxIterations"] != "tier-default" {
		t.Errorf("step 1: source = %q, want tier-default",
			r.Resolved.SourceTrace["maxIterations"])
	}
	if r.Resolved.Tier != "default" {
		t.Errorf("step 1 tier = %q, want default", r.Resolved.Tier)
	}

	// (2) Global=Default — source switches to "global".
	defTier := autonomy.TierDefault
	globalLayer = autonomy.Layer{Level: &defTier}
	r, _ = api.ResolveAutonomy(ctx, sess.ID)
	if r.Resolved.SourceTrace["maxIterations"] != "global" {
		t.Errorf("step 2: source = %q, want global",
			r.Resolved.SourceTrace["maxIterations"])
	}

	// (3) Add project=Bold — MaxIterations now traces to "project".
	boldTier := autonomy.TierBold
	projectLayer = autonomy.Layer{Level: &boldTier}
	r, _ = api.ResolveAutonomy(ctx, sess.ID)
	if r.Resolved.SourceTrace["maxIterations"] != "project" {
		t.Errorf("step 3: source = %q, want project",
			r.Resolved.SourceTrace["maxIterations"])
	}
	boldMaxIters := r.Resolved.MaxIterations
	if boldMaxIters <= defaultMaxIters {
		t.Errorf("step 3: Bold MaxIterations %d should exceed Default %d",
			boldMaxIters, defaultMaxIters)
	}

	// (4) Set session.Overrides[MaxIterations]=1 — wins via "session".
	if err := api.SaveAutonomyProfile(ctx, sess.ID, autonomy.Layer{
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobMaxIterations: int(1),
		},
	}); err != nil {
		t.Fatalf("SaveAutonomyProfile: %v", err)
	}
	r, _ = api.ResolveAutonomy(ctx, sess.ID)
	if r.Resolved.MaxIterations != 1 {
		t.Errorf("step 4: MaxIterations = %d, want 1", r.Resolved.MaxIterations)
	}
	if r.Resolved.SourceTrace["maxIterations"] != "session" {
		t.Errorf("step 4: source = %q, want session",
			r.Resolved.SourceTrace["maxIterations"])
	}

	// (5) Clear session — falls back to project Bold preset.
	if err := api.SaveAutonomyProfile(ctx, sess.ID, autonomy.Layer{}); err != nil {
		t.Fatalf("SaveAutonomyProfile (clear): %v", err)
	}
	r, _ = api.ResolveAutonomy(ctx, sess.ID)
	if r.Resolved.MaxIterations != boldMaxIters {
		t.Errorf("step 5: MaxIterations = %d, want %d (Bold preset)",
			r.Resolved.MaxIterations, boldMaxIters)
	}
	if r.Resolved.SourceTrace["maxIterations"] != "project" {
		t.Errorf("step 5: source = %q, want project",
			r.Resolved.SourceTrace["maxIterations"])
	}

	// (6) Add global.Overrides[TokenCeilingPerTurn]=42 — overrides win
	// across all layers BEFORE Levels (two-pass invariant).
	globalLayer = autonomy.Layer{
		Level: &defTier,
		Overrides: map[autonomy.Knob]any{
			autonomy.KnobTokenCeilingPerTurn: int(42),
		},
	}
	r, _ = api.ResolveAutonomy(ctx, sess.ID)
	if r.Resolved.TokenCeilingPerTurn != 42 {
		t.Errorf("step 6: TokenCeiling = %d, want 42 (global override)",
			r.Resolved.TokenCeilingPerTurn)
	}
	if r.Resolved.SourceTrace["tokenCeilingPerTurn"] != "global" {
		t.Errorf("step 6: source = %q, want global",
			r.Resolved.SourceTrace["tokenCeilingPerTurn"])
	}
	// MaxIterations stays at project Bold (project Level still wins
	// over global Level for non-overridden knobs).
	if r.Resolved.SourceTrace["maxIterations"] != "project" {
		t.Errorf("step 6: maxIterations source = %q, want project (project Level still wins)",
			r.Resolved.SourceTrace["maxIterations"])
	}
}

// TestResolveAutonomy_NoContextProvider pins that ResolveAutonomy
// degrades gracefully when no AutonomyContextProvider has been wired —
// the session-level layer + tier-default chain still resolves and the
// frontend chip can render a usable label.
func TestResolveAutonomy_NoContextProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)

	sess, err := api.Create(ctx, "loose")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	r, err := api.ResolveAutonomy(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ResolveAutonomy: %v", err)
	}
	if r.Resolved.SourceTrace["maxIterations"] != "tier-default" {
		t.Errorf("source = %q, want tier-default",
			r.Resolved.SourceTrace["maxIterations"])
	}
	if r.Resolved.Tier != "default" {
		t.Errorf("tier = %q, want default", r.Resolved.Tier)
	}
	if !r.Global.IsZero() || !r.Project.IsZero() {
		t.Errorf("expected Global+Project IsZero, got Global=%+v Project=%+v",
			r.Global, r.Project)
	}
}
