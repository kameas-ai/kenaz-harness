package connectors

import (
	"log/slog"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// CatalogWithUserRecipes returns a Catalog snapshot function that merges
// shipped + curated-registry + the operator-authored user recipes under
// <dataDir>/mcp/recipes (spec 091 D13/US5): custom connectors baked into
// the workbench image resolve in served mode exactly like curated ids —
// precedence user > registry > shipped, via the same MergedCatalog seam
// the rpc boot path uses.
//
// Access control is unchanged: the KENAZ_MCP_ALLOWLIST whitelist gates
// every id regardless of source. A user recipe id the operator did not
// whitelist stays blocked at install (tools.InstallRecipe's IsAllowed
// gate) and never enters the supervisor's spawn loop; a whitelisted id
// that resolves nowhere stays warn-and-drop unavailable.
//
// The store is loaded on every snapshot call (the supervisor calls once
// at boot); a load failure logs and degrades to shipped + registry —
// never a boot failure.
func CatalogWithUserRecipes(dataDir string, log *slog.Logger) func() *recipes.Catalog {
	if log == nil {
		log = slog.Default()
	}
	store := recipes.NewUserStore(dataDir, log)
	return func() *recipes.Catalog {
		if _, err := store.Load(); err != nil {
			log.Warn("connectors: user recipe load failed — continuing with shipped+registry",
				"err", err.Error())
		}
		mc := recipes.NewMergedCatalog(
			func() []recipes.Recipe { return recipes.Shipped().List() },
			func() []recipes.Recipe { return recipes.Registry().List() },
			store.Recipes,
		)
		return &recipes.Catalog{Version: 1, Recipes: mc.Recipes()}
	}
}
