package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// AuditEmitter is the narrow seam the tools view calls when emitting
// recipe lifecycle events. Mirrors the toolloop's AuditEmitter shape
// (kind + attrs map) so the rpc layer can wire one process-wide
// emitter into both surfaces once core/event materialises a real
// emitter constructor. nil-tolerant.
type AuditEmitter interface {
	Emit(ctx context.Context, kind string, attrs map[string]any)
}

// KeychainWriter is the small write surface the install path needs.
// Implementations stage the plaintext into the OS keychain (so it
// survives restart) and into the in-memory secrets backend (so the
// resolver finds it without an OS round-trip). The plaintext slice
// is zeroed by the implementation before return — see the
// rpc.keychainWriter for the canonical shape.
type KeychainWriter interface {
	Write(ctx context.Context, locator string, plaintext []byte) error
}

// KeychainForgetter is the deletion counterpart to KeychainWriter.
// secrets.Backend has no Delete method (resolver-only contract); the
// rpc layer adapts an OS-keychain Delete + in-memory backend clear
// behind this interface so the view stays decoupled.
type KeychainForgetter interface {
	Forget(ctx context.Context, locator string) error
}

// PoolController is the dynamic-add/remove subset of *stdio.Pool the
// view calls. Defining it as an interface keeps impl_test.go from
// having to spawn a real subprocess: the test fakes this surface and
// drives the recipe-state assertions directly.
type PoolController interface {
	OpenOne(ctx context.Context, spec coremcp.ServerSpec) error
	CloseOne(ctx context.Context, id string) error
	RecipeStatus(id string) (stdio.RecipeStatus, bool)
	// ServerTools returns the tool list for the named server, or nil
	// when the server is not in the pool. Used by the pre-seed path to
	// discover which tools need Cedar permit snippets.
	ServerTools(id string) []coremcp.Tool
}

// Compile-time witness: *stdio.Pool satisfies PoolController.
var _ PoolController = (*stdio.Pool)(nil)

