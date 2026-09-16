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
// primary case: no layer has an explicit RiskThreshold override or a
// Level set at all, so PermissionMode's preset gets written onto the
// global layer.
func TestFoldPermissionModeIntoGlobal_WritesWhenNoOverride(t *testing.T) {
	t.Parallel()

	got := foldPermissionModeIntoGlobal(autonomy.Layer{}, autonomy.Layer{}, autonomy.Layer{}, "strict")
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
	got := foldPermissionModeIntoGlobal(global, autonomy.Layer{}, autonomy.Layer{}, "strict") // strict would write 0
	if v := got.Overrides[autonomy.KnobRiskThreshold]; v != 55 {
		t.Fatalf("RiskThreshold override = %v, want 55 (the pre-existing, more specific override) — "+
			"PermissionMode must not clobber an explicit autonomy-panel override", v)
	}
}

// TestFoldPermissionModeIntoGlobal_GlobalLevelWins is the reviewer's
// confirmed reproduction of the blocking defect: AutonomyPanel.vue's
// primary interaction (setTier(), frontend/src/views/settings/
// AutonomyPanel.vue:172-181) persists a bare {Level: t, Overrides: {}}
// — an explicit tier CHOICE with no per-knob override at all. The
// pre-fix guard only ever inspected global.Overrides, so it never saw
// this choice and clobbered it with PermissionMode's own value.
func TestFoldPermissionModeIntoGlobal_GlobalLevelWins(t *testing.T) {
	t.Parallel()

	tier := autonomy.TierAutonomous // RiskThreshold preset == 80
	global := autonomy.Layer{Level: &tier, Overrides: map[autonomy.Knob]any{}}
	got := foldPermissionModeIntoGlobal(global, autonomy.Layer{}, autonomy.Layer{}, "normal") // normal would write 40
	if _, exists := got.Overrides[autonomy.KnobRiskThreshold]; exists {
		t.Fatalf("foldPermissionModeIntoGlobal wrote a RiskThreshold override (%v) over an explicit global Level choice (TierAutonomous) — "+
			"resolve.go's Pass 1 (session/project/global Overrides) runs entirely before Pass 2 (any layer's Level), "+
			"so this write would have silently outranked the user's own tier pick",
			got.Overrides[autonomy.KnobRiskThreshold])
	}
}

// TestFoldPermissionModeIntoGlobal_SessionLevelWins proves the guard
// covers more than the global layer: resolve.go's resolveKnob checks
// global.Overrides (Pass 1, resolve.go:172-174) BEFORE session.Level
// (Pass 2, resolve.go:176-178), so an unguarded write into
// global.Overrides would outrank an explicit SESSION-scoped tier
// choice too, not just a global one.
func TestFoldPermissionModeIntoGlobal_SessionLevelWins(t *testing.T) {
	t.Parallel()

	tier := autonomy.TierStrict // RiskThreshold preset == 0
	session := autonomy.Layer{Level: &tier}
	got := foldPermissionModeIntoGlobal(autonomy.Layer{}, autonomy.Layer{}, session, "permissive") // permissive would write 80
	if _, exists := got.Overrides[autonomy.KnobRiskThreshold]; exists {
		t.Fatalf("foldPermissionModeIntoGlobal wrote a global RiskThreshold override (%v) despite an explicit SESSION Level choice — "+
			"a global.Overrides write outranks session.Level in resolve.go's precedence, so this must be a no-op",
			got.Overrides[autonomy.KnobRiskThreshold])
	}
}

