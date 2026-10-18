package rpc

// api_mcp_recipe_source_test.go — connector-lifecycle-truth-01PMZ303
// UNIT-11 / FR-007 / AC-007.
//
// Before this unit, RecipeListing had four fields — Recipe, Enabled,
// Status, KeysPresent — and no source discriminator, even though the
// underlying recipes.Recipe.Source field was already being stamped by
// the shipped+registry+user merge (recipes.MergedCatalog.Recipes(),
// threaded through core/rpc/api.go's mergedRecipeCatalog). Recipe.Source
// carries `json:"-"` (it must never round-trip through registry.json /
// shipped.json parsing), which meant the computed value was silently
// discarded at the RPC boundary: KenazToolsPanel.vue's sourceBadge /
// sourceBadgeClass hardcoded "shipped" for every row because the wire
// shape genuinely gave it nothing else to read.
//
// This test drives the REAL merged catalog end to end — rpc.New over a
// real Core, the real embedded recipes.Shipped()/recipes.Registry()
// catalogs, and one real user-store import — and asserts ListRecipes
// (what Tools_ListRecipes serves) reports three distinct, correct
// Source values. Per CLAUDE.md blind spot #1 / spec R-2 ("a Go test
// that constructs RecipeListing values by hand" is a false pass), this
// must go through mergedRecipeCatalog + ListRecipes, not a hand-built
// RecipeListing literal.
import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// TestListRecipes_SourceDiscriminator_ThreeDistinctValues is AC-007's Go
// half: a shipped recipe (fleet-sites, from recipes.Shipped()), a
// registry recipe (any entry from recipes.Registry() — the catalog has
// 115), and a freshly-imported user recipe must each report the correct
// recipes.Source* value through Tools_ListRecipes.
//
// Mutation (spec §7 AC-007): make the merge tag every arm "shipped", at
// one site only. This test drives the mcpLiveCatalog / ListRecipes path
// (core/rpc/api.go's newToolsAPI Config{Catalog: mcpLiveCatalog{...}}),
// which is the ONLY site KenazToolsPanel.vue's badge reads (spec §1.5's
// correction — the MCP health-snapshot path does not carry Source at
// all). A mutation that stops mergedRecipeCatalog from stamping
// Source (or that reverts RecipeListing.Source / the `Source: r.Source`
// assignment in ListRecipes) must fail this test.
func TestListRecipes_SourceDiscriminator_ThreeDistinctValues(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	ctx := context.Background()

	// Import one user recipe through the real import surface — the same
	// path api_mcp_user_source_test.go's tests drive — so its Source is
	// stamped by the same merge the shipped/registry rows go through,
	// not hand-set.
	imp := api.MCPImport()
	if imp == nil {
		t.Fatalf("api.MCPImport() is nil — import surface not wired for a real DataDir")
	}
	const userPayload = `{"mcpServers":{"z303-source-probe":{"command":"z303-cmd","args":["x"]}}}`
	if _, err := imp.ImportClaudeDesktopConfig(ctx, mcpview.ImportRequest{
		RawJSON: userPayload,
		DryRun:  false,
	}); err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}

	listed, err := api.Tools().ListRecipes(ctx)
	if err != nil {
		t.Fatalf("Tools().ListRecipes: %v", err)
	}

	bySource := make(map[string]string, len(listed)) // id -> source
	for _, l := range listed {
		bySource[l.Recipe.ID] = l.Source
	}

	// Shipped: fleet-sites ships in recipes.Shipped() (shipped.json).
	if got := bySource["fleet-sites"]; got != recipes.SourceShipped {
		t.Errorf("fleet-sites Source = %q, want %q", got, recipes.SourceShipped)
	}
	// Registry: any real registry.json entry proves the registry arm.
	// "notion" is one of the 36 mcp_oauth recipes cited throughout the
	// spec and is stable catalog content, not test-authored data.
	if got := bySource["notion"]; got != recipes.SourceRegistry {
		t.Errorf("notion Source = %q, want %q", got, recipes.SourceRegistry)
	}
	// User (imported arm): the recipe this test just pasted in via
	// ImportClaudeDesktopConfig lands under <DataDir>/mcp/recipes/_imports/
	// and is tagged recipes.SourceImported — the sibling of SourceUser
	// (recipes.go:293-307) for the two UserStore-backed origins.
	if got := bySource["z303-source-probe"]; got != recipes.SourceImported {
		t.Errorf("z303-source-probe Source = %q, want %q", got, recipes.SourceImported)
	}

	// All three values must actually be distinct strings, not just
	// individually correct — guards against a constants collapse
	// (e.g. SourceRegistry accidentally aliased to SourceShipped).
	seen := map[string]bool{
		bySource["fleet-sites"]:       true,
		bySource["notion"]:            true,
		bySource["z303-source-probe"]: true,
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct Source values across shipped/registry/user, got %d: %v", len(seen), seen)
	}
}
