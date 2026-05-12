package narrative

import (
	"os"
	"sync/atomic"
)

// settingsEnabledFn is the runtime-toggleable settings dial consulted
// by Enabled(). Set once at boot via SetSettingsGate. The zero value
// means "not wired — use env-var only".
var settingsEnabledFn atomic.Pointer[func() bool]

// SetSettingsGate wires the settings-backed MemoryNarrativeEnabled dial.
// Call this once at harness boot after the settings store is opened.
// The fn is read on every Enabled() call so a runtime toggle takes
// effect on the next turn without a harness restart.
func SetSettingsGate(fn func() bool) {
	if fn == nil {
		return
	}
	settingsEnabledFn.Store(&fn)
}

// Enabled reports whether the narrative layer is active. The precedence
// order (highest first) is:
//
//  1. HARNESS_MEMORY_NARRATIVE_LAYER=off (env) → always false.
//  2. HARNESS_MEMORY_NARRATIVE_LAYER=on (env) → always true.
//  3. Settings.MemoryNarrativeEnabled dial (if wired via SetSettingsGate).
//  4. Default false (cycle N: feature flag OFF until Phase 2 rollout).
//
// Calling code must check Enabled() before any narrative write / job
// enqueue / citation detection path so toggling the dial at runtime
// takes effect within one chat turn.
func Enabled() bool {
	switch os.Getenv("HARNESS_MEMORY_NARRATIVE_LAYER") {
	case "off", "false", "0":
		return false
	case "on", "true", "1":
		return true
	}
	if fp := settingsEnabledFn.Load(); fp != nil {
		return (*fp)()
	}
	// Default: disabled (Phase 1 / cycle N rollout posture).
	return false
}

// LongTermEnabled reports whether the long-term scope tier (WP09) is
// active. Gated by HARNESS_MEMORY_LONGTERM env var (default false) in
// addition to the base Enabled() gate, because WP09's runtime effect
// depends on memory-prune-completion-01KQ8TD8's skip-rule WP which is
// NOT in v0.8.5 scope.
//
// When LongTermEnabled() returns false, the schema (ScopeKindLongTerm,
// migration 0822) is present but the Promoter never promotes chunks and
// the prelude loader returns an empty slice.
func LongTermEnabled() bool {
	if !Enabled() {
		return false
	}
	switch os.Getenv("HARNESS_MEMORY_LONGTERM") {
	case "on", "true", "1":
		return true
	default:
		return false
	}
}

// NarrativeCompactShadowMode reports whether the compactor should run
// both narrative_first and the legacy strategy in parallel and log
// divergence without changing output. Controlled by
// HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW (default false).
func NarrativeCompactShadowMode() bool {
	switch os.Getenv("HARNESS_MEMORY_NARRATIVE_COMPACT_SHADOW") {
	case "on", "true", "1":
		return true
	default:
		return false
	}
}
