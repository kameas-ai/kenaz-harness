package rpc

// permission-mode-wiring (owner ruling, 2026-09-16) — Settings.PermissionMode
// was a mounted dial with zero behavior anywhere in core/ (RegisterDeferred
// in settings_knob_coverage.go, EffectivePermissionMode()'s only callers
// were the two store Load accessors feeding the Settings_GetPermissionMode
// binding). This mission wires it onto the now-live per-call Cedar/risk
// gate (risk-rated-autonomy-01PMRA01) rather than deleting it.
//
// These tests pin the pure mapping/folding functions
// (permissionModeRiskThreshold, foldPermissionModeIntoGlobal) plus the
// live-settings-store integration through computeAutonomyKnobs. The
// downstream half of the chain — RiskThreshold actually changing a
// dispatch OUTCOME through the real cedar.ThreeLayerResolve path — is
// covered in
// core/rpc/views/agentgraph/chat/permission_mode_wiring_test.go, which
// this file's own assertions link to via the exact threshold values
// (0 / 40 / 80) so the two files provably describe the same chain.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// TestPermissionModeRiskThreshold_Mapping pins the exact numeric
// mapping documented on Settings.PermissionMode and
// permissionModeRiskThreshold: strict=0, normal=40, permissive=80, and
// every unrecognised/empty value is a deliberate no-op (ok=false) so a
// fresh install with nothing persisted never folds a value in at all.
func TestPermissionModeRiskThreshold_Mapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode      string
		wantValue int
		wantOK    bool
	}{
		{"strict", 0, true},
		{"normal", 40, true},
		{"permissive", 80, true},
		{"", 0, false},
		{"bogus", 0, false},
	}
	for _, tc := range tests {
		got, ok := permissionModeRiskThreshold(tc.mode)
		if ok != tc.wantOK || (ok && got != tc.wantValue) {
			t.Errorf("permissionModeRiskThreshold(%q) = (%d, %v), want (%d, %v)",
				tc.mode, got, ok, tc.wantValue, tc.wantOK)
		}
	}

	// The "permissive" ceiling must never exceed autonomy.TierAutonomous's
	// own RiskThreshold preset (80) — scripts/ci/check-risk-gate-decides.sh
	// already enforces that the family floor (81) sits strictly above
	// THAT tier's threshold, and permissionModeRiskThreshold must stay
	// inside the envelope that gate already validated rather than
	// introducing a second, unchecked ceiling.
	permissive, ok := permissionModeRiskThreshold("permissive")
	if !ok {
		t.Fatal(`permissionModeRiskThreshold("permissive") returned ok=false`)
	}
	autonomousTierThreshold := autonomy.PresetForTier(autonomy.TierAutonomous)[autonomy.KnobRiskThreshold].(int)
	if permissive > autonomousTierThreshold {
		t.Fatalf(`permissionModeRiskThreshold("permissive") = %d, exceeds autonomy.TierAutonomous's RiskThreshold preset (%d) — `+
			"this would move PermissionMode's most permissive value outside the envelope "+
			"scripts/ci/check-risk-gate-decides.sh's family-floor check validates",
			permissive, autonomousTierThreshold)
	}
}

// TestFoldPermissionModeIntoGlobal_WritesWhenNoOverride pins the
// primary case: a global layer with no explicit RiskThreshold override
// gets one written from the PermissionMode preset.
func TestFoldPermissionModeIntoGlobal_WritesWhenNoOverride(t *testing.T) {
	t.Parallel()

	got := foldPermissionModeIntoGlobal(autonomy.Layer{}, "strict")
	v, ok := got.Overrides[autonomy.KnobRiskThreshold]
	if !ok {
		t.Fatal("foldPermissionModeIntoGlobal did not write a RiskThreshold override")
	}
	if v != 0 {
		t.Fatalf("RiskThreshold override = %v, want 0 (strict)", v)
	}
}

// TestFoldPermissionModeIntoGlobal_MoreSpecificOverrideWins pins the
// documented precedence rule: an explicit RiskThreshold override
// already present on the global layer (set via the Autonomy Dials
// panel — a more specific, per-knob control) must NOT be clobbered by
// PermissionMode's coarse preset.
func TestFoldPermissionModeIntoGlobal_MoreSpecificOverrideWins(t *testing.T) {
	t.Parallel()

	global := autonomy.Layer{Overrides: map[autonomy.Knob]any{autonomy.KnobRiskThreshold: 55}}
	got := foldPermissionModeIntoGlobal(global, "strict") // strict would write 0
	if v := got.Overrides[autonomy.KnobRiskThreshold]; v != 55 {
		t.Fatalf("RiskThreshold override = %v, want 55 (the pre-existing, more specific override) — "+
			"PermissionMode must not clobber an explicit autonomy-panel override", v)
	}
}