// snippetNameRE is the validation regex for Cedar snippet filenames
// per cedar WP09: ^[a-z][a-z0-9_]{0,127}\.cedar$
var snippetNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}\.cedar$`)

// sanitizeSnippetID converts an arbitrary string (recipe id + "__" +
// tool name) into a snippet filename that satisfies snippetNameRE.
// Non-alphanumeric runes are replaced with '_'; the result is
// lower-cased; a leading non-alpha rune gets a "t_" prefix.
func sanitizeSnippetID(raw string) string {
	out := make([]rune, 0, len(raw))
	for _, r := range strings.ToLower(raw) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "t_.cedar"
	}
	// Ensure first rune is alpha.
	if !unicode.IsLetter(out[0]) {
		out = append([]rune{'t', '_'}, out...)
	}
	// Trim to max 128 chars before the .cedar suffix.
	if len(out) > 128 {
		out = out[:128]
	}
	return string(out) + ".cedar"
}

// Config bundles the dependencies the API constructor needs. Every
// field except Catalog and Enabled is nil-tolerant — tests that only
// exercise the catalog walk can leave the rest unset.
type Config struct {
	Catalog   *recipes.Catalog
	Enabled   *recipes.EnabledRecipes
	Pool      PoolController
	Secrets   secrets.Backend
	Keychain  KeychainWriter
	Forgetter KeychainForgetter
	DataDir   string
	// WorkspaceDir is the core's RESOLVED agent workspace (spec 089) —
	// the granted /workspace mount in a workbench, <DataDir>/agent-workspace
	// otherwise. A recipe default of exactly "${DATA_DIR}/agent-workspace"
	// resolves here (plan D4), so the shipped filesystem recipe grants the
	// directory the agent actually works in. Empty falls back to the
	// DataDir join (nil-core test path).
	WorkspaceDir string
	Audit        AuditEmitter
	// Gate is the Cedar policy gate for InstallRecipe (WP10). nil =
	// default-permit (best-effort: a Cedar bug never blocks the chassis).
	Gate cedar.Gate
	// CedarPolicy is the optional Cedar policy API used to pre-seed
	// tool permit snippets after a successful recipe install (WP11).
	// When nil the pre-seed step is silently skipped (backwards-compatible).
	CedarPolicy cedarpolicy.CedarPolicyAPI
	// PromptRegistry is the Cedar interactive-prompt registry used by
	// RequestAdditionalAllowedDir. When nil the call returns an error.
	PromptRegistry *cedar.Registry
}

// API is the concrete ToolsAPI implementation.
//
// mu serialises the install / uninstall / forget paths so a
// concurrent toggle storm from the frontend cannot corrupt the
// EnabledRecipes file or race the pool's add/remove on the same id.
// RecipeStatus / ListRecipes do not take the lock — they are
// read-only and idempotent.
type API struct {
	cfg Config
	mu  sync.Mutex
}

// New constructs the API. Catalog and Enabled are required — without
// them the view has nothing to list. Other fields are nil-tolerant.
func New(cfg Config) *API {
	return &API{cfg: cfg}
}

// emit sends an audit event when an emitter is wired. nil-safe so
// tests and the pre-real-emitter rpc path stay quiet.
func (a *API) emit(ctx context.Context, kind string, attrs map[string]any) {
	if a == nil || a.cfg.Audit == nil {
		return
	}
	a.cfg.Audit.Emit(ctx, kind, attrs)
}

// ListRecipes walks the catalog, overlays the persisted enabled
// state, the live pool status, and a keys-resolvable hint per
// recipe, and returns the joined list in catalog declaration order.
func (a *API) ListRecipes(ctx context.Context) ([]RecipeListing, error) {
	if a.cfg.Catalog == nil {
		return nil, errors.New("tools: no catalog configured")
	}
	all := a.cfg.Catalog.List()
	out := make([]RecipeListing, 0, len(all))
	for _, r := range all {
		listing := RecipeListing{Recipe: r}
		if a.cfg.Enabled != nil {
			if _, ok := a.cfg.Enabled.Get(r.ID); ok {
				listing.Enabled = true
			}
		}
		if a.cfg.Pool != nil {
			if status, ok := a.cfg.Pool.RecipeStatus(r.ID); ok {
				listing.Status = status
			}
		}
		listing.KeysPresent = a.keysResolvable(ctx, r)
		out = append(out, listing)
	}
	return out, nil
}

// InstallRecipe is the FR-021/FR-003 install path. It validates the
// env input against the recipe's required keys, validates each
// declared ConfigOption against its Kind (directory_list paths
// canonicalise + deny-list-check; boolean/string type-check; missing
// optional options fall back to Default), stages every env key in
// the keychain, persists the enabled-list update with the resolved
// config, spawns the server via the pool, zeroes the input env to
// scrub plaintext from the caller's frame, and returns the live
// status snapshot.
//
// On any failure the function unwinds in reverse: spawned process
// is closed (best-effort), enabled-list save is rolled back, and
// the error surfaces to the caller. The keychain entries written
// before the failure point persist — that matches the spec's "keys
// stick across uninstall" rule, and a follow-up install will
// overwrite them.
//
// ${DATA_DIR} substitution: Default values for directory_list
// options are templated, so a recipe can ship a default like
// "${DATA_DIR}/agent-workspace" that resolves to the harness data
// directory at install time. The substitution happens BEFORE
// validation so the deny-list check sees the canonical resolved
// path.
//
// EnsureWorkspace: when the resolved allowed_directories list
// includes the canonical default workspace path, the harness
// creates it (idempotent) before validation runs — without this,
// a fresh install would always reject the default path with
// ErrPathNotFound.
func (a *API) InstallRecipe(ctx context.Context, id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Catalog == nil {
		return stdio.RecipeStatus{}, errors.New("tools: no catalog configured")
	}
	recipe, ok := a.cfg.Catalog.Get(id)
	if !ok {
		return stdio.RecipeStatus{}, fmt.Errorf("%w: %q", recipes.ErrRecipeNotFound, id)
	}

	// Allow-list gate (spec 091 FR-004): install is an enable path, so a
	// recipe outside the active allow-list must be refused before any
	// side effect. Host mode with no fleet allow-list is unrestricted
	// (nil filter); served mode is block-all unless whitelisted.
	if !recipes.IsAllowed(id) {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: recipe %q is not permitted by the active MCP allow-list", id)
	}

	// Validate required env keys before any side-effects.
	for _, key := range recipe.EnvKeys {
		if !key.Required {
			continue
		}
		v, present := env[key.Name]
		if !present || v == "" {
			return stdio.RecipeStatus{}, fmt.Errorf("tools: required env key %q for recipe %q is missing or empty", key.Name, id)
		}
	}

	// Validate + materialise the per-install config against the
	// recipe's declared ConfigOptions. resolvedConfig is what we
	// persist on EnabledRecipe.Config and feed into ToServerSpec
	// for ${VAR} expansion.
	resolvedConfig, err := a.resolveConfig(recipe, config)
	if err != nil {
		return stdio.RecipeStatus{}, err
	}

	// Prereq pre-flight BEFORE any keychain write or enabled-list mutation:
	// detect missing runtimes (uv/uvx, npx/node) and required files (e.g.
	// Gmail OAuth credentials) up front so a missing prereq returns a
	// friendly, actionable error WITHOUT staging the user's secrets in the OS
	// keychain for a recipe that never installs. Pass the recipe ID so
	// file-based prereqs (recipeFilePrereqs) are also consulted.
	if missing := CheckPrereqs(recipe.Command, recipe.ID); len(missing) > 0 {
		return stdio.RecipeStatus{}, PrereqError(missing)
	}

	// Always zero the caller's plaintext frame before return — even
	// on the failure paths below — so a stack trace never carries
	// the credential.
	defer func() {
		for k := range env {
			env[k] = ""
		}
	}()

	// Stage every keychain entry the user supplied. Iterate over
	// recipe.EnvKeys (not the input map) so a malicious input with
	// extra keys cannot smuggle data into the keychain namespace.
	if a.cfg.Keychain != nil {
		for _, key := range recipe.EnvKeys {
			plaintext, present := env[key.Name]
			if !present || plaintext == "" {
				continue
			}
			locator := recipes.KeychainLocator(id, key.Name)
			buf := []byte(plaintext)
			if err := a.cfg.Keychain.Write(ctx, locator, buf); err != nil {
				return stdio.RecipeStatus{}, fmt.Errorf("tools: write keychain %q: %w", locator, err)
			}
		}
	}

	// Update the enabled list and persist to disk before spawning so
	// a crash between spawn-success and Save does not leave the user
	// with a running but unpersisted recipe.
	if a.cfg.Enabled == nil {
		return stdio.RecipeStatus{}, errors.New("tools: no enabled-recipes store configured")
	}
	entry := recipes.EnabledRecipe{
		ID:              id,
		EnabledAt:       time.Now().UTC(),
		SamplingEnabled: recipe.SamplingPolicy.Default,
		EnvAuditHash:    recipes.EnvAuditHash(recipe),
		Config:          resolvedConfig,
	}
	a.cfg.Enabled.Add(entry)
	if a.cfg.DataDir != "" {
		if err := a.cfg.Enabled.Save(a.cfg.DataDir); err != nil {
			a.cfg.Enabled.Remove(id)
			return stdio.RecipeStatus{}, fmt.Errorf("tools: save enabled list: %w", err)
		}
	}

	// Resolve env via the secrets backend (which now reads the keys
	// we just wrote) and spawn through the pool. The resolved map is
	// passed by value into ToServerSpec; ServerSpec.Env is the only
	// thing that lands on exec.Cmd.Env, and the spawn path's
	// mergeEnv copies it into the child's environment without
	// touching the data directory.
	if a.cfg.Secrets == nil {
		return stdio.RecipeStatus{}, errors.New("tools: no secrets backend configured")
	}
	resolved, err := recipes.ResolveEnv(ctx, a.cfg.Secrets, recipe)
	if err != nil {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, fmt.Errorf("tools: resolve env: %w", err)
	}
	spec := recipe.ToServerSpec(resolved, resolvedConfig)

	// For OAuth recipes (remote servers signed in via the MCP authorization
	// flow), inject the bearer from the stored credential, refreshing if
	// needed. No-op for non-OAuth recipes; deferred (server unauthenticated)
	// when the user has not signed in yet.
	if err := a.injectOAuthBearer(ctx, recipe, &spec); err != nil {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, err
	}

	if a.cfg.Pool == nil {
		return stdio.RecipeStatus{}, errors.New("tools: no pool configured")
	}

	// Cedar gate (WP10) — best-effort: nil gate = permit. A Cedar
	// evaluation error (engine not loaded, etc.) must never block the
	// install path; CheckRecipeSpawn logs the warning internally and
	// returns nil when g is nil.
	command := ""
	if len(recipe.Command) > 0 {
		command = recipe.Command[0]
	}
	// Transport field lands in WP03/WP04; current recipes are all stdio.
	transport := "stdio"
	if err := cedar.CheckRecipeSpawn(ctx, a.cfg.Gate, id, command, transport); err != nil {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, fmt.Errorf("tools: cedar gate denied recipe %q: %w", id, err)
	}

	// Idempotency: if the pool still holds an entry under this id (a
	// re-install with config changes, or a prior crash that left the
	// pool dirty), evict it before spawning. CloseOne returning
	// ErrServerNotFound is the happy path on a clean install.
	if err := a.cfg.Pool.CloseOne(ctx, id); err != nil && !errors.Is(err, stdio.ErrServerNotFound) {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, fmt.Errorf("tools: replace existing pool entry for %q: %w", id, err)
	}
	if err := a.cfg.Pool.OpenOne(ctx, spec); err != nil {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, fmt.Errorf("tools: open recipe %q: %w", id, err)
	}

	a.emit(ctx, "mcp.recipe.installed", map[string]any{
		"recipe_id": id,
		"config":    resolvedConfig,
	})

	// Pre-seed Cedar permit snippets for the recipe's discovered tools.
	// Best-effort: a single snippet failure logs a warning and continues.
	// The install never rolls back on snippet write failure.
	a.preSeedToolSnippets(ctx, recipe)

	status, _ := a.cfg.Pool.RecipeStatus(id)
	return status, nil
}

// preSeedToolSnippets writes Cedar permit snippets for the tools the
// recipe just exposed via the pool. It is called after a successful
// install. It is a no-op when CedarPolicy is nil, the pool has no
// ServerTools method result, or PreSeedingPolicy is "prompt_only"/"none".
func (a *API) preSeedToolSnippets(ctx context.Context, recipe recipes.Recipe) {
	if a.cfg.CedarPolicy == nil {
		return
	}
	// "prompt_only" and "none" disable pre-seeding entirely.
	switch recipe.PreSeedingPolicy {
	case "prompt_only", "none":
		return
	}
	// default ("" or "allow_all"): pre-seed all non-prompt-tagged tools.

	// Build a set of tool names that must NOT be pre-seeded.
	skipSet := make(map[string]struct{}, len(recipe.PromptOnFirstUse))
	for _, name := range recipe.PromptOnFirstUse {
		skipSet[name] = struct{}{}
	}

	// Discover the tools the server is advertising via the pool.
	serverTools := a.cfg.Pool.ServerTools(recipe.ID)

	for _, tool := range serverTools {
		if _, skip := skipSet[tool.Name]; skip {
			continue
		}
		// Build snippet filename: sanitize "<recipeID>__<toolName>".
		raw := recipe.ID + "__" + tool.Name
		filename := sanitizeSnippetID(raw)
		if !snippetNameRE.MatchString(filename) {
			slog.Warn("tools: pre-seed snippet filename invalid after sanitisation; skipping",
				"recipe", recipe.ID, "tool", tool.Name, "filename", filename)
			continue
		}
		body := fmt.Sprintf(
			"permit(principal, action == Action::\"use_tool\", resource == Tool::\"%s__%s\");",
			recipe.ID, tool.Name,
		)
		if err := a.cfg.CedarPolicy.WritePolicySnippet(ctx, filename, body); err != nil {
			slog.Warn("tools: pre-seed snippet write failed; continuing",
				"recipe", recipe.ID, "tool", tool.Name, "err", err)
		}
	}
}

// resolveConfig validates and materialises the per-install config
// against the recipe's declared ConfigOptions. It returns the
// canonical config map suitable to persist on EnabledRecipe.Config
// and feed into ToServerSpec.
//
// Per-Kind rules:
//   - directory_list: each entry must be a string; ${DATA_DIR} is
//     expanded BEFORE validation so a default like
//     "${DATA_DIR}/agent-workspace" resolves to a real path. When
//     the resolved path equals the canonical default workspace
//     under the harness DataDir, EnsureWorkspace is called
//     idempotently so a fresh install doesn't trip ErrPathNotFound.
//     Each path then runs through ValidateAllowedDir.
//   - boolean: must be a Go bool.
//   - string: must be a Go string.
//
// Required options that are missing-or-nil → error. Optional
// options that are missing fall back to the recipe's declared
// Default (also templated for directory_list). The function also
// includes "data_dir" in the resolved map when the harness has a
// DataDir set, so ToServerSpec's ${DATA_DIR} substitution works on
// args_template too.
//
// Unknown Kind values fail loudly (rather than silently dropping
// the option) so a typo in shipped.json fails the install path
// instead of producing a half-configured recipe.
func (a *API) resolveConfig(recipe recipes.Recipe, input map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if a.cfg.DataDir != "" {
		out["data_dir"] = a.cfg.DataDir
	}

	for _, opt := range recipe.ConfigOptions {
		raw, present := input[opt.Name]
		if !present || raw == nil {
			if opt.Required && opt.Default == nil {
				return nil, fmt.Errorf("tools: required config option %q for recipe %q is missing", opt.Name, recipe.ID)
			}
			if opt.Default == nil {
				continue
			}
			raw = opt.Default
		}

		switch opt.Kind {
		case recipes.ConfigKindDirectoryList:
			list, ok := coerceConfigStringSlice(raw)
			if !ok {
				return nil, fmt.Errorf("tools: config option %q for recipe %q must be a list of strings", opt.Name, recipe.ID)
			}
			expanded := make([]string, 0, len(list))
			// spec 089 (plan D4): the shipped filesystem recipe's default of
			// exactly "${DATA_DIR}/agent-workspace" means "the agent's
			// workspace" — which, when a granted workspace override is
			// active (a workbench's /workspace mount), is cfg.WorkspaceDir,
			// not a literal DataDir join. Any OTHER ${DATA_DIR}-prefixed
			// value keeps literal substitution: those name harness state on
			// purpose.
			defaultWorkspaceRaw := "${DATA_DIR}/" + agentWorkspaceDirName
			for _, p := range list {
				if p == defaultWorkspaceRaw && a.cfg.WorkspaceDir != "" {
					expanded = append(expanded, a.cfg.WorkspaceDir)
					continue
				}
				// Two passes: ${DATA_DIR} substitution (for shipped
				// recipe defaults) then user-friendly expansion of "~/",
				// "$HOME", and bare common-folder names like "Desktop".
				// Both passes must run BEFORE the persisted list is
				// stored; otherwise the npx args end up with the user's
				// literal input ("Desktop") and the MCP filesystem
				// server exits on stat() before responding to the
				// initialize handshake (EOF on stdio init).
				resolved := ExpandPath(expandDataDir(p, a.cfg.DataDir))
				expanded = append(expanded, resolved)
			}
			if a.cfg.DataDir != "" {
				defaultWorkspace := filepath.Join(a.cfg.DataDir, "agent-workspace")
				for _, p := range expanded {
					if p == defaultWorkspace {
						if _, err := EnsureWorkspace(a.cfg.DataDir); err != nil {
							return nil, fmt.Errorf("tools: ensure workspace: %w", err)
						}
						break
					}
				}
			}
			for _, p := range expanded {
				if err := ValidateAllowedDir(p); err != nil {
					return nil, err
				}
			}
			out[opt.Name] = expanded
		case recipes.ConfigKindBoolean:
			b, ok := raw.(bool)
			if !ok {
				return nil, fmt.Errorf("tools: config option %q for recipe %q must be a boolean", opt.Name, recipe.ID)
			}
			out[opt.Name] = b
		case recipes.ConfigKindString:
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("tools: config option %q for recipe %q must be a string", opt.Name, recipe.ID)
			}
			out[opt.Name] = s
		case recipes.ConfigKindEnum:
			s, ok := raw.(string)
			if !ok {
				return nil, fmt.Errorf("tools: config option %q for recipe %q must be a string (enum kind)", opt.Name, recipe.ID)
			}
			// Validate against the recipe's declared choices when present;
			// an empty Choices slice is treated as "any string accepted"
			// so older recipes that haven't filled in choices don't fail.
			if len(opt.Choices) > 0 {
				match := false
				for _, c := range opt.Choices {
					if c == s {
						match = true
						break
					}
				}
				if !match {
					return nil, fmt.Errorf("tools: config option %q for recipe %q: value %q not in declared choices %v", opt.Name, recipe.ID, s, opt.Choices)
				}
			}
			out[opt.Name] = s
		default:
			return nil, fmt.Errorf("tools: unknown ConfigOption kind: %q (option %q, recipe %q)", opt.Kind, opt.Name, recipe.ID)
		}
	}
	return out, nil
}

// expandDataDir replaces ${DATA_DIR} in s with dataDir. Other
// tokens are left untouched — they're not legal in a Default for a
// directory_list (the only caller). When dataDir is empty, the
// token is left literal so the downstream ValidateAllowedDir error
// surfaces "directory does not exist" rather than appearing to
// accept the literal "${DATA_DIR}/foo" path.
func expandDataDir(s, dataDir string) string {
	if dataDir == "" {
		return s
	}
	return strings.ReplaceAll(s, "${DATA_DIR}", dataDir)
}

// coerceConfigStringSlice accepts the two shapes a JSON-loaded
// config or recipe Default may surface for a list field.
func coerceConfigStringSlice(raw any) ([]string, bool) {
	switch v := raw.(type) {
	case []string:
		out := make([]string, len(v))
		copy(out, v)
		return out, true
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// UninstallRecipe stops the server and drops the enabled-list entry.
// Keychain entries persist by design — explicit deletion is the
// ForgetRecipeKey path. Calling Uninstall against an already-
// uninstalled recipe is non-fatal (the pool's CloseOne returns
// ErrServerNotFound, which we swallow).
func (a *API) UninstallRecipe(ctx context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Pool != nil {
		if err := a.cfg.Pool.CloseOne(ctx, id); err != nil && !errors.Is(err, stdio.ErrServerNotFound) {
			return fmt.Errorf("tools: close recipe %q: %w", id, err)
		}
	}
	if a.cfg.Enabled != nil {
		a.cfg.Enabled.Remove(id)
		if err := a.saveEnabled(); err != nil {
			return fmt.Errorf("tools: save enabled list: %w", err)
		}
	}
	a.emit(ctx, "mcp.recipe.uninstalled", map[string]any{
		"recipe_id": id,
	})
	return nil
}

// ForgetRecipeKey removes one keychain entry. The recipe stays
// enabled (or not); callers that want to fully forget should chain
// this with Uninstall.
func (a *API) ForgetRecipeKey(ctx context.Context, id, envName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Forgetter == nil {
		return errors.New("tools: no keychain forgetter configured")
	}
	locator := recipes.KeychainLocator(id, envName)
	if err := a.cfg.Forgetter.Forget(ctx, locator); err != nil {
		return fmt.Errorf("tools: forget %q: %w", locator, err)
	}
	a.emit(ctx, "mcp.recipe.key_forgotten", map[string]any{
		"recipe_id": id,
		"env_name":  envName,
	})
	return nil
}

// RecipeStatus reads-through to the pool. When the recipe is not
// running (or the pool has no record of it) we return a synthesised
// stopped-state snapshot so the frontend can render a uniform row.
func (a *API) RecipeStatus(ctx context.Context, id string) (stdio.RecipeStatus, error) {
	if a.cfg.Pool != nil {
		if status, ok := a.cfg.Pool.RecipeStatus(id); ok {
			return status, nil
		}
	}
	enabled := false
	if a.cfg.Enabled != nil {
		if _, ok := a.cfg.Enabled.Get(id); ok {
			enabled = true
		}
	}
	keysPresent := false
	if a.cfg.Catalog != nil {
		if recipe, ok := a.cfg.Catalog.Get(id); ok {
			keysPresent = a.keysResolvable(ctx, recipe)
		}
	}
	return stdio.RecipeStatus{
		ID:          id,
		Enabled:     enabled,
		State:       string(stdio.StateStopped),
		KeysPresent: keysPresent,
		UpdatedAt:   time.Now().UTC(),
	}, nil
}

// RecipeConfig returns the persisted per-install config map for an
// enabled recipe. When the recipe is not enabled the function
// returns an empty map (not nil) and no error — the frontend can
// render a uniform row regardless. The returned map is a shallow
// copy of the persisted entry so callers may safely mutate it.
func (a *API) RecipeConfig(_ context.Context, id string) (map[string]any, error) {
	if a.cfg.Enabled == nil {
		return map[string]any{}, nil
	}
	entry, ok := a.cfg.Enabled.Get(id)
	if !ok {
		return map[string]any{}, nil
	}
	out := make(map[string]any, len(entry.Config))
	for k, v := range entry.Config {
		out[k] = v
	}
	return out, nil
}

// keysResolvable reports whether every required env key for r
// resolves through the secrets backend. Optional keys do not affect
// the boolean — a recipe with one required + one optional key is
// "keys present" as long as the required one resolves. A nil
// backend returns false because we can't prove anything.
func (a *API) keysResolvable(ctx context.Context, r recipes.Recipe) bool {
	if a.cfg.Secrets == nil {
		return false
	}
	for _, key := range r.EnvKeys {
		if !key.Required {
			continue
		}
		ref := secrets.CredentialReference{
			Kind:    secrets.RefKeychain,
			Locator: recipes.KeychainLocator(r.ID, key.Name),
		}
		s, err := a.cfg.Secrets.Resolve(ctx, ref)
		if err != nil {
			return false
		}
		s.Destroy()
	}
	return true
}

// saveEnabled centralises the data-dir-aware Save call so the rollback
// branches above stay readable. nil DataDir is treated as "in-memory
// mode" and the persistence step is skipped.
func (a *API) saveEnabled() error {
	if a.cfg.Enabled == nil || a.cfg.DataDir == "" {
		return nil
	}
	return a.cfg.Enabled.Save(a.cfg.DataDir)
}

// RequestAdditionalAllowedDir implements the runtime "expand filesystem
// access" flow (runtime-fsaccess-request feature).
//
// Execution order:
//  1. Expand + validate path via ExpandPath + ValidateAllowedDir. Denied
//     paths (deny-list, bad syntax) return (false, "", err) immediately —
//     the user is never prompted for a path the harness will reject.
//  2. Fire cedar.Registry.RequestInteractive with Op "recipe_dir_add". On
//     Deny / timeout / overflow: return (false, "", nil).
//  3. On AllowOnce or AllowAlways: load the enabled-recipe entry, append
//     the canonical path (deduplicated) to Config["allowed_directories"],
//     and re-spawn the MCP server transactionally (snapshot config →
//     persist → CloseOne → OpenOne; restore snapshot on any failure).
//  4. On AllowAlways: additionally write a Cedar snippet
//     fs_allow_recipe_dir_<sanitized>.cedar so future sessions inherit the
//     grant without re-prompting.
//
// v1 hardcodes support for the "filesystem" recipe id. Multi-recipe
// support is out of scope.
func (a *API) RequestAdditionalAllowedDir(ctx context.Context, recipeID, path, reason string) (granted bool, expanded string, err error) {
	// ── Step 1: validate path before any user interaction ──────────────
	path = ExpandPath(path)
	if err := ValidateAllowedDir(path); err != nil {
		return false, "", fmt.Errorf("tools: path rejected before prompt: %w", err)
	}
	// Canonicalise: resolve symlinks so the comparison + snippet name
	// use the real path the MCP server will stat().
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		canonical = path // best-effort fallback; validation already passed
	}

	if a.cfg.PromptRegistry == nil {
		return false, canonical, errors.New("tools: prompt registry not configured")
	}

	// ── Step 2: interactive prompt ──────────────────────────────────────
	surface := cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{
			Op:            "recipe_dir_add",
			CanonicalPath: canonical,
			Dangerous:     false,
		},
		Reason: reason,
	}
	resolution, err := a.cfg.PromptRegistry.RequestInteractive(ctx, surface)
	if err != nil {
		return false, canonical, fmt.Errorf("tools: prompt error: %w", err)
	}
	switch resolution.Decision {
	case cedar.DecisionDeny:
		return false, canonical, nil
	case cedar.DecisionAllowOnce, cedar.DecisionAllowAlways:
		// proceed below
	default:
		return false, canonical, nil
	}

	// ── Step 3: expand config + restart MCP server ─────────────────────
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Enabled == nil {
		return false, canonical, errors.New("tools: no enabled-recipes store configured")
	}

	entry, ok := a.cfg.Enabled.Get(recipeID)
	if !ok {
		return false, canonical, fmt.Errorf("tools: recipe %q is not installed", recipeID)
	}

	// Snapshot current config for transactional restore on failure.
	snapshot := make(map[string]any, len(entry.Config))
	for k, v := range entry.Config {
		snapshot[k] = v
	}

	// Append canonical path to allowed_directories (dedupe, preserve order).
	existing := configStringSlice(entry.Config, "allowed_directories")
	for _, p := range existing {
		if p == canonical {
			// Already in the list — nothing to update, but the prompt
			// said yes so we call this a success.
			if resolution.Decision == cedar.DecisionAllowAlways {
				a.writeAllowAlwaysSnippet(ctx, canonical)
			}
			return true, canonical, nil
		}
	}
	newList := append(existing, canonical)
	if entry.Config == nil {
		entry.Config = make(map[string]any)
	}
	entry.Config["allowed_directories"] = newList

	// Persist the updated entry — do this BEFORE restart so a crash
	// between persist and restart leaves the stored config consistent.
	a.cfg.Enabled.Add(entry)
	if saveErr := a.saveEnabled(); saveErr != nil {
		// Roll back the in-memory change.
		entry.Config = snapshot
		a.cfg.Enabled.Add(entry)
		return false, canonical, fmt.Errorf("tools: save enabled list: %w", saveErr)
	}

	// Restart the MCP server with the new config. CloseOne + OpenOne is
	// the idempotent spawn pattern (same as InstallRecipe).
	if a.cfg.Pool != nil {
		// Build a ServerSpec from the recipe catalog + updated config.
		recipe, catalogOK := a.cfg.Catalog.Get(recipeID)
		if catalogOK {
			if restartErr := a.restartRecipe(ctx, recipe, entry.Config); restartErr != nil {
				// Roll back: restore the snapshot in memory and on disk.
				entry.Config = snapshot
				a.cfg.Enabled.Add(entry)
				// (FR-006) WARN-log the rollback-save failure; the in-memory snapshot
				// was already restored so the restart failure will be visible to the user
				// via the returned error, but a stale on-disk state is diagnosable via the log.
				if saveErr2 := a.saveEnabled(); saveErr2 != nil {
					slog.WarnContext(ctx, "tools: rollback save failed; on-disk state may be stale",
						"recipe_id", recipeID,
						"error",     saveErr2.Error(),
					)
				}
				return false, canonical, fmt.Errorf("tools: restart failed: %w", restartErr)
			}
		}
	}

	// ── Step 4: AllowAlways snippet ─────────────────────────────────────
	if resolution.Decision == cedar.DecisionAllowAlways {
		a.writeAllowAlwaysSnippet(ctx, canonical)
	}

	return true, canonical, nil
}

// CheckRecipePrereqs implements ToolsAPI.CheckRecipePrereqs. It resolves the
// recipe from the merged catalog and delegates to CheckPrereqs. Returns an
// empty slice (not nil) when all prerequisites are satisfied, so the JSON wire
// shape is always an array rather than null.
//
// The recipe ID is forwarded to CheckPrereqs so file-based prereqs (e.g.
// Gmail's ~/.gmail-mcp/gcp-oauth.keys.json) are also consulted, in addition
// to the standard binary-in-PATH runtime checks.
func (a *API) CheckRecipePrereqs(_ context.Context, id string) ([]MissingPrereq, error) {
	if a.cfg.Catalog == nil {
		return []MissingPrereq{}, nil
	}
	recipe, ok := a.cfg.Catalog.Get(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", recipes.ErrRecipeNotFound, id)
	}
	missing := CheckPrereqs(recipe.Command, recipe.ID)
	if missing == nil {
		return []MissingPrereq{}, nil
	}
	return missing, nil
}

// PlaceRecipeFile copies srcPath into the target path declared by the
// recipe's file prereq (e.g. ~/.gmail-mcp/gcp-oauth.keys.json).
//
// Security invariants:
//   - recipeID must be present in recipeFilePrereqs (has a file prereq).
//   - The destination is always exactly the recipe's declared
//     FileSetupGuide.TargetPath — callers cannot specify an arbitrary dst.
//   - The target directory is created with mode 0700 (owner-only).
//   - The file is written with mode 0600 (owner read/write only).
func (a *API) PlaceRecipeFile(_ context.Context, recipeID, srcPath string) error {
	if srcPath == "" {
		return errors.New("tools: PlaceRecipeFile: srcPath must not be empty")
	}

	// Validate recipeID has a file prereq registered.
	fp, ok := recipeFilePrereqs[recipeID]
	if !ok {
		return fmt.Errorf("tools: PlaceRecipeFile: recipe %q has no registered file prereq", recipeID)
	}
	if fp.FileSetupGuide == nil {
		return fmt.Errorf("tools: PlaceRecipeFile: recipe %q file prereq has no FileSetupGuide", recipeID)
	}
	dstPath := fp.FileSetupGuide.TargetPath
	if dstPath == "" {
		return fmt.Errorf("tools: PlaceRecipeFile: recipe %q file prereq has empty TargetPath", recipeID)
	}

	// Create target directory (owner-only) if it does not exist.
	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0700); err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: create target directory %q: %w", dstDir, err)
	}

	// Open source file.
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: open source file %q: %w", srcPath, err)
	}
	defer src.Close()

	// Write to a temp file in the same directory so we can atomically rename.
	// This avoids leaving a partial file at the destination if the copy fails.
	tmp, err := os.CreateTemp(dstDir, ".place-recipe-*.tmp")
	if err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: create temp file in %q: %w", dstDir, err)
	}
	tmpName := tmp.Name()
	// Ensure cleanup on any failure path.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	// Copy bytes.
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("tools: PlaceRecipeFile: copy to temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: close temp: %w", err)
	}

	// Set permissions to 0600 before rename so the file is never briefly
	// world-readable at the destination.
	if err := os.Chmod(tmpName, 0600); err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: chmod temp: %w", err)
	}

	// Atomic rename into place.
	if err := os.Rename(tmpName, dstPath); err != nil {
		return fmt.Errorf("tools: PlaceRecipeFile: rename to %q: %w", dstPath, err)
	}
	success = true
	return nil
}

// restartRecipe closes and re-opens the MCP server with updatedConfig.
// Called under a.mu — must NOT call a.cfg.Enabled or pool under a
// separate lock.
func (a *API) restartRecipe(ctx context.Context, recipe recipes.Recipe, updatedConfig map[string]any) error {
	if a.cfg.Secrets == nil {
		return errors.New("tools: no secrets backend configured")
	}
	resolved, err := recipes.ResolveEnv(ctx, a.cfg.Secrets, recipe)
	if err != nil {
		return fmt.Errorf("resolve env: %w", err)
	}
	spec := recipe.ToServerSpec(resolved, updatedConfig)
	if err := a.cfg.Pool.CloseOne(ctx, recipe.ID); err != nil && !errors.Is(err, stdio.ErrServerNotFound) {
		return fmt.Errorf("close existing server: %w", err)
	}
	if err := a.cfg.Pool.OpenOne(ctx, spec); err != nil {
		return fmt.Errorf("open server: %w", err)
	}
	return nil
}

// writeAllowAlwaysSnippet writes a Cedar policy file that permanently
// permits recipe_dir_add for the canonical path. Best-effort: failure is
// logged but does not affect the granted-true return.
func (a *API) writeAllowAlwaysSnippet(ctx context.Context, canonical string) {
	if a.cfg.CedarPolicy == nil || a.cfg.DataDir == "" {
		slog.Warn("tools.fsrequest.snippet_skip",
			"reason", "no CedarPolicy or DataDir configured",
			"path", canonical)
		return
	}
	sanitized := sanitizePathForSnippet(canonical)
	filename := "fs_allow_recipe_dir_" + sanitized + ".cedar"
	body := fmt.Sprintf(
		"permit(\n  principal,\n  action == Action::\"recipe_dir_add\",\n  resource == FilesystemOp::\"%s\"\n);\n",
		canonical,
	)
	if err := a.cfg.CedarPolicy.WritePolicySnippet(ctx, filename, body); err != nil {
		slog.Warn("tools.fsrequest.snippet_write_err",
			"file", filename, "err", err.Error())
	}
}

// sanitizePathForSnippet converts an absolute path into a safe filename
// stem for use in fs_allow_recipe_dir_<stem>.cedar. The stem must satisfy
// the snippet filename regex: [a-z][a-z0-9_]{0,127}.
func sanitizePathForSnippet(p string) string {
	out := make([]rune, 0, len(p))
	for _, r := range strings.ToLower(p) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
		} else {
			out = append(out, '_')
		}
	}
	// Strip leading underscores/non-alpha.
	for len(out) > 0 && !unicode.IsLetter(out[0]) {
		out = out[1:]
	}
	if len(out) == 0 {
		return "dir"
	}
	if len(out) > 64 {
		out = out[len(out)-64:]
		// Ensure starts with alpha after truncation.
		for len(out) > 0 && !unicode.IsLetter(out[0]) {
			out = out[1:]
		}
	}
	if len(out) == 0 {
		return "dir"
	}
	return string(out)
}

// configStringSlice extracts config["allowed_directories"] as []string.
// Handles both []string and []any (JSON-round-tripped form).
func configStringSlice(config map[string]any, key string) []string {
	if config == nil {
		return nil
	}
	raw, ok := config[key]
	if !ok {
		return nil
	}
	out, _ := coerceConfigStringSlice(raw)
	return out
}

// TrimAllowedDir removes path from the named recipe's
// Config["allowed_directories"] and persists the change. It implements
// permissions.RecipeConfigTrimmer so the permissions view can call back
// into the tools layer without importing it.
//
// Best-effort: failures are logged but not returned.
func (a *API) TrimAllowedDir(_ context.Context, recipeID, path string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cfg.Enabled == nil {
		return
	}
	entry, ok := a.cfg.Enabled.Get(recipeID)
	if !ok {
		return
	}
	existing := configStringSlice(entry.Config, "allowed_directories")
	found := false
	for _, p := range existing {
		if p == path {
			found = true
			break
		}
	}
	if !found {
		return
	}
	newList := make([]string, 0, len(existing))
	for _, p := range existing {
		if p != path {
			newList = append(newList, p)
		}
	}
	if entry.Config == nil {
		entry.Config = make(map[string]any)
	}
	entry.Config["allowed_directories"] = newList
	a.cfg.Enabled.Add(entry)
	if err := a.saveEnabled(); err != nil {
		slog.Warn("tools.trim_allowed_dir.save_failed",
			"recipe", recipeID, "path", path, "err", err.Error())
	}
}

// Compile-time witness.
var _ ToolsAPI = (*API)(nil)
