// Package tools is the view-scoped surface that drives the harness
// Tools panel: it lists shipped MCP recipes, installs / uninstalls
// them against the running *stdio.Pool, and forwards live status
// snapshots to the frontend.
//
// Wire shapes are deliberately small so the JSON payload that crosses
// the Wails boundary stays compact. RecipeListing copies its embedded
// recipes.Recipe by value rather than sharing a pointer with the
// catalog singleton so a client mutation cannot drift the in-process
// catalog underneath other readers.
package tools

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
)

// RecipeListing is the per-recipe row returned from ListRecipes. The
// embedded recipes.Recipe is the catalog metadata (display name, env
// keys, docs URL); Enabled / Status / KeysPresent are the harness-side
// overlay derived from the persisted enabled list, the live pool, and
// the secrets backend respectively.
//
// Status is a zero value when the recipe is not enabled. Frontend
// renderers branch on Enabled to decide whether to surface status
// fields.
type RecipeListing struct {
	Recipe      recipes.Recipe     `json:"recipe"`
	Enabled     bool               `json:"enabled"`
	Status      stdio.RecipeStatus `json:"status"`
	KeysPresent bool               `json:"keysPresent"`
}

// FSAccessResult is the wire shape returned by RequestAdditionalAllowedDir
// and the Tools_RequestAdditionalAllowedDir Wails binding. Kept here so
// the binding file can reference it without importing the impl package.
type FSAccessResult struct {
	Granted  bool   `json:"granted"`
	Expanded string `json:"expanded"`
	Message  string `json:"message"`
}

// ToolsAPI is the view-scoped surface backing /tools. Implementations
// MUST be safe for concurrent use; the rpc layer holds a single API
// pointer for the lifetime of the harness and fan-outs from the Wails
// binding hit it directly.
type ToolsAPI interface {
	// ListRecipes returns every shipped recipe overlaid with its
	// enabled-state, live-pool status, and a keys-resolvable hint.
	ListRecipes(ctx context.Context) ([]RecipeListing, error)
	// InstallRecipe enables a recipe, writes its env keys to the
	// keychain backend, spawns the server through the pool, and
	// returns the live status snapshot. The env map is zeroed before
	// return so the caller's plaintext frame never escapes the call.
	//
	// config carries per-install ConfigOption values (filesystem
	// allowed_directories, future boolean toggles). Validation is
	// done by Kind: required options must be present + well-typed,
	// directory_list paths run through ValidateAllowedDir, missing
	// optional options fall back to the recipe's declared Default.
	// Recipes with no ConfigOptions ignore this argument.
	InstallRecipe(ctx context.Context, id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error)
	// SignInRecipe runs the MCP OAuth authorization flow for a remote
	// recipe (Auth.Kind == mcp_oauth), opening the system browser, persists
	// the resulting bearer token to the keychain, and respawns the recipe
	// authenticated. Errors clearly when the recipe is not an OAuth recipe
	// or has no configured client_id.
	SignInRecipe(ctx context.Context, id string) (stdio.RecipeStatus, error)
	// UninstallRecipe stops the running server (SIGTERM grace) and
	// removes the entry from the persisted enabled list. Keychain
	// entries persist — explicit deletion goes through ForgetRecipeKey.
	UninstallRecipe(ctx context.Context, id string) error
	// ForgetRecipeKey removes one keychain entry for a recipe. The
	// recipe stays enabled (or not) — only the secret is purged.
	ForgetRecipeKey(ctx context.Context, id, envName string) error
	// RecipeStatus returns the live status snapshot for one recipe.
	// A not-installed recipe returns {Enabled: false, State: "stopped"}
	// so the frontend can render a uniform row regardless.
	RecipeStatus(ctx context.Context, id string) (stdio.RecipeStatus, error)
	// RecipeConfig returns the persisted per-install ConfigOption map
	// for an enabled recipe (e.g. {"allowed_directories": [...]} for
	// the filesystem recipe). The frontend reads this to resolve
	// workspace paths and to pre-fill the edit-config modal. Returns
	// an empty map when the recipe is not enabled — no error in that
	// case so the UI can render a uniform row.
	RecipeConfig(ctx context.Context, id string) (map[string]any, error)

	// RequestAdditionalAllowedDir is the runtime "expand filesystem
	// access" flow. It validates the path, fires the Cedar interactive
	// prompt, and — on approval — appends the path to the recipe's
	// Config["allowed_directories"] and re-spawns the MCP server.
	// Returns (true, canonicalPath, nil) on success and
	// (false, "", err) on rejection or error. The path is
	// canonicalised before returning so the model can retry with the
	// resolved form.
	RequestAdditionalAllowedDir(ctx context.Context, recipeID, path, reason string) (granted bool, expanded string, err error)
}
