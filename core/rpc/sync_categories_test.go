// sync_categories_test.go — unit tests for registerSyncCategories and
// the credential-free collectors it wires.
//
// (harness-fleet-sync-activation-01NSYNC01 NFR-004)
package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	settingsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// newTestStore creates a file-backed SettingsStore under a temp dir.
func newTestStore(t *testing.T) settingsview.SettingsStore {
	t.Helper()
	dir := t.TempDir()
	store, err := settingsview.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return store
}

// ── registerSyncCategories ────────────────────────────────────────────────────

// TestRegisterSyncCategories_NilSyncer verifies that calling
// registerSyncCategories with a nil syncer is a safe no-op.
func TestRegisterSyncCategories_NilSyncer(t *testing.T) {
	store := newTestStore(t)
	// Must not panic.
	registerSyncCategories(context.Background(), nil, store, nil)
}

// TestRegisterSyncCategories_NilStore verifies that calling with a nil store
// does not panic and still registers categories that don't require the store.
func TestRegisterSyncCategories_NilStore(t *testing.T) {
	syncer := corefleet.NewSyncer(nil)
	// Must not panic; mcp_category is nil too — tests the full nil-guard path.
	registerSyncCategories(context.Background(), syncer, nil, nil)
	syncer.Stop()
}

// TestRegisterSyncCategories_CategoriesRegistered verifies that after
// registerSyncCategories is called, the expected categories have wired
// collectors/appliers. With mcpCategory=nil, installed_mcp is skipped;
// the remaining 4 should be registered (ui_theme, model_prefs,
// provider_profiles, mcp_recipes).
func TestRegisterSyncCategories_CategoriesRegistered(t *testing.T) {
	store := newTestStore(t)
	syncer := corefleet.NewSyncer(nil) // nil client → network calls are short-circuited
	t.Cleanup(syncer.Stop)

	registerSyncCategories(context.Background(), syncer, store, nil)

	// With nil mcpCategory, installed_mcp is not wired.
	const wantCount = 4 // ui_theme + model_prefs + provider_profiles + mcp_recipes
	registered := syncer.RegisteredCategories()
	if len(registered) != wantCount {
		t.Errorf("registered categories = %d, want %d (nil mcp skips installed_mcp)",
			len(registered), wantCount)
	}

	// All non-mcp categories must have a working collector.
	for _, cat := range []corefleet.SyncCategory{
		corefleet.SyncCategoryUITheme,
		corefleet.SyncCategoryModelPrefs,
		corefleet.SyncCategoryProviderProfiles,
		corefleet.SyncCategoryMCPRecipes,
	} {
		if _, err := syncer.CollectCategory(context.Background(), cat); err != nil {
			t.Errorf("CollectCategory(%s): %v", cat, err)
		}
	}
}

// ── Credential-free collector assertions ─────────────────────────────────────

// TestUIThemeCollector_CredentialFree verifies the ui_theme collector
// returns only theme + accent — no credential bytes.
func TestUIThemeCollector_CredentialFree(t *testing.T) {
	store := newTestStore(t)

	// Seed a theme value.
	s, _ := store.LoadAll()
	s.Theme = "dark"
	s.Accent = "#ff6600"
	_ = store.SaveAll(s)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	raw, err := syncer.CollectCategory(context.Background(), corefleet.SyncCategoryUITheme)
	if err != nil {
		t.Fatalf("collect ui_theme: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal ui_theme payload: %v", err)
	}

	if payload["theme"] != "dark" {
		t.Errorf("theme=%q, want dark", payload["theme"])
	}
	if payload["accent"] != "#ff6600" {
		t.Errorf("accent=%q, want #ff6600", payload["accent"])
	}

	// Ensure no unexpected keys (no credential fields).
	for k := range payload {
		if k != "theme" && k != "accent" {
			t.Errorf("unexpected key in ui_theme payload: %q", k)
		}
	}
}

