package settings

// harness_self_mcp_disabled_upgrade_test.go —
// harness-self-attach-01PMHS01 UNIT-7, UNIT-PI (WP-persistence-integrity),
// AC-PI-1.
//
// Same rationale as graph_authoring_upgrade_test.go (read that file's doc
// comment for the full argument): settings persistence in this harness is
// a single JSON file (settings.FileStore), not sqlite, so
// core/storage/sqlite.TestUpgradePath's snapshot chain cannot reach this
// field at all. The honest equivalent is booting a settings.json shaped
// like a real prior release wrote it — v0640SettingsJSON, declared in
// graph_authoring_upgrade_test.go and reused verbatim here rather than
// duplicated, since both fields need exactly the same property: "a real,
// populated settings.json from before this key existed." v0.64.0 predates
// GraphAuthoringEnabled (UNIT-4 of model-authored-graphs-01PMGA01) AND
// HarnessSelfMCPDisabled (UNIT-7 of this mission) — both landed after it —
// so the same fixture proves the same thing for both fields without
// needing a newer, larger snapshot.
//
// Falsifiable: if LoadHarnessSelfMCPDisabled's implementation changed to
// default an absent key to true (silently detaching the harness-self
// server from every upgraded install), this test fails — there is no
// "harnessSelfMCPDisabled" key in v0640SettingsJSON for it to read "true"
// from; the read comes entirely from Go's json.Unmarshal leaving an
// absent bool field at its zero value, the actual production mechanism.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHarnessSelfMCPDisabled_UpgradeFromV0640_ReadsAsOff is AC-PI-1's
// falsifiable core for this field: a real (non-empty, non-fresh)
// settings.json that predates HarnessSelfMCPDisabled must load with the
// kill switch OFF (= server attached), through BOTH the dedicated
// accessor (what onboardingSettingsDialAdapter.IsHarnessSelfMCPDisabled
// actually calls) and the full-record LoadAll path (what Settings_Get
// returns to the frontend), so a fix narrowly scoped to only one of the
// two read paths still fails this test.
func TestHarnessSelfMCPDisabled_UpgradeFromV0640_ReadsAsOff(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "kenaz-harness", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(v0640SettingsJSON), 0o600); err != nil {
		t.Fatalf("write v0.64.0-shaped settings.json: %v", err)
	}

	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	disabled, err := store.LoadHarnessSelfMCPDisabled()
	if err != nil {
		t.Fatalf("LoadHarnessSelfMCPDisabled against a v0.64.0-shaped file: %v", err)
	}
	if disabled {
		t.Fatal("LoadHarnessSelfMCPDisabled returned true against a settings.json that predates the field — " +
			"absent must read as attached, the pre-UNIT-7 default every existing install already had")
	}

	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.HarnessSelfMCPDisabled {
		t.Fatal("LoadAll().HarnessSelfMCPDisabled is true against a v0.64.0-shaped file")
	}
	// Sanity: the fixture really is "an old real install", not an
	// accidentally-empty file.
	if !got.CedarStrictWorkflowMode {
		t.Fatal("fixture sanity check failed: cedarStrictWorkflowMode did not round-trip true — this fixture is not actually exercising a populated pre-existing file")
	}
	if got.Theme != "dark" {
		t.Fatalf("fixture sanity check failed: theme = %q, want dark — this fixture is not actually exercising a populated pre-existing file", got.Theme)
	}
}

// TestHarnessSelfMCPDisabled_UpgradeFromV0640_SaveThenReload closes the
// loop: after loading the v0.64.0-shaped file, saving the kill switch
// through the real store must not disturb any of the pre-existing
// fields — an additive round-trip, not a rebuild (AC-PI-3's property,
// demonstrated at the actual read/write layer).
func TestHarnessSelfMCPDisabled_UpgradeFromV0640_SaveThenReload(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "kenaz-harness", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(v0640SettingsJSON), 0o600); err != nil {
		t.Fatalf("write v0.64.0-shaped settings.json: %v", err)
	}
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	if err := store.SaveHarnessSelfMCPDisabled(true); err != nil {
		t.Fatalf("SaveHarnessSelfMCPDisabled(true): %v", err)
	}

	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after enabling the kill switch: %v", err)
	}
	if !got.HarnessSelfMCPDisabled {
		t.Fatal("HarnessSelfMCPDisabled did not persist as true after SaveHarnessSelfMCPDisabled(true)")
	}
	// Every pre-existing v0.64.0 field must have survived the write
	// unchanged — additive, not destructive.
	if got.Theme != "dark" || got.Accent != "violet" {
		t.Errorf("pre-existing theme/accent were disturbed by saving the new dial: theme=%q accent=%q", got.Theme, got.Accent)
	}
	if !got.CedarStrictWorkflowMode {
		t.Error("pre-existing cedarStrictWorkflowMode was reset by saving the new dial")
	}
	if got.MaxAgentTurns != 40 {
		t.Errorf("pre-existing maxAgentTurns was disturbed: got %d, want 40", got.MaxAgentTurns)
	}
	if !got.FirstRunOnboardingCompleted {
		t.Error("pre-existing firstRunOnboardingCompleted was reset by saving the new dial")
	}
}
