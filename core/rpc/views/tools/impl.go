package tools

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
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
}

// Compile-time witness: *stdio.Pool satisfies PoolController.
var _ PoolController = (*stdio.Pool)(nil)

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

// InstallRecipe is the FR-021 install path. It validates the env
// input against the recipe's required keys, stages every key in the
// keychain, persists the enabled-list update, spawns the server via
// the pool, zeroes the input env to scrub plaintext from the
// caller's frame, and returns the live status snapshot.
//
// On any failure the function unwinds in reverse: spawned process
// is closed (best-effort), enabled-list save is rolled back, and
// the error surfaces to the caller. The keychain entries written
// before the failure point persist — that matches the spec's "keys
// stick across uninstall" rule, and a follow-up install will
// overwrite them.
func (a *API) InstallRecipe(ctx context.Context, id string, env map[string]string) (stdio.RecipeStatus, error) {
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
				// Optional + omitted: skip without error.
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
	spec := recipe.ToServerSpec(resolved)

	if a.cfg.Pool == nil {
		return stdio.RecipeStatus{}, errors.New("tools: no pool configured")
	}
	if err := a.cfg.Pool.OpenOne(ctx, spec); err != nil {
		a.cfg.Enabled.Remove(id)
		_ = a.saveEnabled()
		return stdio.RecipeStatus{}, fmt.Errorf("tools: open recipe %q: %w", id, err)
	}

	a.emit(ctx, "mcp.recipe.installed", map[string]any{
		"recipe_id": id,
	})

	status, _ := a.cfg.Pool.RecipeStatus(id)
	return status, nil
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
