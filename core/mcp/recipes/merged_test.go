package recipes_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// stubRecipe is a minimal valid Recipe for merge tests. Validate() is
// not called by MergedCatalog (sources are responsible for their own
// validation), so we can construct partial recipes here freely.
func stubRecipe(id, source, display string) recipes.Recipe {
	return recipes.Recipe{
		ID:          id,
		Source:      source,
		DisplayName: display,
		Command:     []string{"x"},
	}
}

func recipeIDSet(rs []recipes.Recipe) map[string]recipes.Recipe {
	out := make(map[string]recipes.Recipe, len(rs))
	for _, r := range rs {
		out[r.ID] = r
	}
	return out
}

func TestMergedCatalogShippedOnly(t *testing.T) {
	shipped := []recipes.Recipe{
		stubRecipe("filesystem", recipes.SourceShipped, "Filesystem"),
		stubRecipe("filesystem-full", recipes.SourceShipped, "Full"),
	}
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		nil, nil,
	)
	got := mc.Recipes()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for _, r := range got {
		if r.Source != recipes.SourceShipped {
			t.Errorf("recipe %q Source = %q, want %q", r.ID, r.Source, recipes.SourceShipped)
		}
	}
}

func TestMergedCatalogNilShippedSafe(t *testing.T) {
	// All sources nil → empty merged catalog (boot-time state).
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	got := mc.Recipes()
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}

func TestMergedCatalogUserShadowsShipped(t *testing.T) {
	shipped := []recipes.Recipe{stubRecipe("filesystem", recipes.SourceShipped, "Shipped Filesystem")}
	user := []recipes.Recipe{stubRecipe("filesystem", recipes.SourceUser, "User Filesystem")}

	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		nil,
		func() []recipes.Recipe { return user },
	)
	got := mc.Recipes()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (user shadows shipped)", len(got))
	}
	r := got[0]
	if r.DisplayName != "User Filesystem" {
		t.Errorf("DisplayName = %q, want User Filesystem", r.DisplayName)
	}
	if r.Source != recipes.SourceUser {
		t.Errorf("Source = %q, want %q", r.Source, recipes.SourceUser)
	}
}

func TestMergedCatalogRegistryShadowsShipped(t *testing.T) {
	shipped := []recipes.Recipe{stubRecipe("github", recipes.SourceShipped, "Shipped GH")}
	registry := []recipes.Recipe{stubRecipe("github", recipes.SourceRegistry, "Registry GH")}

	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		func() []recipes.Recipe { return registry },
		nil,
	)
	got := mc.Recipes()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Source != recipes.SourceRegistry {
		t.Errorf("Source = %q, want %q", got[0].Source, recipes.SourceRegistry)
	}
	if got[0].DisplayName != "Registry GH" {
		t.Errorf("DisplayName = %q", got[0].DisplayName)
	}
}

func TestMergedCatalogUserShadowsRegistryAndShipped(t *testing.T) {
	// Three-way collision: user wins.
	shipped := []recipes.Recipe{stubRecipe("github", recipes.SourceShipped, "Shipped GH")}
	registry := []recipes.Recipe{stubRecipe("github", recipes.SourceRegistry, "Registry GH")}
	user := []recipes.Recipe{stubRecipe("github", recipes.SourceUser, "User GH")}

	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		func() []recipes.Recipe { return registry },
		func() []recipes.Recipe { return user },
	)
	got := mc.Recipes()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Source != recipes.SourceUser {
		t.Errorf("Source = %q, want user", got[0].Source)
	}
	if got[0].DisplayName != "User GH" {
		t.Errorf("DisplayName = %q", got[0].DisplayName)
	}
}

func TestMergedCatalogAdditiveAcrossSources(t *testing.T) {
	// No id collisions: all entries appear in declaration order
	// (shipped first, then registry, then user).
	shipped := []recipes.Recipe{stubRecipe("filesystem", recipes.SourceShipped, "FS")}
	registry := []recipes.Recipe{stubRecipe("github", recipes.SourceRegistry, "GH")}
	user := []recipes.Recipe{stubRecipe("custom", recipes.SourceUser, "Custom")}

	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		func() []recipes.Recipe { return registry },
		func() []recipes.Recipe { return user },
	)
	got := mc.Recipes()
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "filesystem" || got[1].ID != "github" || got[2].ID != "custom" {
		t.Errorf("order = [%s %s %s], want [filesystem github custom]",
			got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestMergedCatalogGet(t *testing.T) {
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{stubRecipe("alpha", recipes.SourceShipped, "Alpha")}
		},
		nil, nil,
	)
	r, ok := mc.Get("alpha")
	if !ok {
		t.Fatal("Get(alpha) miss")
	}
	if r.DisplayName != "Alpha" {
		t.Errorf("DisplayName = %q", r.DisplayName)
	}
	if _, ok := mc.Get("missing"); ok {
		t.Error("Get(missing) hit")
	}
}

