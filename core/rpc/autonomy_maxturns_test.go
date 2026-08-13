package rpc

// autonomy-knobs-live-01PMAG02 WP01 — maxIterations reconciliation.
//
// Before this WP, autonomy.ResolvedKnobs.MaxIterations was resolved
// through the three-layer chain and read by nothing; Settings.
// EffectiveMaxAgentTurns bypassed resolution entirely and was the only
// dial that actually bound the LoopNode's max_iterations. These tests
// pin resolveAutonomyKnobsWithSettingsFallback — the pure function that
// reconciles the two — directly, without standing up a live core.Core /
// session.Manager / settings store.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// FR-005: a user who never touched the autonomy panel (no project or
// session override anywhere) must see the identical effective cap
// after this WP as before it — the legacy Settings value.
func TestResolveAutonomyKnobsWithSettingsFallback_NoOverrides_MatchesLegacySetting(t *testing.T) {
	t.Parallel()

	var global, project, session autonomy.Layer
	got := resolveAutonomyKnobsWithSettingsFallback(global, project, session, 37)

	if got.MaxIterations != 37 {
		t.Fatalf("MaxIterations = %d, want 37 (the legacy Settings.EffectiveMaxAgentTurns value, unchanged by this mission)", got.MaxIterations)
	}
	if got.SourceTrace[autonomy.KnobMaxIterations] != autonomy.SourceGlobal {
		t.Errorf("SourceTrace[MaxIterations] = %v, want SourceGlobal (fed from the legacy setting)", got.SourceTrace[autonomy.KnobMaxIterations])
	}
}

// A session-layer explicit override wins over the legacy Settings
// value — this is "the per-project and per-session dials real" half of
// the reconciliation (spec §3.2).
func TestResolveAutonomyKnobsWithSettingsFallback_SessionOverrideWins(t *testing.T) {
	t.Parallel()

	var global, project autonomy.Layer
	session := autonomy.Layer{
		Overrides: map[autonomy.Knob]any{autonomy.KnobMaxIterations: 7},
	}
	got := resolveAutonomyKnobsWithSettingsFallback(global, project, session, 37)

	if got.MaxIterations != 7 {
		t.Fatalf("MaxIterations = %d, want 7 (the session override), legacy setting must not win", got.MaxIterations)
	}
	if got.SourceTrace[autonomy.KnobMaxIterations] != autonomy.SourceSession {
		t.Errorf("SourceTrace[MaxIterations] = %v, want SourceSession", got.SourceTrace[autonomy.KnobMaxIterations])
	}
}

// A project-layer explicit override wins over the legacy Settings
// value when no session override is present.
func TestResolveAutonomyKnobsWithSettingsFallback_ProjectOverrideWins(t *testing.T) {
	t.Parallel()

	var global, session autonomy.Layer
	project := autonomy.Layer{
		Overrides: map[autonomy.Knob]any{autonomy.KnobMaxIterations: 12},
	}
	got := resolveAutonomyKnobsWithSettingsFallback(global, project, session, 37)

	if got.MaxIterations != 12 {
		t.Fatalf("MaxIterations = %d, want 12 (the project override)", got.MaxIterations)
	}
	if got.SourceTrace[autonomy.KnobMaxIterations] != autonomy.SourceProject {
		t.Errorf("SourceTrace[MaxIterations] = %v, want SourceProject", got.SourceTrace[autonomy.KnobMaxIterations])
	}
}

// An explicit global-layer override (set through the autonomy panel,
// independent of the legacy numeric Settings field) wins over the
// legacy Settings value — the more specific control wins, and the
// legacy fallback must not clobber it.
func TestResolveAutonomyKnobsWithSettingsFallback_ExistingGlobalOverrideNotClobbered(t *testing.T) {
	t.Parallel()

	global := autonomy.Layer{
		Overrides: map[autonomy.Knob]any{autonomy.KnobMaxIterations: 99},
	}
	var project, session autonomy.Layer
	got := resolveAutonomyKnobsWithSettingsFallback(global, project, session, 37)

	if got.MaxIterations != 99 {
		t.Fatalf("MaxIterations = %d, want 99 (the explicit global-panel override); the legacy setting=37 must not have clobbered it", got.MaxIterations)
	}
}

// A nil global.Overrides map must not panic — the common case for a
// session whose global layer was never persisted.
func TestResolveAutonomyKnobsWithSettingsFallback_NilGlobalOverridesMap(t *testing.T) {
	t.Parallel()

	global := autonomy.Layer{Overrides: nil}
	var project, session autonomy.Layer
	got := resolveAutonomyKnobsWithSettingsFallback(global, project, session, 10)
	if got.MaxIterations != 10 {
		t.Fatalf("MaxIterations = %d, want 10", got.MaxIterations)
	}
}