// TestModelPrefsCollector_CredentialFree verifies the model_prefs collector
// emits only credential-free fields and does NOT include any API key material.
func TestModelPrefsCollector_CredentialFree(t *testing.T) {
	store := newTestStore(t)

	// Seed model-prefs values.
	s, _ := store.LoadAll()
	s.CompactionAggressiveness = "balanced"
	s.CompactionArchiveDays = 7
	s.CompactionRecentWindow = 3
	s.MaxAgentTurns = 20
	_ = store.SaveAll(s)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	raw, err := syncer.CollectCategory(context.Background(), corefleet.SyncCategoryModelPrefs)
	if err != nil {
		t.Fatalf("collect model_prefs: %v", err)
	}

	var p modelPrefsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal model_prefs payload: %v", err)
	}

	if p.CompactionAggressiveness != "balanced" {
		t.Errorf("CompactionAggressiveness=%q, want balanced", p.CompactionAggressiveness)
	}
	if p.CompactionArchiveDays != 7 {
		t.Errorf("CompactionArchiveDays=%d, want 7", p.CompactionArchiveDays)
	}
	if p.CompactionRecentWindow != 3 {
		t.Errorf("CompactionRecentWindow=%d, want 3", p.CompactionRecentWindow)
	}
	if p.MaxAgentTurns != 20 {
		t.Errorf("MaxAgentTurns=%d, want 20", p.MaxAgentTurns)
	}

	// Assert no credential bytes appear in the raw JSON.
	rawStr := string(raw)
	for _, forbidden := range []string{"api_key", "apiKey", "secret", "password", "token", "credential"} {
		if strings.Contains(strings.ToLower(rawStr), forbidden) {
			t.Errorf("credential-like key %q found in model_prefs payload: %s", forbidden, rawStr)
		}
	}
}

// TestModelPrefsApplier_RoundTrip verifies the model_prefs applier writes
// non-zero values back into the settings store without overwriting zero
// fields already present.
func TestModelPrefsApplier_RoundTrip(t *testing.T) {
	store := newTestStore(t)

	// Seed an initial MaxAgentTurns value.
	s, _ := store.LoadAll()
	s.MaxAgentTurns = 10
	_ = store.SaveAll(s)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	// Apply a partial update: only CompactionAggressiveness + MaxAgentTurns.
	incoming, _ := json.Marshal(modelPrefsPayload{
		CompactionAggressiveness: "aggressive",
		MaxAgentTurns:            25,
	})
	if err := syncer.ApplyCategory(context.Background(), corefleet.SyncCategoryModelPrefs, incoming); err != nil {
		t.Fatalf("apply model_prefs: %v", err)
	}

	got, _ := store.LoadAll()
	if got.CompactionAggressiveness != "aggressive" {
		t.Errorf("CompactionAggressiveness=%q, want aggressive", got.CompactionAggressiveness)
	}
	if got.MaxAgentTurns != 25 {
		t.Errorf("MaxAgentTurns=%d, want 25", got.MaxAgentTurns)
	}
}

// TestProviderProfilesCollector_EmptyPayload verifies the provider_profiles
// collector returns an empty JSON object (no credential bytes).
func TestProviderProfilesCollector_EmptyPayload(t *testing.T) {
	store := newTestStore(t)
	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	raw, err := syncer.CollectCategory(context.Background(), corefleet.SyncCategoryProviderProfiles)
	if err != nil {
		t.Fatalf("collect provider_profiles: %v", err)
	}
	rawStr := strings.TrimSpace(string(raw))
	if rawStr != "{}" {
		t.Errorf("provider_profiles payload=%q, want {}", rawStr)
	}
}

// TestMCPRecipesCollector_EmptyPayload verifies mcp_recipes collector is safe.
func TestMCPRecipesCollector_EmptyPayload(t *testing.T) {
	store := newTestStore(t)
	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	raw, err := syncer.CollectCategory(context.Background(), corefleet.SyncCategoryMCPRecipes)
	if err != nil {
		t.Fatalf("collect mcp_recipes: %v", err)
	}
	rawStr := strings.TrimSpace(string(raw))
	if rawStr != "{}" {
		t.Errorf("mcp_recipes payload=%q, want {}", rawStr)
	}
}

// TestNoopApplier_NoError verifies the noop applier for provider_profiles
// and mcp_recipes returns nil and doesn't modify the settings store.
func TestNoopApplier_NoError(t *testing.T) {
	store := newTestStore(t)
	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, nil)

	before, _ := store.LoadAll()
	for _, cat := range []corefleet.SyncCategory{
		corefleet.SyncCategoryProviderProfiles,
		corefleet.SyncCategoryMCPRecipes,
	} {
		if err := syncer.ApplyCategory(context.Background(), cat, json.RawMessage(`{}`)); err != nil {
			t.Errorf("ApplyCategory(%s): %v", cat, err)
		}
	}
	after, _ := store.LoadAll()
	// Spot-check a few fields to confirm the store wasn't mutated.
	if before.Theme != after.Theme {
		t.Errorf("Theme changed by noop applier: %q → %q", before.Theme, after.Theme)
	}
	if before.MaxAgentTurns != after.MaxAgentTurns {
		t.Errorf("MaxAgentTurns changed by noop applier: %d → %d", before.MaxAgentTurns, after.MaxAgentTurns)
	}
}
