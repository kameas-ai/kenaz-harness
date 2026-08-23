// sync_mcp_registry.go — concrete corefleet.MCPRegistryReader /
// MCPRegistryWriter for the installed-MCP sync category
// (fleet-enforcement-truth-01PMZ505 WP07).
//
// Before this file, core/rpc/api.go wired
// corefleet.NewMCPSyncCategory(nil, nil, nil, syncPending) — a nil
// Reader, Writer AND SecretKeys. With Reader nil, Collect()
// (core/fleet/sync_mcp.go:124) always returned an empty payload, so
// the "Installed MCP servers" sync row synced nothing. The panel also
// pointed at the wrong category id (fixed separately in
// SyncPanel.vue). Critically, a nil SecretKeys map disables the
// redaction guard at sync_mcp.go:137
// (`len(it.EnvOverrides) > 0 && len(m.SecretKeys) > 0`), so shipping a
// Reader without a non-nil SecretKeys set would send unredacted secret
// env values off the device. This file supplies the Reader, the
// Writer and the SecretKeys set together — per plan.md Rule 3, id +
// Reader + Writer + SecretKeys land in one commit or none of them.
//
// OSS-first boundary (mirrors sync_categories.go): core/fleet must not
// import the tools view, so the adapter is constructed here in the rpc
// layer, over the same tools.ToolsAPI surface the Tools panel uses.
package rpc

import (
	"context"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
)

// toolsMCPRegistry adapts tools.ToolsAPI — the existing enumeration
// point for installed recipes (core/rpc/views/tools/impl.go:189,
// ListRecipes) — into corefleet.MCPRegistryReader and
// corefleet.MCPRegistryWriter.
type toolsMCPRegistry struct {
	tools tools.ToolsAPI
}

// newToolsMCPRegistry constructs the adapter. t is a.toolsAPI, which is
// never nil in production wiring (newToolsAPI falls back to &stubTools{}
// when the chassis is a test harness with no *core.Core, never to a nil
// interface) — the nil-guards below are defensive, not load-bearing.
func newToolsMCPRegistry(t tools.ToolsAPI) *toolsMCPRegistry {
	return &toolsMCPRegistry{tools: t}
}

// ListInstalled implements corefleet.MCPRegistryReader. For every
// enabled recipe it reports the recipe id, the non-secret per-install
// config as EnvOverrides (e.g. a custom URL override — see
// stringifyConfig), and the full set of the recipe's declared EnvKey
// names as RequiresSecretKeys, so the receiving device knows which
// credentials it must supply before the recipe can start.
//
// RequiresSecretKeys mirrors the SecretKeys redaction set
// mcpRecipeSecretKeys builds below: every EnvKey a recipe declares is
// credential-bearing (recipes.EnvKey has no Secret flag — see the
// field doc at core/mcp/recipes/recipes.go:78), so there is no
// narrower "these specific keys are secret" set to compute per recipe.
func (r *toolsMCPRegistry) ListInstalled() ([]corefleet.InstalledMCP, error) {
	if r == nil || r.tools == nil {
		return nil, nil
	}
	ctx := context.Background()
	listings, err := r.tools.ListRecipes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]corefleet.InstalledMCP, 0, len(listings))
	for _, l := range listings {
		if !l.Enabled {
			continue
		}
		var secretKeys []string
		if len(l.Recipe.EnvKeys) > 0 {
			secretKeys = make([]string, 0, len(l.Recipe.EnvKeys))
			for _, k := range l.Recipe.EnvKeys {
				secretKeys = append(secretKeys, k.Name)
			}
		}
		var overrides map[string]string
		if len(l.Recipe.ConfigOptions) > 0 {
			if cfg, cfgErr := r.tools.RecipeConfig(ctx, l.Recipe.ID); cfgErr == nil && len(cfg) > 0 {
				overrides = stringifyConfig(cfg)
			}
		}
		out = append(out, corefleet.InstalledMCP{
			ID:                 l.Recipe.ID,
			RecipeID:           l.Recipe.ID,
			EnabledState:       l.Enabled,
			EnvOverrides:       overrides,
			RequiresSecretKeys: secretKeys,
		})
	}
	return out, nil
}

