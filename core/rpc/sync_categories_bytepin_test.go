// sync_categories_bytepin_test.go — WP01 zero-behavior-change regression
// pins (fleet-generic-sync-framework-01NSYNC02).
//
// Written BEFORE the SyncKind-registry refactor of sync_categories.go /
// core/fleet/sync.go landed, to pin the exact collect/apply round-trip
// byte shape of the five existing LWW categories. If a future change to the
// registry adapter (SyncKind.CategoryConfig, core/fleet/synckind.go)
// silently alters wire bytes, these tests catch it.
package rpc

import (
	"context"
	"encoding/json"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// fakeMCPReader implements corefleet.MCPRegistryReader for the byte-pin test.
type fakeMCPReader struct {
	items []corefleet.InstalledMCP
}

func (f *fakeMCPReader) ListInstalled() ([]corefleet.InstalledMCP, error) {
	return f.items, nil
}

// fakeMCPWriter implements corefleet.MCPRegistryWriter, recording the last
// applied payload for the round-trip assertion.
type fakeMCPWriter struct {
	applied []corefleet.InstalledMCP
}

func (f *fakeMCPWriter) ApplyInstalled(incoming []corefleet.InstalledMCP) error {
	f.applied = incoming
	return nil
}

// TestSyncCategories_ByteStabilityPin pins the exact collected JSON bytes
// for every registered category, given fixed local state. This is the
// "payload shape byte-stability" regression the WP01 refactor must not
// break: registering the five categories through the SyncKind registry
// instead of directly on the Syncer must produce byte-identical wire
// payloads.
func TestSyncCategories_ByteStabilityPin(t *testing.T) {
	store := newTestStore(t)
	s, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	s.Theme = "dark"
	s.Accent = "#112233"
	s.CompactionAggressiveness = "balanced"
	s.CompactionArchiveDays = 14
	s.CompactionRecentWindow = 5
	s.MaxAgentTurns = 42
	if err := store.SaveAll(s); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	reader := &fakeMCPReader{items: []corefleet.InstalledMCP{
		{
			ID:                 "mcp-1",
			RecipeID:           "github",
			EnabledState:       true,
			EnvOverrides:       map[string]string{"URL": "https://api.example.com"},
			RequiresSecretKeys: []string{"TOKEN"},
		},
	}}
	mcpCat := corefleet.NewMCPSyncCategory(reader, nil, nil, nil)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, mcpCat)

	cases := []struct {
		cat  corefleet.SyncCategory
		want string
	}{
		{corefleet.SyncCategoryUITheme, `{"theme":"dark","accent":"#112233"}`},
		{corefleet.SyncCategoryModelPrefs, `{"compactionAggressiveness":"balanced","compactionArchiveDays":14,"compactionRecentWindow":5,"maxAgentTurns":42}`},
		{corefleet.SyncCategoryProviderProfiles, `{}`},
		{corefleet.SyncCategoryMCPRecipes, `{}`},
		{corefleet.SyncCategoryInstalledMCP, `{"items":[{"id":"mcp-1","recipe_id":"github","enabled":true,"env_overrides":{"URL":"https://api.example.com"},"requires_secret_keys":["TOKEN"]}]}`},
	}

	for _, tc := range cases {
		raw, err := syncer.CollectCategory(context.Background(), tc.cat)
		if err != nil {
			t.Fatalf("CollectCategory(%s): %v", tc.cat, err)
		}
		if string(raw) != tc.want {
			t.Errorf("%s payload = %s, want %s", tc.cat, raw, tc.want)
		}
	}
}

// TestSyncCategories_ApplyRoundTripPin pins the apply-side round trip for
// every category with a non-noop applier: ui_theme, model_prefs, and
// installed_mcp. provider_profiles / mcp_recipes are covered by
// TestNoopApplier_NoError in sync_categories_test.go.
func TestSyncCategories_ApplyRoundTripPin(t *testing.T) {
	store := newTestStore(t)
	writer := &fakeMCPWriter{}
	mcpCat := corefleet.NewMCPSyncCategory(nil, writer, nil, nil)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSyncCategories(context.Background(), syncer, store, mcpCat)

	// ui_theme apply round-trip.
	themeIn, _ := json.Marshal(uiThemePayload{Theme: "light", Accent: "#abcdef"})
	if err := syncer.ApplyCategory(context.Background(), corefleet.SyncCategoryUITheme, themeIn); err != nil {
		t.Fatalf("apply ui_theme: %v", err)
	}
	got, _ := store.LoadAll()
	if got.Theme != "light" || got.Accent != "#abcdef" {
		t.Errorf("ui_theme applied = (%q, %q), want (light, #abcdef)", got.Theme, got.Accent)
	}

	// model_prefs apply round-trip (already covered by
	// TestModelPrefsApplier_RoundTrip; re-asserted here for the
	// registry-adapter pin).
	prefsIn, _ := json.Marshal(modelPrefsPayload{MaxAgentTurns: 99})
	if err := syncer.ApplyCategory(context.Background(), corefleet.SyncCategoryModelPrefs, prefsIn); err != nil {
		t.Fatalf("apply model_prefs: %v", err)
	}
	got, _ = store.LoadAll()
	if got.MaxAgentTurns != 99 {
		t.Errorf("model_prefs applied MaxAgentTurns = %d, want 99", got.MaxAgentTurns)
	}

	// installed_mcp apply round-trip: incoming payload reaches the writer
	// unchanged.
	mcpIn, _ := json.Marshal(corefleet.InstalledMCPPayload{
		Items: []corefleet.InstalledMCP{{ID: "mcp-2", RecipeID: "slack", EnabledState: true}},
	})
	if err := syncer.ApplyCategory(context.Background(), corefleet.SyncCategoryInstalledMCP, mcpIn); err != nil {
		t.Fatalf("apply installed_mcp: %v", err)
	}
	if len(writer.applied) != 1 || writer.applied[0].ID != "mcp-2" {
		t.Errorf("installed_mcp applied = %+v, want one item mcp-2", writer.applied)
	}
}
