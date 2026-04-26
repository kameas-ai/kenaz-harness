package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_LoadDefaults_OnMissingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("LastRoute = %q, want /sessions", got.LastRoute)
	}
	if got.Theme != "system" {
		t.Errorf("Theme = %q, want system", got.Theme)
	}
}

func TestFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	in := Settings{
		SchemaVersion: 1,
		LastRoute:     "/audit",
		Theme:         "dark",
		Accent:        "default",
		WindowSize:    WindowSize{Width: 1600, Height: 1000},
	}
	if err := store.SaveAll(in); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got != in {
		t.Errorf("round trip lost data: got %+v, want %+v", got, in)
	}

	// Disk format: file exists, schemaVersion field present.
	raw, err := os.ReadFile(filepath.Join(dir, "kaneaz-harness", "settings.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var disk map[string]any
	if err := json.Unmarshal(raw, &disk); err != nil {
		t.Fatalf("disk JSON parse: %v", err)
	}
	if _, ok := disk["schemaVersion"]; !ok {
		t.Errorf("disk JSON missing schemaVersion (privacy invariant #5)")
	}
	if _, ok := disk["lastRoute"]; !ok {
		t.Errorf("disk JSON missing lastRoute")
	}
	if _, ok := disk["theme"]; !ok {
		t.Errorf("disk JSON missing theme")
	}
}

func TestFileStore_PartialFile_FillsDefaults(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "kaneaz-harness", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got.Theme != "dark" {
		t.Errorf("Theme = %q, want dark", got.Theme)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("LastRoute should fall back to /sessions, got %q", got.LastRoute)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("SchemaVersion should fall back to 1, got %d", got.SchemaVersion)
	}
}

func TestFileStore_RouteAndThemeMethods(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.SaveRoute("/bundles"); err != nil {
		t.Fatalf("SaveRoute: %v", err)
	}
	if err := store.SaveTheme("dark"); err != nil {
		t.Fatalf("SaveTheme: %v", err)
	}
	r, err := store.LoadRoute()
	if err != nil {
		t.Fatalf("LoadRoute: %v", err)
	}
	if r != "/bundles" {
		t.Errorf("LoadRoute = %q, want /bundles", r)
	}
	th, err := store.LoadTheme()
	if err != nil {
		t.Fatalf("LoadTheme: %v", err)
	}
	if th != "dark" {
		t.Errorf("LoadTheme = %q, want dark", th)
	}
}

func TestAPI_GetSet(t *testing.T) {
	api := NewAPI(nil)
	got, err := api.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.LastRoute != "/sessions" {
		t.Errorf("default LastRoute = %q, want /sessions", got.LastRoute)
	}

	in := got
	in.LastRoute = "/audit"
	if err := api.Set(context.Background(), in); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got2, _ := api.Get(context.Background())
	if got2.LastRoute != "/audit" {
		t.Errorf("post-set LastRoute = %q, want /audit", got2.LastRoute)
	}
}

func TestFileStore_ConfirmEachDefaultsTrue(t *testing.T) {
	// A fresh install with no settings.json (and one with a partial file
	// missing the confirmEachDisabled field) must report ConfirmEach
	// enabled — the spec's default-ON requirement maps to a zero-value
	// ConfirmEachDisabled.
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	got, err := store.LoadConfirmEach()
	if err != nil {
		t.Fatalf("LoadConfirmEach: %v", err)
	}
	if !got {
		t.Errorf("LoadConfirmEach() = false on fresh install, want true (default ON)")
	}
}

func TestFileStore_ConfirmEachRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.SaveConfirmEach(false); err != nil {
		t.Fatalf("SaveConfirmEach: %v", err)
	}
	got, err := store.LoadConfirmEach()
	if err != nil {
		t.Fatalf("LoadConfirmEach: %v", err)
	}
	if got {
		t.Errorf("LoadConfirmEach = true after Save(false)")
	}
	if err := store.SaveConfirmEach(true); err != nil {
		t.Fatalf("SaveConfirmEach(true): %v", err)
	}
	got, _ = store.LoadConfirmEach()
	if !got {
		t.Errorf("LoadConfirmEach = false after Save(true)")
	}
}

func TestAPI_StoreAccessor(t *testing.T) {
	api := NewAPI(nil)
	store := api.Store()
	if store == nil {
		t.Fatalf("Store() returned nil")
	}
	if err := store.SaveRoute("/tools"); err != nil {
		t.Fatalf("Store.SaveRoute: %v", err)
	}
	got, _ := api.Get(context.Background())
	if got.LastRoute != "/tools" {
		t.Errorf("Store and API should share state; got %q", got.LastRoute)
	}
}
