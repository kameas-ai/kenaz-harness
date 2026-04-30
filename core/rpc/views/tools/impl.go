package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/cedarpolicy"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
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
	Audit     AuditEmitter
	// Gate is the Cedar policy gate for InstallRecipe (WP10). nil =
	// default-permit (best-effort: a Cedar bug never blocks the chassis).
	Gate cedar.Gate
	// CedarPolicy is the optional Cedar policy API used to pre-seed
	// tool permit snippets after a successful recipe install (WP11).
	// When nil the pre-seed step is silently skipped (backwards-compatible).
	CedarPolicy cedarpolicy.CedarPolicyAPI
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
			for _, p := range list {
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

// Compile-time witness.
var _ ToolsAPI = (*API)(nil)
