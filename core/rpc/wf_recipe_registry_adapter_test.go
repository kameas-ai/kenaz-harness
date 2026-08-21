package rpc

// wf_recipe_registry_adapter_test.go — automation-actually-runs-01PMZ404
// UNIT-10, AC-011. Drives wfRecipeRegistryAdapter over a real
// <dataDir>/mcp/recipes.enabled.json — the same file
// core/mcp/recipes.LoadEnabled reads in production — rather than a fake
// RecipeRegistry, so a wrong-registry mistake (answering from the full
// shipped catalog instead of the installed list) would be caught here.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// TestWfRecipeRegistryAdapter_InstalledServer_ReportsConfigured is
// AC-011's first case: a server present in EnabledRecipes reports Has ==
// true, so the catalog's missingCredentials list comes back empty for
// it.
func TestWfRecipeRegistryAdapter_InstalledServer_ReportsConfigured(t *testing.T) {
	dataDir := t.TempDir()
	enabled := &recipes.EnabledRecipes{
		Entries: []recipes.EnabledRecipe{
			{ID: "gmail", EnabledAt: time.Now().UTC()},
		},
	}
	if err := enabled.Save(dataDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	adapter := &wfRecipeRegistryAdapter{dataDir: dataDir}
	if !adapter.Has("gmail") {
		t.Error("Has(\"gmail\") = false, want true — gmail is in recipes.enabled.json")
	}
}

// TestWfRecipeRegistryAdapter_NotInstalledServer_ReportsMissing is
// AC-011's second case, and the one that fails if the adapter were built
// over the full shipped catalog instead of the installed list (X-2/X-8):
// a server that exists in the broader recipe catalog but was never
// installed (absent from recipes.enabled.json) must report Has ==
// false.
func TestWfRecipeRegistryAdapter_NotInstalledServer_ReportsMissing(t *testing.T) {
	dataDir := t.TempDir()
	enabled := &recipes.EnabledRecipes{
		Entries: []recipes.EnabledRecipe{
			{ID: "gmail", EnabledAt: time.Now().UTC()},
		},
	}
	if err := enabled.Save(dataDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	adapter := &wfRecipeRegistryAdapter{dataDir: dataDir}
	// "slack" is a real shipped-catalog recipe ID (core/mcp/recipes'
	// bundled catalog) that was never installed in this dataDir. An
	// adapter that (wrongly) answered from the full catalog rather than
	// EnabledRecipes would report this as configured.
	if adapter.Has("slack") {
		t.Error("Has(\"slack\") = true, want false — slack was never installed in this dataDir; " +
			"an adapter built over the full shipped catalog would wrongly report every recipe as configured")
	}
}

// TestWfRecipeRegistryAdapter_ReadsLive_NotACachedSnapshot: installing a
// connector after construction must turn Has() true on the NEXT call,
// without reconstructing the adapter — matching the "read live, no boot
// snapshot" rule this mission applies to every Cedar-mode/registry
// dial.
func TestWfRecipeRegistryAdapter_ReadsLive_NotACachedSnapshot(t *testing.T) {
	dataDir := t.TempDir()
	adapter := &wfRecipeRegistryAdapter{dataDir: dataDir}

	if adapter.Has("gmail") {
		t.Fatal("Has(\"gmail\") = true before any recipe was installed")
	}

	enabled := &recipes.EnabledRecipes{
		Entries: []recipes.EnabledRecipe{
			{ID: "gmail", EnabledAt: time.Now().UTC()},
		},
	}
	if err := enabled.Save(dataDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !adapter.Has("gmail") {
		t.Error("Has(\"gmail\") = false after installing — the adapter cached a boot-time snapshot instead of reading live")
	}
}

// TestWfRecipeRegistryAdapter_EmptyDataDir_DegradesToMissing mirrors the
// test-chassis / disabled path's prior behaviour: no dataDir means no
// registry, which must degrade to "everything missing" rather than
// panicking or reporting everything configured.
func TestWfRecipeRegistryAdapter_EmptyDataDir_DegradesToMissing(t *testing.T) {
	adapter := &wfRecipeRegistryAdapter{}
	if adapter.Has("gmail") {
		t.Error("Has() with empty dataDir returned true, want false (degrade-to-missing)")
	}
}

// TestWfRecipeRegistryAdapter_ProductionWiring_Catalog_Get is AC-011's
// production-path assertion: rpc.New must actually wire the adapter into
// wfcatalogpkg.Config.RecipeRegistry. Pre-seeds recipes.enabled.json with
// "gmail" BEFORE booting the chassis, then reads the shipped
// daily_ea_briefing builtin (which references gmail, slack and
// google_calendar via mcp_call) through Catalog_Get. Before UNIT-10 this
// would report ALL THREE as missing regardless of the seeded state — a
// nil registry and a correctly-wired-but-broken registry both produce
// "all missing", so the positive case (gmail NOT listed as missing)
// is the only assertion that distinguishes "wired" from "nil".
func TestWfRecipeRegistryAdapter_ProductionWiring_Catalog_Get(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	enabled := &recipes.EnabledRecipes{
		Entries: []recipes.EnabledRecipe{
			{ID: "gmail", EnabledAt: time.Now().UTC()},
		},
	}
	if err := enabled.Save(dataDir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)

	preview, err := api.Workflows().Catalog_Get(context.Background(), "daily_ea_briefing")
	if err != nil {
		t.Fatalf("Catalog_Get: %v", err)
	}

	for _, missing := range preview.Entry.RequiresCredentials {
		if missing == "gmail" {
			t.Fatalf("RequiresCredentials = %v — gmail was pre-seeded as installed but is still "+
				"reported missing; RecipeRegistry is not wired to the real recipes.enabled.json",
				preview.Entry.RequiresCredentials)
		}
	}
	foundSlack := false
	for _, missing := range preview.Entry.RequiresCredentials {
		if missing == "slack" {
			foundSlack = true
		}
	}
	if !foundSlack {
		t.Errorf("RequiresCredentials = %v, want it to still list \"slack\" (never installed in this dataDir)",
			preview.Entry.RequiresCredentials)
	}
}
