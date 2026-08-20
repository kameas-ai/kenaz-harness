package settings

// wp03_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-1 /
// WP03. AC-004 and AC-005 (AC-PI-1, settings half).

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWP03_AC004_ZeroValueSettingsResolvesCollapseDefaultTrue asserts a
// zero-value Settings{} (in-memory, never touched a file) resolves
// EffectiveAutoCollapseBranchesInSidebar() to true, the documented default.
//
// Mutation per tasks.md: restore `return s.AutoCollapseBranchesInSidebar`
// on a plain bool field. Must fail (zero-value bool is false).
func TestWP03_AC004_ZeroValueSettingsResolvesCollapseDefaultTrue(t *testing.T) {
	var s Settings
	if got := s.EffectiveAutoCollapseBranchesInSidebar(); got != true {
		t.Fatalf("EffectiveAutoCollapseBranchesInSidebar() on zero-value Settings = %v, want true (DefaultAutoCollapseBranchesInSidebar)", got)
	}
}

// TestWP03_AC004_ExplicitFalseIsHonoured is the companion case: once a
// user has genuinely turned the toggle off, that choice must survive —
// the *bool fix must not turn every "false" into "true".
func TestWP03_AC004_ExplicitFalseIsHonoured(t *testing.T) {
	f := false
	s := Settings{AutoCollapseBranchesInSidebar: &f}
	if got := s.EffectiveAutoCollapseBranchesInSidebar(); got != false {
		t.Fatalf("EffectiveAutoCollapseBranchesInSidebar() with explicit false = %v, want false", got)
	}
}

// TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue is
// AC-005 / AC-PI-1 (settings half): a settings.json written by v0.64.0
// (no autoCollapseBranchesInSidebar key at all — see testdata/upgrade/
// v0.64.0/PROVENANCE.md) loads under HEAD and resolves to true, not false.
//
// Falsifiable: reverting the *bool shape change (restoring `bool` +
// `return s.AutoCollapseBranchesInSidebar`) must make this fail, because a
// plain bool unmarshalled from a file with no matching key stays at its Go
// zero-value (false). Do NOT delete this test alongside such a revert
// without first confirming it fails — that failure is the falsifiability
// proof AC-PI-1 requires.
func TestWP03_AC005_V0640SettingsFixture_ResolvesCollapseDefaultTrue(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.64.0", "settings.json"))
	if err != nil {
		t.Fatalf("read v0.64.0 fixture: %v", err)
	}

	// FileStore reads from <userConfigDir>/kenaz-harness/settings.json;
	// reproduce that exact layout under a temp dir rather than hand-rolling
	// a second materialiser (spec instruction: "extend it or follow its
	// pattern; do not hand-roll a second materialiser" — this is the
	// settings-side sibling of that SQL-side rule).
	tmp := t.TempDir()
	kenazDir := filepath.Join(tmp, "kenaz-harness")
	if err := os.MkdirAll(kenazDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(kenazDir, "settings.json"), fixture, 0o644); err != nil {
		t.Fatalf("write fixture into place: %v", err)
	}

	store, err := NewFileStore(tmp)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	loaded, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if loaded.AutoCollapseBranchesInSidebar != nil {
		t.Fatalf("expected the v0.64.0 fixture (no autoCollapseBranchesInSidebar key) to unmarshal to a nil pointer, got %v", *loaded.AutoCollapseBranchesInSidebar)
	}
	if got := loaded.EffectiveAutoCollapseBranchesInSidebar(); got != true {
		t.Fatalf("EffectiveAutoCollapseBranchesInSidebar() on a v0.64.0-shaped file = %v, want true", got)
	}

	// Sanity: the fixture's other fields backfilled correctly too, proving
	// this is a real LoadAll round-trip and not a vacuous pass on an
	// all-defaults struct.
	if loaded.Theme != "dark" {
		t.Fatalf("expected fixture theme 'dark' to survive the round-trip, got %q", loaded.Theme)
	}
}
