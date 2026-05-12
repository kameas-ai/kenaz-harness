package narrative

import (
	"testing"
)

// TestEnabled_EnvOn verifies that HARNESS_MEMORY_NARRATIVE_LAYER=on
// forces Enabled() to return true regardless of the settings gate.
func TestEnabled_EnvOn(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "on")
	// Wire a gate that returns false — env var should win.
	SetSettingsGate(func() bool { return false })
	t.Cleanup(func() { settingsEnabledFn.Store(nil) })

	if !Enabled() {
		t.Error("Enabled() = false, want true when HARNESS_MEMORY_NARRATIVE_LAYER=on")
	}
}

// TestEnabled_EnvOff verifies that HARNESS_MEMORY_NARRATIVE_LAYER=off
// forces Enabled() to return false regardless of the settings gate.
func TestEnabled_EnvOff(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "off")
	// Wire a gate that returns true — env var should win.
	SetSettingsGate(func() bool { return true })
	t.Cleanup(func() { settingsEnabledFn.Store(nil) })

	if Enabled() {
		t.Error("Enabled() = true, want false when HARNESS_MEMORY_NARRATIVE_LAYER=off")
	}
}

// TestEnabled_EnvFalse verifies the alternate spelling HARNESS_MEMORY_NARRATIVE_LAYER=false.
func TestEnabled_EnvFalse(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "false")
	if Enabled() {
		t.Error("Enabled() = true, want false when HARNESS_MEMORY_NARRATIVE_LAYER=false")
	}
}

// TestEnabled_SettingsGate verifies that the settings dial is consulted
// when no env override is set.
func TestEnabled_SettingsGate(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "") // unset the env override
	enabled := true
	SetSettingsGate(func() bool { return enabled })
	t.Cleanup(func() { settingsEnabledFn.Store(nil) })

	if !Enabled() {
		t.Error("Enabled() = false, want true when settings gate returns true")
	}
	enabled = false
	if Enabled() {
		t.Error("Enabled() = true, want false after settings gate toggled to false")
	}
}

// TestEnabled_DefaultFalse verifies that when no env var and no settings
// gate are set, Enabled() returns false (Phase 1 rollout posture).
func TestEnabled_DefaultFalse(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "")
	settingsEnabledFn.Store(nil)

	if Enabled() {
		t.Error("Enabled() = true, want false by default (no env, no settings gate)")
	}
}

// TestEnabled_NilSettingsGateIsNoop verifies that SetSettingsGate(nil)
// does not panic and leaves the default-false behaviour intact.
func TestEnabled_NilSettingsGateIsNoop(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "")
	settingsEnabledFn.Store(nil)
	SetSettingsGate(nil) // must not panic

	if Enabled() {
		t.Error("Enabled() = true after SetSettingsGate(nil), want false")
	}
}

// TestLongTermEnabled_RequiresBaseEnabled verifies that
// HARNESS_MEMORY_LONGTERM=on alone is not enough — base Enabled() must
// also return true.
func TestLongTermEnabled_RequiresBaseEnabled(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "off")
	t.Setenv("HARNESS_MEMORY_LONGTERM", "on")

	if LongTermEnabled() {
		t.Error("LongTermEnabled() = true, want false when base Enabled()=false")
	}
}

// TestLongTermEnabled_BothOn verifies that both gates must be on for
// LongTermEnabled() to return true.
func TestLongTermEnabled_BothOn(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "on")
	t.Setenv("HARNESS_MEMORY_LONGTERM", "on")

	if !LongTermEnabled() {
		t.Error("LongTermEnabled() = false, want true when both env vars are on")
	}
}

// TestLongTermEnabled_DefaultFalse verifies that when only base is
// enabled (no HARNESS_MEMORY_LONGTERM), LongTermEnabled returns false.
func TestLongTermEnabled_DefaultFalse(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "on")
	t.Setenv("HARNESS_MEMORY_LONGTERM", "")

	if LongTermEnabled() {
		t.Error("LongTermEnabled() = true, want false when HARNESS_MEMORY_LONGTERM unset")
	}
}

// TestNarrativeCompactShadowMode verifies the shadow-mode env var.
func TestNarrativeCompactShadowMode(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW", "on")
	if !NarrativeCompactShadowMode() {
		t.Error("NarrativeCompactShadowMode() = false, want true when env=on")
	}
}

// TestNarrativeCompactShadowMode_Default verifies default-false.
func TestNarrativeCompactShadowMode_Default(t *testing.T) {
	t.Setenv("HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW", "")
	if NarrativeCompactShadowMode() {
		t.Error("NarrativeCompactShadowMode() = true, want false by default")
	}
}