// TestFoldPermissionModeIntoGlobal_ProjectLevelWins is
// SessionLevelWins's sibling for the project layer — resolve.go checks
// global.Overrides (Pass 1) before project.Level (Pass 2,
// resolve.go:179-181) too.
func TestFoldPermissionModeIntoGlobal_ProjectLevelWins(t *testing.T) {
	t.Parallel()

	tier := autonomy.TierBold // RiskThreshold preset == 60
	project := autonomy.Layer{Level: &tier}
	got := foldPermissionModeIntoGlobal(autonomy.Layer{}, project, autonomy.Layer{}, "strict") // strict would write 0
	if _, exists := got.Overrides[autonomy.KnobRiskThreshold]; exists {
		t.Fatalf("foldPermissionModeIntoGlobal wrote a global RiskThreshold override (%v) despite an explicit PROJECT Level choice — "+
			"a global.Overrides write outranks project.Level in resolve.go's precedence, so this must be a no-op",
			got.Overrides[autonomy.KnobRiskThreshold])
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
		got := foldPermissionModeIntoGlobal(autonomy.Layer{}, autonomy.Layer{}, autonomy.Layer{}, mode)
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

// TestComputeAutonomyKnobs_GlobalLevelBeatsPermissionMode is the
// reviewer's confirmed live reproduction of the blocking defect,
// pinned end to end through the real production call path
// (computeAutonomyKnobs, core/rpc/api.go). AutonomyPanel.vue's PRIMARY
// interaction — setTier(), frontend/src/views/settings/
// AutonomyPanel.vue:172-181, the isCustom===false branch — persists
// exactly {Level: t, Overrides: {}}. With PermissionMode left at its
// untouched default ("normal", which EffectivePermissionMode
// normalises empty/unset to), the pre-fix guard only ever inspected
// global.Overrides (empty here), folded in normal's 40, and
// resolveKnob's Pass 1 (resolve.go:172-174) returned that 40 from
// SourceGlobal BEFORE Pass 2 ever got to read global.Level
// (resolve.go:182-184) — silently overriding the user's explicit
// Autonomous (80) choice. Must fail on the pre-fix code.
func TestComputeAutonomyKnobs_GlobalLevelBeatsPermissionMode(t *testing.T) {
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

	// PermissionMode is deliberately left untouched (defaults to
	// "normal") — the panel's tier pick is the only explicit choice.
	tier := autonomy.TierAutonomous
	if err := store.SaveAutonomyProfile(autonomy.Layer{Level: &tier, Overrides: map[autonomy.Knob]any{}}); err != nil {
		t.Fatalf("SaveAutonomyProfile: %v", err)
	}

	got := computeAutonomyKnobs(ctx, "", api.core, api.settingsImpl)
	if got.RiskThreshold != 80 {
		t.Fatalf("RiskThreshold = %d, want 80 (TierAutonomous, the user's explicit global tier choice) — "+
			"PermissionMode's untouched default (\"normal\" -> 40) must not clobber a Level-only autonomy choice",
			got.RiskThreshold)
	}
	if src := got.SourceTrace[autonomy.KnobRiskThreshold]; src != autonomy.SourceGlobal {
		t.Errorf("SourceTrace[RiskThreshold] = %v, want SourceGlobal (from the Level preset, not an override)", src)
	}
}

// TestComputeAutonomyKnobs_SessionLevelBeatsPermissionMode proves the
// fix covers the session layer too: computeAutonomyKnobs must not let
// PermissionMode's global-layer fold outrank an explicit session-scoped
// tier choice. Must fail on the pre-fix code (which would fold
// permissive's 80 into global.Overrides, and resolve.go's Pass 1
// checks global.Overrides before Pass 2 ever reads session.Level).
func TestComputeAutonomyKnobs_SessionLevelBeatsPermissionMode(t *testing.T) {
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

	if err := store.SavePermissionMode("permissive"); err != nil {
		t.Fatalf("SavePermissionMode: %v", err)
	}

	rec, err := c.SessionManager().Create(ctx, "permmode-session-level")
	if err != nil {
		t.Fatalf("session create: %v", err)
	}
	tier := autonomy.TierStrict // RiskThreshold preset == 0
	if err := c.SessionManager().SetAutonomyProfile(ctx, rec.ID, autonomy.Layer{Level: &tier}); err != nil {
		t.Fatalf("SetAutonomyProfile (session): %v", err)
	}

	got := computeAutonomyKnobs(ctx, rec.ID, api.core, api.settingsImpl)
	if got.RiskThreshold != 0 {
		t.Fatalf("RiskThreshold = %d, want 0 (TierStrict, the session's explicit tier choice) — "+
			"PermissionMode=\"permissive\" must not clobber a session-scoped Level choice",
			got.RiskThreshold)
	}
	if src := got.SourceTrace[autonomy.KnobRiskThreshold]; src != autonomy.SourceSession {
		t.Errorf("SourceTrace[RiskThreshold] = %v, want SourceSession", src)
	}
}

// TestComputeAutonomyKnobs_ProjectLevelBeatsPermissionMode is
// SessionLevelBeatsPermissionMode's sibling for the project layer.
func TestComputeAutonomyKnobs_ProjectLevelBeatsPermissionMode(t *testing.T) {
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

	proj, err := c.ProjectManager().Create(ctx, "permmode-project-level", "")
	if err != nil {
		t.Fatalf("project create: %v", err)
	}
	tier := autonomy.TierBold // RiskThreshold preset == 60
	if err := c.ProjectManager().SetAutonomyProfile(ctx, proj.ID, autonomy.Layer{Level: &tier}); err != nil {
		t.Fatalf("SetAutonomyProfile (project): %v", err)
	}
	rec, err := c.SessionManager().CreateInProject(ctx, "permmode-project-level-sess", &proj.ID)
	if err != nil {
		t.Fatalf("session create: %v", err)
	}

	got := computeAutonomyKnobs(ctx, rec.ID, api.core, api.settingsImpl)
	if got.RiskThreshold != 60 {
		t.Fatalf("RiskThreshold = %d, want 60 (TierBold, the project's explicit tier choice) — "+
			"PermissionMode=\"strict\" must not clobber a project-scoped Level choice",
			got.RiskThreshold)
	}
	if src := got.SourceTrace[autonomy.KnobRiskThreshold]; src != autonomy.SourceProject {
		t.Errorf("SourceTrace[RiskThreshold] = %v, want SourceProject", src)
	}
}