// stringifyConfig projects the per-install ConfigOption map (values
// are `any` — recipes.ConfigOption.Kind decides string / boolean /
// enum / directory_list) down to the string-valued subset the sync
// wire shape (map[string]string) can carry. Non-string values (e.g.
// the filesystem recipe's allowed_directories list) are omitted rather
// than stringified, so a round trip never hands InstallRecipe's config
// validation a shape it would reject.
func stringifyConfig(cfg map[string]any) map[string]string {
	out := make(map[string]string, len(cfg))
	for k, v := range cfg {
		if s, ok := v.(string); ok && s != "" {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyInstalled implements corefleet.MCPRegistryWriter. Per the
// interface's doc (core/fleet/sync_mcp.go:58-61), MCPSyncCategory.Apply
// (sync_mcp.go:158) has already enqueued every item with non-empty
// RequiresSecretKeys onto the shared SecretPromptQueue before calling
// this method, so ApplyInstalled must not enqueue again — it only
// decides which items it can safely turn into local state.
//
// An item whose RequiresSecretKeys is non-empty cannot be installed on
// this device sight-unseen: the payload never carries secret values
// (that is what the redaction guard at sync_mcp.go:137 is for), so
// calling InstallRecipe now would either fail on the missing required
// key or spawn a server with no credentials. Those items stay pending —
// the "N MCP servers need credentials" banner (SyncPanel.vue) already
// exists for this, and the user finishes the install from the Tools
// panel once they supply the key.
//
// An item with no required secret keys is safe to merge immediately.
// It goes through InstallRecipe (core/rpc/views/tools/impl.go:242)
// rather than writing recipes.EnabledRecipes directly, so it inherits
// the same allow-list, prereq and Cedar gates a local install gets —
// deliberately not the core/fleet/sites_reconciler.go pattern of a
// direct EnabledStore write, because that reconciler only ever enables
// one first-party recipe id, while a synced item's RecipeID is
// effectively input from another of the user's own devices. Already-
// enabled recipes are skipped, which is what makes repeated Apply
// calls over the same payload idempotent by RecipeID — this category
// has no install-instance identity distinct from the recipe id itself
// (see InstalledMCP.ID's doc).
func (r *toolsMCPRegistry) ApplyInstalled(incoming []corefleet.InstalledMCP) error {
	if r == nil || r.tools == nil || len(incoming) == 0 {
		return nil
	}
	ctx := context.Background()
	already := make(map[string]bool)
	if listings, err := r.tools.ListRecipes(ctx); err == nil {
		for _, l := range listings {
			if l.Enabled {
				already[l.Recipe.ID] = true
			}
		}
	}
	var firstErr error
	for _, item := range incoming {
		if item.RecipeID == "" || len(item.RequiresSecretKeys) > 0 || already[item.RecipeID] {
			continue
		}
		config := make(map[string]any, len(item.EnvOverrides))
		for k, v := range item.EnvOverrides {
			config[k] = v
		}
		if _, err := r.tools.InstallRecipe(ctx, item.RecipeID, nil, config); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// mcpRecipeSecretKeys builds the redaction set for
// corefleet.NewMCPSyncCategory's SecretKeys argument: every EnvKey
// name declared by any loaded recipe (shipped + curated registry +
// user), per spec decision D-4 — recipes.EnvKey has no Secret flag
// (core/mcp/recipes/recipes.go:78 documents every EnvKey as
// credential-bearing), so the decidable secret set is every declared
// name, not a narrower per-recipe subset.
func mcpRecipeSecretKeys(userSource func() []recipes.Recipe) map[string]bool {
	out := make(map[string]bool)
	catalog := mergedRecipeCatalog(userSource)
	if catalog == nil {
		return out
	}
	for _, rec := range catalog.List() {
		for _, k := range rec.EnvKeys {
			if k.Name != "" {
				out[k.Name] = true
			}
		}
	}
	return out
}