func TestMergedCatalogRecipeBySource(t *testing.T) {
	shipped := []recipes.Recipe{stubRecipe("filesystem", recipes.SourceShipped, "FS")}
	registry := []recipes.Recipe{stubRecipe("github", recipes.SourceRegistry, "GH")}
	user := []recipes.Recipe{
		stubRecipe("custom", recipes.SourceUser, "Custom"),
		// A user-level shadow of github should land under "user", not
		// under "registry", because the merged view is post-shadow.
		stubRecipe("github", recipes.SourceUser, "User GH"),
	}
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return shipped },
		func() []recipes.Recipe { return registry },
		func() []recipes.Recipe { return user },
	)
	if got := mc.RecipeBySource(recipes.SourceShipped); len(got) != 1 || got[0].ID != "filesystem" {
		t.Errorf("shipped slice = %#v", got)
	}
	if got := mc.RecipeBySource(recipes.SourceRegistry); len(got) != 0 {
		t.Errorf("registry slice = %#v, want empty (shadowed by user)", got)
	}
	userSlice := mc.RecipeBySource(recipes.SourceUser)
	if len(userSlice) != 2 {
		t.Fatalf("user slice len = %d, want 2", len(userSlice))
	}
	idx := recipeIDSet(userSlice)
	if _, ok := idx["custom"]; !ok {
		t.Error("custom missing from user slice")
	}
	if r, ok := idx["github"]; !ok {
		t.Error("github missing from user slice")
	} else if r.DisplayName != "User GH" {
		t.Errorf("user github DisplayName = %q", r.DisplayName)
	}
}

func TestMergedCatalogCopyOnWrite(t *testing.T) {
	// Mutating the returned slice must not affect the underlying
	// source or subsequent calls.
	src := []recipes.Recipe{
		stubRecipe("a", recipes.SourceShipped, "A"),
		stubRecipe("b", recipes.SourceShipped, "B"),
	}
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return src },
		nil, nil,
	)

	first := mc.Recipes()
	if len(first) != 2 {
		t.Fatalf("len = %d, want 2", len(first))
	}
	first[0].ID = "tampered"

	second := mc.Recipes()
	if second[0].ID == "tampered" {
		t.Fatal("MergedCatalog.Recipes returned aliased slice; mutation leaked")
	}
	// Source slice itself must also be untouched.
	if src[0].ID != "a" {
		t.Errorf("source slice corrupted: src[0].ID = %q", src[0].ID)
	}
}

func TestMergedCatalogReflectsLatestSnapshot(t *testing.T) {
	// Every call to Recipes pulls the freshest content from each
	// source — UserStore.Reload-then-Recipes wiring relies on this.
	var live []recipes.Recipe
	mc := recipes.NewMergedCatalog(
		nil, nil,
		func() []recipes.Recipe { return live },
	)
	if got := mc.Recipes(); len(got) != 0 {
		t.Errorf("initial len = %d, want 0", len(got))
	}
	live = []recipes.Recipe{stubRecipe("hot", recipes.SourceUser, "Hot")}
	if got := mc.Recipes(); len(got) != 1 || got[0].ID != "hot" {
		t.Errorf("post-mutation = %#v", got)
	}
	live = nil
	if got := mc.Recipes(); len(got) != 0 {
		t.Errorf("post-clear len = %d, want 0", len(got))
	}
}

func TestMergedCatalogSetUserSourceLateBinding(t *testing.T) {
	// Boot wiring case: catalog constructed with nil user source,
	// then UserStore is wired in once Load completes.
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{stubRecipe("filesystem", recipes.SourceShipped, "FS")}
		},
		nil, nil,
	)
	if got := mc.Recipes(); len(got) != 1 {
		t.Fatalf("pre-wire len = %d, want 1", len(got))
	}

	mc.SetUserSource(func() []recipes.Recipe {
		return []recipes.Recipe{
			stubRecipe("filesystem", recipes.SourceUser, "User FS"),
			stubRecipe("custom", recipes.SourceUser, "Custom"),
		}
	})

	got := mc.Recipes()
	if len(got) != 2 {
		t.Fatalf("post-wire len = %d, want 2", len(got))
	}
	idx := recipeIDSet(got)
	if r := idx["filesystem"]; r.Source != recipes.SourceUser {
		t.Errorf("filesystem Source = %q, want user (user shadows shipped)", r.Source)
	}
	if r := idx["custom"]; r.Source != recipes.SourceUser {
		t.Errorf("custom Source = %q, want user", r.Source)
	}
}

func TestMergedCatalogSetRegistrySourceLateBinding(t *testing.T) {
	// WP06 plug-in: catalog constructed with nil registry, then WP06
	// installs the curated registry without re-architecting.
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{stubRecipe("filesystem", recipes.SourceShipped, "FS")}
		},
		nil, nil,
	)
	if got := mc.Recipes(); len(got) != 1 {
		t.Fatalf("pre-wire len = %d, want 1", len(got))
	}

	mc.SetRegistrySource(func() []recipes.Recipe {
		return []recipes.Recipe{stubRecipe("github", recipes.SourceRegistry, "GH")}
	})
	got := mc.Recipes()
	if len(got) != 2 {
		t.Fatalf("post-wire len = %d, want 2", len(got))
	}
}

