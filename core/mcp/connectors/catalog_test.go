package connectors

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// writeUserRecipe stages a valid operator-authored recipe under
// <dataDir>/mcp/recipes/<id>.yaml — the layout the workbench image bakes
// for custom connectors (spec 091 D13/US5).
func writeUserRecipe(t *testing.T, dataDir, id string) {
	t.Helper()
	dir := filepath.Join(dataDir, "mcp", "recipes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir user recipes dir: %v", err)
	}
	yaml := "id: " + id + "\n" +
		"display_name: Chalk AI\n" +
		"category: data\n" +
		"transport: http\n" +
		"url: https://api.chalk.ai/mcp\n"
	if err := os.WriteFile(filepath.Join(dir, id+".yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write user recipe: %v", err)
	}
}

// TestSupervisor_UserRecipeResolvesWhenWhitelisted verifies the served
// supervisor's merged catalog includes the user-recipe source: a custom
// connector baked into <dataDir>/mcp/recipes resolves and spawns when —
// and only when — its id is whitelisted.
func TestSupervisor_UserRecipeResolvesWhenWhitelisted(t *testing.T) {
	dataDir := t.TempDir()
	writeUserRecipe(t, dataDir, "chalk-ai")

	pool := &fakePool{}
	sup := NewSupervisor(SupervisorConfig{
		Provisioning: Provisioning{Provisioned: true, IDs: []string{"chalk-ai"}},
		Getenv:       func(string) string { return "" },
		Catalog:      CatalogWithUserRecipes(dataDir, slog.Default()),
	})
	sup.SetPool(pool)
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	specs := pool.snapshot()
	if len(specs) != 1 || specs[0].Name != "chalk-ai" {
		t.Fatalf("opened specs = %+v, want the chalk-ai user recipe", specs)
	}
	if specs[0].URL != "https://api.chalk.ai/mcp" || specs[0].Transport != "http" {
		t.Errorf("spec = %+v, want the user recipe's transport/url", specs[0])
	}
	if !specs[0].IsolateEnv {
		t.Error("user recipe spawned without IsolateEnv — custom connectors get the same isolation")
	}
	st := sup.States()[0]
	if !st.Available || !st.Enabled || st.SpawnState != SpawnStateOK || st.DisplayName != "Chalk AI" {
		t.Errorf("state = %+v, want available+enabled+ok", st)
	}
}

// TestSupervisor_UserRecipeIgnoredWhenNotWhitelisted verifies the
// whitelist still gates user recipes: a baked custom connector whose id
// is NOT in KENAZ_MCP_ALLOWLIST never spawns and surfaces no state.
func TestSupervisor_UserRecipeIgnoredWhenNotWhitelisted(t *testing.T) {
	dataDir := t.TempDir()
	writeUserRecipe(t, dataDir, "chalk-ai")

	pool := &fakePool{}
	sup := NewSupervisor(SupervisorConfig{
		// Whitelist names something else entirely; chalk-ai exists only
		// in the user-recipe dir.
		Provisioning: Provisioning{Provisioned: true, IDs: []string{"ghost"}},
		Getenv:       func(string) string { return "" },
		Catalog:      CatalogWithUserRecipes(dataDir, slog.Default()),
	})
	sup.SetPool(pool)
	if err := sup.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(pool.snapshot()) != 0 {
		t.Error("non-whitelisted user recipe spawned")
	}
	for _, st := range sup.States() {
		if st.ID == "chalk-ai" {
			t.Error("non-whitelisted user recipe surfaced a state")
		}
	}
}

// TestCatalogWithUserRecipes_MissingDirDegrades verifies a dataDir with
// no user-recipe directory still yields the shipped+registry view.
func TestCatalogWithUserRecipes_MissingDirDegrades(t *testing.T) {
	cat := CatalogWithUserRecipes(t.TempDir(), slog.Default())()
	if cat == nil || len(cat.Recipes) == 0 {
		t.Fatal("catalog empty — shipped+registry merge lost without user recipes")
	}
	if _, ok := cat.Get("github"); !ok {
		t.Error("curated registry recipe missing from merged view")
	}
}
