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
// keys, docs URL); Enabled / Status / KeysPresent / Source are the
// harness-side overlay derived from the persisted enabled list, the live
// pool, the secrets backend, and the merged catalog respectively.
//
// Status is a zero value when the recipe is not enabled. Frontend
// renderers branch on Enabled to decide whether to surface status
// fields.
type RecipeListing struct {
	Recipe      recipes.Recipe     `json:"recipe"`
	Enabled     bool               `json:"enabled"`
	Status      stdio.RecipeStatus `json:"status"`
	KeysPresent bool               `json:"keysPresent"`
	// Source is the catalog layer that produced this row's Recipe:
	// recipes.SourceShipped | SourceRegistry | SourceUser | SourceImported
	// | SourceOrg. Populated from recipes.Recipe.Source, which itself
	// carries `json:"-"` (it must never round-trip through the on-disk
	// YAML/JSON recipe-definition codecs — see that field's doc comment
	// in core/mcp/recipes/recipes.go). RecipeListing is a dedicated,
	// deliberately-small wire type for this one RPC response, so
	// re-exposing the value here at the top level does not touch that
	// on-disk contract or any other endpoint that returns a bare
	// recipes.Recipe (e.g. SaveCustomRecipe).
	//
	// fleet-generic-sync-framework-01NSYNC02 WP03: this closes the exact
	// gap the frontend's KenazToolsPanel.vue sourceBadge() heuristic was
	// working around ("BACKEND GAP: The wire shape does not yet carry a
	// `source` discriminator") — most importantly, it lets the UI render
	// a "Provisioned by your org" read-only badge for SourceOrg rows
	// (fleet-org-config-inheritance-01NORGX01's org-provisioned MCP
	// servers), which had no wire path to the frontend before this field.
	Source string `json:"source"`
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

	// CheckRecipePrereqs inspects the recipe's command and returns any
	// runtimes (uv/uvx, npx/node) that are not present in $PATH. An
	// empty slice means all prerequisites are satisfied. The frontend
	// calls this when the install dialog opens to surface a friendly
	// "install X first" message before the user even clicks Install.
	CheckRecipePrereqs(ctx context.Context, id string) ([]MissingPrereq, error)

	// BeginDeviceAuth starts the RFC 8628 device-authorization flow for a
	// recipe whose primary_auth == "device_code". It posts to the provider's
	// device-authorization endpoint and returns display-safe fields (user_code
	// + verification_uri + expires_in). The opaque device_code is kept in
	// memory server-side and must never cross the Wails RPC boundary.
	// Call PollDeviceAuth after showing the user_code to the user.
	BeginDeviceAuth(ctx context.Context, id string) (DeviceAuthBeginResult, error)

	// PollDeviceAuth polls the provider's token endpoint for the device auth
	// session started by BeginDeviceAuth for the same recipe id. It blocks
	// until the user approves, the code expires, the user denies, or ctx is
	// cancelled. On success the token is persisted to the credstore (it is
	// NEVER returned via any RPC value) and the recipe is installed/respawned
	// so the bearer takes effect immediately. Returns the live RecipeStatus.
	PollDeviceAuth(ctx context.Context, id string) (stdio.RecipeStatus, error)

	// PlaceRecipeFile copies srcPath into the target path declared by the
	// recipe's file prereq (e.g. ~/.gmail-mcp/gcp-oauth.keys.json). The
	// harness creates the target directory (0700) if it does not exist, and
	// writes the file with mode 0600 so credentials stay owner-readable only.
	//
	// Validation rules (security boundary):
	//   - recipeID must be a known recipe that HAS a file prereq entry in
	//     recipeFilePrereqs. Unknown recipes and recipes without file prereqs
	//     are rejected with a clear error — this is NOT a generic file-copy
	//     primitive.
	//   - The destination is always exactly the recipe's declared
	//     FileSetupGuide.TargetPath. The caller cannot specify an arbitrary dst.
	//   - srcPath must be non-empty; the file must be readable by the harness.
	//
	// Returns nil on success; a descriptive error otherwise.
	PlaceRecipeFile(ctx context.Context, recipeID, srcPath string) error
}