func TestMergedCatalogSourceFieldDefaultsApplied(t *testing.T) {
	// A source that hands back a Recipe without a Source field set
	// gets the canonical source stamped on the merged view. Lets
	// callers (e.g. shipped.go pre-source-stamp) wire in safely.
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe {
			return []recipes.Recipe{{ID: "bare", Command: []string{"x"}}}
		},
		nil, nil,
	)
	got := mc.Recipes()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Source != recipes.SourceShipped {
		t.Errorf("Source = %q, want shipped (default)", got[0].Source)
	}
}

// ── Org layer (fleet-org-config-inheritance-01NORGX01 WP02) ────────────────

// TestMergedCatalogOrgShadowsUserRegistryShipped proves the org layer wins
// over EVERY other source — not just user, which TestMergedCatalogUserShadowsShipped
// already covers for the pre-existing three-source case. ConflictPolicyOrgWinsReadonly
// requires the org copy to be the one every caller (including OAuth sign-in call
// sites that read recipe.Auth.ClientID) resolves through Get/Recipes.
func TestMergedCatalogOrgShadowsUserRegistryShipped(t *testing.T) {
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return []recipes.Recipe{stubRecipe("slack", recipes.SourceShipped, "Shipped Slack")} },
		func() []recipes.Recipe { return []recipes.Recipe{stubRecipe("slack", recipes.SourceRegistry, "Registry Slack")} },
		func() []recipes.Recipe { return []recipes.Recipe{stubRecipe("slack", recipes.SourceUser, "User Slack")} },
	)
	mc.SetOrgRecipes([]recipes.Recipe{stubRecipe("slack", recipes.SourceOrg, "Org Slack")})

	got := mc.Recipes()
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (org shadows, not appends)", len(got))
	}
	if got[0].Source != recipes.SourceOrg || got[0].DisplayName != "Org Slack" {
		t.Errorf("got %+v, want the org copy to win", got[0])
	}
	r, ok := mc.Get("slack")
	if !ok || r.Source != recipes.SourceOrg {
		t.Errorf("Get(slack) = %+v, %v — want the org copy", r, ok)
	}
}

// TestMergedCatalogOrgSourceDefaultsApplied mirrors
// TestMergedCatalogSourceFieldDefaultsApplied for the org layer: a caller
// that forgets to stamp Source still gets tagged "org" on the merged view.
func TestMergedCatalogOrgSourceDefaultsApplied(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	mc.SetOrgRecipes([]recipes.Recipe{{ID: "acme-internal", Command: []string{"x"}}})
	got := mc.Recipes()
	if len(got) != 1 || got[0].Source != recipes.SourceOrg {
		t.Fatalf("got %+v, want one recipe stamped SourceOrg", got)
	}
}

// TestMergedCatalogSetOrgRecipesNilClears proves the "removing fleet
// reverts to local-only" success criterion: SetOrgRecipes(nil) drops the
// overlay entirely and the member's own (never-deleted) recipe reappears
// in the merged view.
func TestMergedCatalogSetOrgRecipesNilClears(t *testing.T) {
	mc := recipes.NewMergedCatalog(
		nil, nil,
		func() []recipes.Recipe { return []recipes.Recipe{stubRecipe("slack", recipes.SourceUser, "User Slack")} },
	)
	mc.SetOrgRecipes([]recipes.Recipe{stubRecipe("slack", recipes.SourceOrg, "Org Slack")})
	if r, _ := mc.Get("slack"); r.Source != recipes.SourceOrg {
		t.Fatalf("precondition: org should be winning, got Source=%q", r.Source)
	}

	mc.SetOrgRecipes(nil)
	r, ok := mc.Get("slack")
	if !ok || r.Source != recipes.SourceUser || r.DisplayName != "User Slack" {
		t.Errorf("after SetOrgRecipes(nil), Get(slack) = %+v, %v — want the member's own recipe restored", r, ok)
	}
}

// TestMergedCatalogSetOrgRecipesDefensiveCopy proves a caller mutating its
// own slice after SetOrgRecipes cannot retroactively change what was
// installed — SetOrgRecipes must copy, not alias.
func TestMergedCatalogSetOrgRecipesDefensiveCopy(t *testing.T) {
	mc := recipes.NewMergedCatalog(nil, nil, nil)
	orgRecipes := []recipes.Recipe{stubRecipe("slack", recipes.SourceOrg, "Org Slack")}
	mc.SetOrgRecipes(orgRecipes)

	orgRecipes[0].DisplayName = "MUTATED"

	r, ok := mc.Get("slack")
	if !ok || r.DisplayName != "Org Slack" {
		t.Errorf("Get(slack).DisplayName = %q, ok=%v — want unaffected by post-call mutation of the caller's slice", r.DisplayName, ok)
	}
}
