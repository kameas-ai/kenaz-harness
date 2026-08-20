package settings

// graph_authoring_upgrade_test.go — model-authored-graphs-01PMGA01
// UNIT-PI (WP-persistence-integrity), AC-PI-1.
//
// WHY THIS FILE EXISTS INSTEAD OF AN UPGRADE-PATH SQLITE TEST.
// spec.md/tasks.md describe AC-PI-1 in terms of
// core/storage/sqlite/testdata/upgrade/<tag>/dump.sql — booting a
// previous release's SQLITE database under HEAD, per
// core/storage/sqlite.TestUpgradePath. GraphAuthoringEnabled is NOT a
// sqlite-backed field: settings persistence for this harness is a
// single JSON file at <UserConfigDir>/kenaz-harness/settings.json
// (settings.FileStore, see impl.go's own package doc: "Persistence is
// a single JSON file per privacy CI invariant #5"), a storage
// mechanism TestUpgradePath's sqlite snapshot chain cannot reach at
// all — verified by reading core/storage/sqlite/upgrade_path_test.go,
// which walks testdata/upgrade/*/dump.sql exclusively.
//
// The tree wins over the spec's assumption here (CLAUDE.md: "where the
// owner's briefing and the tree disagree, the tree wins"). The
// equivalent, honest test for a JSON-file-backed field is: construct a
// settings.json shaped exactly like one a v0.64.0 install would have
// written — every field that existed at that tag, populated with
// non-default values so this is provably "an old real install", NOT
// an empty/missing file (TestFileStore_LoadDefaults_OnMissingFile
// already covers that different case) — and confirm
// LoadGraphAuthoringEnabled reads false against it. This is the ONLY
// path that distinguishes "the field defaults to false" from "the
// field defaults to false on a fresh install": a v0.64.0 settings.json
// is a real file with real content that simply predates the key.

import (
	"os"
	"path/filepath"
	"testing"
)

// v0640SettingsJSON is a representative settings.json shaped exactly
// like a v0.64.0 install's would be: every top-level key that existed
// at that tag, several set away from their zero value so this reads as
// a used, non-fresh profile — and, load-bearingly, no
// "graphAuthoringEnabled" key anywhere, because that field did not
// exist until model-authored-graphs-01PMGA01 UNIT-4.
const v0640SettingsJSON = `{
  "schemaVersion": 1,
  "lastRoute": "/sessions/01HZXYZ",
  "theme": "dark",
  "accent": "violet",
  "windowSize": {"width": 1440, "height": 900},
  "memoryEnabled": true,
  "confirmEachDisabled": false,
  "webSearchEnabled": true,
  "bashEnabled": true,
  "webFetchEnabled": false,
  "saveArtifactDisabled": false,
  "maxAgentTurns": 40,
  "permissionMode": "normal",
  "permissionCacheDangerousOps": false,
  "bashAllowlistMigrated": true,
  "permissionsMigrationToastShown": true,
  "cedarStrictCredentialMode": false,
  "cedarStrictWorkflowMode": true,
  "credentialAuditRetentionDays": 90,
  "fsReadDisabled": false,
  "fsWriteDisabled": true,
  "todoDisabled": false,
  "autoTitleDisabled": false,
  "mcpAutoRestartDisabled": false,
  "firstRunOnboardingCompleted": true
}`

// TestGraphAuthoringEnabled_UpgradeFromV0640_ReadsAsOff is AC-PI-1's
// falsifiable core: a real (non-empty, non-fresh) settings.json that
// predates GraphAuthoringEnabled must load with the field OFF, through
// BOTH the dedicated accessor (what the graph.author gate actually
// calls, per core/rpc's WithAuthoringEnabled wiring) and the full-record
// LoadAll path (what Settings_Get returns to the frontend), so a fix
// narrowly scoped to only one of the two read paths still fails this
// test.
//
// Falsifiable: if LoadGraphAuthoringEnabled's implementation were
// changed to default an unset/absent key to true (the dangerous
// direction FR-006 forbids), this test fails — there is no key in
// v0640SettingsJSON for it to read "true" from; the reading comes
// entirely from Go's json.Unmarshal leaving an absent bool field at its
// zero value, which is the actual production mechanism, not a stand-in
// for it.
func TestGraphAuthoringEnabled_UpgradeFromV0640_ReadsAsOff(t *testing.T) {
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

	enabled, err := store.LoadGraphAuthoringEnabled()
	if err != nil {
		t.Fatalf("LoadGraphAuthoringEnabled against a v0.64.0-shaped file: %v", err)
	}
	if enabled {
		t.Fatal("LoadGraphAuthoringEnabled returned true against a settings.json that predates the field — absent must read as off")
	}

	// The full-record path must agree — this is what Settings_Get
	// returns to the frontend and what a hand-rolled fixture testing
	// only the narrow accessor could miss.
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.GraphAuthoringEnabled {
		t.Fatal("LoadAll().GraphAuthoringEnabled is true against a v0.64.0-shaped file")
	}
	// Sanity: the fixture really is "an old real install", not an
	// accidentally-empty file — a field that existed at v0.64.0 must
	// have round-tripped its non-default value.
	if !got.CedarStrictWorkflowMode {
		t.Fatal("fixture sanity check failed: cedarStrictWorkflowMode did not round-trip true — this fixture is not actually exercising a populated pre-existing file")
	}
	if got.Theme != "dark" {
		t.Fatalf("fixture sanity check failed: theme = %q, want dark — this fixture is not actually exercising a populated pre-existing file", got.Theme)
	}
}

// TestGraphAuthoringEnabled_UpgradeFromV0640_SaveThenReload closes the
// loop: after loading the v0.64.0-shaped file, saving the dial through
// the real store must not disturb any of the pre-existing fields (an
// additive round-trip, not a rebuild) — the AC-PI-3 property ("a single
// additive settings field, no destructive migration") demonstrated at
// the actual read/write layer rather than merely asserted in prose.
func TestGraphAuthoringEnabled_UpgradeFromV0640_SaveThenReload(t *testing.T) {
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

	if err := store.SaveGraphAuthoringEnabled(true); err != nil {
		t.Fatalf("SaveGraphAuthoringEnabled(true): %v", err)
	}

	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll after enabling: %v", err)
	}
	if !got.GraphAuthoringEnabled {
		t.Fatal("GraphAuthoringEnabled did not persist as true after SaveGraphAuthoringEnabled(true)")
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
