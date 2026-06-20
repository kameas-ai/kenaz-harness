package recipes_test

import (
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// expectedRegistryIDs is the WP06 mandate: these ten ids — and only
// these — make up the curated registry catalog. Drift from the list
// fails the test so a future PR that adds an entry without updating
// the spec gets caught.
var expectedRegistryIDs = []string{
	"github",
	"fetch",
	"brave-search",
	"sqlite",
	"postgres",
	"slack",
	"gmail",
	"outlook",
	"time",
	"git",
	"puppeteer",
	"playwright",
}

func TestRegistrySingletonParses(t *testing.T) {
	cat := recipes.Registry()
	if cat == nil {
		t.Fatal("Registry() returned nil")
	}
	if got, want := len(cat.List()), len(expectedRegistryIDs); got != want {
		t.Fatalf("registry recipe count = %d, want %d", got, want)
	}
}

func TestLoadRegistryReturnsFreshCopy(t *testing.T) {
	a, err := recipes.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	b, err := recipes.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry (second): %v", err)
	}
	if a == b {
		t.Fatal("LoadRegistry should return a fresh *Catalog each call, got identical pointer")
	}
	if len(a.Recipes) != len(b.Recipes) {
		t.Fatalf("recipe count drift: %d vs %d", len(a.Recipes), len(b.Recipes))
	}
}

func TestRegistryAllExpectedIDsPresent(t *testing.T) {
	cat := recipes.Registry()
	for _, id := range expectedRegistryIDs {
		if _, ok := cat.Get(id); !ok {
			t.Errorf("registry missing expected id %q", id)
		}
	}
}

func TestRegistryEverySourceIsRegistry(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if r.Source != recipes.SourceRegistry {
			t.Errorf("recipe %q Source = %q, want %q", r.ID, r.Source, recipes.SourceRegistry)
		}
	}
}

func TestRegistryEveryRecipeValidates(t *testing.T) {
	// LoadRegistry already calls Validate; this test guards against a
	// future refactor that quietly drops the validation step.
	cat := recipes.Registry()
	for _, r := range cat.List() {
		r := r
		if err := r.Validate(); err != nil {
			t.Errorf("recipe %q failed Validate: %v", r.ID, err)
		}
	}
}

func TestRegistryDoesNotShadowShipped(t *testing.T) {
	// C-003: shipped.json is frozen. The registry must not collide on
	// id with any of the 3 shipped filesystem recipes — collisions are
	// permitted by MergedCatalog (registry would shadow shipped) but a
	// silent shadow on the boot path defeats C-003's intent.
	shipped := recipes.Shipped()
	registry := recipes.Registry()
	shippedIDs := map[string]struct{}{}
	for _, r := range shipped.List() {
		shippedIDs[r.ID] = struct{}{}
	}
	for _, r := range registry.List() {
		if _, clash := shippedIDs[r.ID]; clash {
			t.Errorf("registry recipe %q collides with shipped recipe id (C-003)", r.ID)
		}
	}
}

func TestRegistryRoundTripsJSON(t *testing.T) {
	// Round-trip every entry: marshal → unmarshal → re-validate. If
	// any entry has a JSON tag that doesn't survive a round trip
	// (unknown field, type mismatch), this fails.
	cat := recipes.Registry()
	for _, r := range cat.List() {
		r := r
		t.Run(r.ID, func(t *testing.T) {
			t.Parallel()
			raw, err := json.Marshal(&r)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got recipes.Recipe
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("post-roundtrip Validate: %v", err)
			}
			if got.ID != r.ID {
				t.Errorf("ID drift: %q vs %q", got.ID, r.ID)
			}
			if got.DisplayName != r.DisplayName {
				t.Errorf("DisplayName drift: %q vs %q", got.DisplayName, r.DisplayName)
			}
			if len(got.Command) != len(r.Command) {
				t.Errorf("Command length drift: %d vs %d", len(got.Command), len(r.Command))
			}
			if len(got.EnvKeys) != len(r.EnvKeys) {
				t.Errorf("EnvKeys length drift: %d vs %d", len(got.EnvKeys), len(r.EnvKeys))
			}
		})
	}
}

func TestRegistryGitHubRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("github")
	if !ok {
		t.Fatal("github not in registry")
	}
	if r.Category != "search" {
		t.Errorf("Category = %q, want search", r.Category)
	}
	if len(r.EnvKeys) != 1 {
		t.Fatalf("EnvKeys len = %d, want 1", len(r.EnvKeys))
	}
	if r.EnvKeys[0].Name != "GITHUB_TOKEN" {
		t.Errorf("EnvKeys[0].Name = %q, want GITHUB_TOKEN", r.EnvKeys[0].Name)
	}
	if !r.EnvKeys[0].Required {
		t.Error("GITHUB_TOKEN should be required")
	}
}

func TestRegistryFetchRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("fetch")
	if !ok {
		t.Fatal("fetch not in registry")
	}
	if r.Category != "fetch" {
		t.Errorf("Category = %q, want fetch", r.Category)
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("fetch should have no env keys, got %d", len(r.EnvKeys))
	}
}

func TestRegistryBraveSearchRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("brave-search")
	if !ok {
		t.Fatal("brave-search not in registry")
	}
	if r.Category != "search" {
		t.Errorf("Category = %q, want search", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "BRAVE_API_KEY" {
		t.Errorf("EnvKeys = %+v, want one entry named BRAVE_API_KEY", r.EnvKeys)
	}
}

func TestRegistrySQLiteRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("sqlite")
	if !ok {
		t.Fatal("sqlite not in registry")
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
	}
	if len(r.ConfigOptions) != 1 || r.ConfigOptions[0].Name != "db_path" {
		t.Errorf("ConfigOptions = %+v, want one entry named db_path", r.ConfigOptions)
	}
	if !r.ConfigOptions[0].Required {
		t.Error("db_path should be required")
	}
}

func TestRegistryPostgresRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("postgres")
	if !ok {
		t.Fatal("postgres not in registry")
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
	}
	if len(r.EnvKeys) != 1 || r.EnvKeys[0].Name != "POSTGRES_CONNECTION_STRING" {
		t.Errorf("EnvKeys = %+v, want one entry named POSTGRES_CONNECTION_STRING", r.EnvKeys)
	}
}

func TestRegistrySlackRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("slack")
	if !ok {
		t.Fatal("slack not in registry")
	}
	if r.Category != "fetch" {
		t.Errorf("Category = %q, want fetch", r.Category)
	}
	envNames := map[string]bool{}
	for _, e := range r.EnvKeys {
		envNames[e.Name] = true
	}
	if !envNames["SLACK_BOT_TOKEN"] || !envNames["SLACK_APP_TOKEN"] {
		t.Errorf("slack EnvKeys = %+v, want both SLACK_BOT_TOKEN and SLACK_APP_TOKEN", r.EnvKeys)
	}
}

func TestRegistryTimeRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("time")
	if !ok {
		t.Fatal("time not in registry")
	}
	if len(r.EnvKeys) != 0 {
		t.Errorf("time should have no env keys, got %d", len(r.EnvKeys))
	}
}

func TestRegistryGitRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("git")
	if !ok {
		t.Fatal("git not in registry")
	}
	if r.Category != "filesystem" {
		t.Errorf("Category = %q, want filesystem", r.Category)
	}
	if len(r.ConfigOptions) != 1 || r.ConfigOptions[0].Name != "repo_path" {
		t.Errorf("ConfigOptions = %+v, want one entry named repo_path", r.ConfigOptions)
	}
}

func TestRegistryPuppeteerRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("puppeteer")
	if !ok {
		t.Fatal("puppeteer not in registry")
	}
	if r.Warning == "" {
		t.Error("puppeteer should carry a Warning (npm fetch / Chromium download)")
	}
}

func TestRegistryPlaywrightRecipe(t *testing.T) {
	cat := recipes.Registry()
	r, ok := cat.Get("playwright")
	if !ok {
		t.Fatal("playwright not in registry")
	}
	if r.Warning == "" {
		t.Error("playwright should carry a Warning (npm fetch / browser binaries)")
	}
}

func TestRegistryEveryRecipeHasCommand(t *testing.T) {
	cat := recipes.Registry()
	for _, r := range cat.List() {
		if len(r.Command) == 0 || r.Command[0] == "" {
			t.Errorf("recipe %q has empty Command", r.ID)
		}
	}
}

func TestRegistryWiresIntoMergedCatalog(t *testing.T) {
	// Acceptance: tools view's ListRecipes returns shipped + registry
	// merged in source-tagged order. Exercise the same wiring shape
	// the rpc layer uses.
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return recipes.Shipped().List() },
		func() []recipes.Recipe { return recipes.Registry().List() },
		nil,
	)
	merged := mc.Recipes()
	want := len(recipes.Shipped().List()) + len(recipes.Registry().List())
	if len(merged) != want {
		t.Fatalf("merged recipe count = %d, want %d (no id collisions expected)", len(merged), want)
	}
	// Source-tagged: every entry carries its origin source.
	shippedSeen := 0
	registrySeen := 0
	for _, r := range merged {
		switch r.Source {
		case recipes.SourceShipped:
			shippedSeen++
		case recipes.SourceRegistry:
			registrySeen++
		default:
			t.Errorf("recipe %q has unexpected Source %q", r.ID, r.Source)
		}
	}
	if shippedSeen != len(recipes.Shipped().List()) {
		t.Errorf("shipped count in merged = %d, want %d", shippedSeen, len(recipes.Shipped().List()))
	}
	if registrySeen != len(recipes.Registry().List()) {
		t.Errorf("registry count in merged = %d, want %d", registrySeen, len(recipes.Registry().List()))
	}
}