// TestFoldPermissionModeIntoGlobal_EmptyOrUnknownIsNoOp pins that an
// unset/garbage PermissionMode value never writes an override at all —
// distinct from writing "normal"'s 40, which would silently downgrade
// any pre-existing project/session RiskThreshold override on a fresh
// install that predates this wiring.
func TestFoldPermissionModeIntoGlobal_EmptyOrUnknownIsNoOp(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"", "bogus"} {
		got := foldPermissionModeIntoGlobal(autonomy.Layer{}, mode)
		if got.Overrides != nil {
			t.Errorf("foldPermissionModeIntoGlobal(mode=%q) wrote overrides %v, want nil (no-op)", mode, got.Overrides)
		}
	}
}

// TestComputeAutonomyKnobs_PermissionModeSetsRiskThreshold is the
// integration proof: persisting Settings.PermissionMode through a real
// settings.FileStore and calling computeAutonomyKnobs (the exact
// production call path — core/rpc/api.go:6163) must produce the mapped
// RiskThreshold, sourced from the global layer.
func TestComputeAutonomyKnobs_PermissionModeSetsRiskThreshold(t *testing.T) {
	// NOTE: t.Parallel() omitted — sandboxUserConfigDir uses t.Setenv,
	// incompatible with parallel subtests (see
	// TestComputeAutonomyKnobs_SessionScoped_NotGlobalOnly for the same
	// note on the sibling test this one is modeled on).
	sandboxUserConfigDir(t)

	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store := newTestStore(t)
	api := New(c, WithSettingsStore(store))
	t.Cleanup(api.Shutdown)

	ctx := context.Background()

	for _, tc := range []struct {
		mode          string
		wantThreshold int
	}{
		{"strict", 0},
		{"normal", 40},
		{"permissive", 80},
	} {
		if err := store.SavePermissionMode(tc.mode); err != nil {
			t.Fatalf("SavePermissionMode(%q): %v", tc.mode, err)
		}
		got := computeAutonomyKnobs(ctx, "", api.core, api.settingsImpl)
		if got.RiskThreshold != tc.wantThreshold {
			t.Fatalf("PermissionMode=%q: computeAutonomyKnobs().RiskThreshold = %d, want %d",
				tc.mode, got.RiskThreshold, tc.wantThreshold)
		}
		if src := got.SourceTrace[autonomy.KnobRiskThreshold]; src != autonomy.SourceGlobal {
			t.Errorf("PermissionMode=%q: SourceTrace[RiskThreshold] = %v, want SourceGlobal", tc.mode, src)
		}
	}
}

// TestComputeAutonomyKnobs_ExplicitAutonomyOverrideBeatsPermissionMode
// proves the precedence rule survives the FULL computeAutonomyKnobs
// path, not just the pure foldPermissionModeIntoGlobal helper: setting
// PermissionMode="strict" (which would fold in RiskThreshold=0) must
// not clobber an explicit global RiskThreshold override set through
// SaveAutonomyProfile (the Autonomy Dials panel's own write path).
func TestComputeAutonomyKnobs_ExplicitAutonomyOverrideBeatsPermissionMode(t *testing.T) {
	sandboxUserConfigDir(t)

	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	store := newTestStore(t)
	api := New(c, WithSettingsStore(store))
	t.Cleanup(api.Shutdown)

	ctx := context.Background()

	if err := store.SavePermissionMode("strict"); err != nil {
		t.Fatalf("SavePermissionMode: %v", err)
	}
	if err := store.SaveAutonomyProfile(autonomy.Layer{
		Overrides: map[autonomy.Knob]any{autonomy.KnobRiskThreshold: 65},
	}); err != nil {
		t.Fatalf("SaveAutonomyProfile: %v", err)
	}

	got := computeAutonomyKnobs(ctx, "", api.core, api.settingsImpl)
	if got.RiskThreshold != 65 {
		t.Fatalf("RiskThreshold = %d, want 65 (the explicit Autonomy Dials panel override) — "+
			"PermissionMode=\"strict\" must not clobber a more specific global-layer override",
			got.RiskThreshold)
	}
}
