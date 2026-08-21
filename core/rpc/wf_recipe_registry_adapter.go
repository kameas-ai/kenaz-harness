package rpc

// wf_recipe_registry_adapter.go — automation-actually-runs-01PMZ404
// UNIT-10.
//
// wfRecipeRegistryAdapter is the production implementation of
// wfcatalog.RecipeRegistry. Before this unit,
// wfcatalogpkg.Config.RecipeRegistry was never assigned (the sole
// production construction, core/rpc/api.go's `wfcatalogpkg.New(...)`
// call, set only Store and Scheduler), so
// concreteCatalog.missingCredentials's nil-guard made EVERY mcp_call.server
// report as missing regardless of whether it was actually installed —
// the catalog preview drawer's credential chip could only ever render
// red.
//
// X-2 + X-8 (spec §1.11): the two previously-proposed fixes were both
// wrong. *recipes.MergedCatalog has no Has(string) bool method at all
// (X-2), and even an adapter that added one would answer the WRONG
// question — MergedCatalog is the full shipped+registry+user catalog
// (installed or not), so Has() over it would report every cataloged
// recipe as configured, flipping the chip from always-red to
// always-green (a new lie, not a fix). The correct source is
// recipes.EnabledRecipes — the same "is this server actually installed"
// list core/rpc/api.go's newToolsAPI already loads.
import (
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

type wfRecipeRegistryAdapter struct {
	dataDir string
}

// Has implements wfcatalog.RecipeRegistry. Re-reads
// <DataDir>/mcp/recipes.enabled.json on every call rather than caching a
// boot-time snapshot — the same "read live" rule UNIT-7's
// NetworkAuthorizer and U10's own spec text require, so installing a
// connector turns the chip green without an app restart.
//
// Key space: Recipe.ID "doubles as ... mcp.Tool.Server for tools
// produced by this server" (core/mcp/recipes/recipes.go), and
// mcpCallRunner passes st.Server straight to the same lookup shape
// (core/workflows/runners.go) — so EnabledRecipes.Get(serverName) is the
// right check (spec X-8's "verify the keying" caution resolves yes).
func (a *wfRecipeRegistryAdapter) Has(serverName string) bool {
	if a == nil || a.dataDir == "" {
		return false
	}
	enabled, err := recipes.LoadEnabled(a.dataDir)
	if err != nil || enabled == nil {
		return false
	}
	_, ok := enabled.Get(serverName)
	return ok
}
