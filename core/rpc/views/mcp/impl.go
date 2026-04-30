// Package mcp's impl provides a concrete MCPAPI that returns the
// configured server list (currently empty — the mcp-client mission
// hasn't landed yet). The structure is right so the view lights up
// the moment servers register through bundles.
//
// The Server entries returned here are reference-only metadata —
// transport descriptors, capability advertisements, and human-readable
// state strings. No tool-call payloads or credentials traverse this
// surface (privacy CI invariant: rpc never re-emits raw payloads).
//
// WP10 (Cedar gate + audit kinds): AddRecipe, EditRecipe, and
// RemoveRecipe are wired through a Cedar gate (CheckRecipeAdd) before
// persisting. On Deny the typed *cedar.PolicyDeniedError is returned to
// the frontend. Audit events are emitted on success.
package mcp

import (
	"context"
	"sort"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// Subscriber is the broker contract used by API.StartStream. Mirrors
// the audit-impl decoupling so this package keeps DIRECTIVE_001
// isolation from core/rpc.
type Subscriber interface {
	Subscribe(ctx context.Context, view, kind string, source <-chan any) (string, error)
	Unsubscribe(id string) error
}

// Registry is the seam the mcp-client mission will populate when it
// lands. Today every implementation returns an empty list; the rpc
// layer doesn't care which Registry is wired.
type Registry interface {
	List(ctx context.Context) ([]Server, error)
}

// RecipeStore is the write seam for user-authored recipes. *recipes.UserStore
// satisfies this interface; tests pass a stub.
type RecipeStore interface {
	Save(r recipes.Recipe) error
	Delete(id string) error
}

// RecipeAuditEmitter is the narrow seam the mcp view calls for audit events.
// nil-tolerant.
type RecipeAuditEmitter interface {
	Emit(ctx context.Context, kind string, attrs map[string]any)
}

// API is the concrete MCPAPI implementation.
type API struct {
	mu       sync.RWMutex
	registry Registry
	broker   Subscriber
	subs     map[string]chan any

	// WP10: recipe management dependencies (all nil-tolerant).
	store  RecipeStore
	gate   cedar.Gate
	audit  RecipeAuditEmitter
}

// Option configures NewAPI.
type Option func(*API)

// WithRegistry plugs in the mcp-client registry. nil leaves the
// impl returning an empty list — the v1 expected behaviour until
// the client mission ships.
func WithRegistry(r Registry) Option {
	return func(a *API) { a.registry = r }
}

// WithSubscriber injects a streamBroker.
func WithSubscriber(s Subscriber) Option {
	return func(a *API) { a.broker = s }
}

// WithRecipeStore wires the user-recipe write seam (WP10).
func WithRecipeStore(s RecipeStore) Option {
	return func(a *API) { a.store = s }
}

// WithGate wires the Cedar policy gate (WP10). nil leaves gate evaluation
// disabled (default-permit — the boot-stage default).
func WithGate(g cedar.Gate) Option {
	return func(a *API) { a.gate = g }
}

// WithAudit wires the audit emitter (WP10). nil disables audit emission.
func WithAudit(e RecipeAuditEmitter) Option {
	return func(a *API) { a.audit = e }
}

// NewAPI constructs the mcp view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{subs: make(map[string]chan any)}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// emit sends an audit event when an emitter is wired. nil-safe.
func (a *API) emit(ctx context.Context, kind string, attrs map[string]any) {
	if a == nil || a.audit == nil {
		return
	}
	a.audit.Emit(ctx, kind, attrs)
}

// firstCommand returns Command[0] for a recipe, or "" when the command
// slice is empty. Used to populate the Cedar context recipe_command attr.
func firstCommand(r recipes.Recipe) string {
	if len(r.Command) > 0 {
		return r.Command[0]
	}
	return ""
}

// recipeTransport returns the recipe's transport string. All current
// recipes are stdio; the Transport field will be added by WP03/WP04.
// This stub returns "stdio" unconditionally so WP10 does not depend on
// the WP03 transport field landing first.
func recipeTransport(_ recipes.Recipe) string {
	return "stdio"
}

// AddRecipe validates a new user recipe, evaluates the Cedar gate, and
// persists it via the RecipeStore. On Cedar Deny, returns the typed
// *cedar.PolicyDeniedError so the frontend can render the deny reason
// inline. On success, emits a mcp.recipe.added audit event.
//
// Gate is best-effort: if no gate is wired (nil), the action is
// permitted (default-allow).
func (a *API) AddRecipe(ctx context.Context, r recipes.Recipe) error {
	if a.store == nil {
		return recipes.ErrUserRecipesDisabled
	}
	if err := r.Validate(); err != nil {
		return err
	}
	// Cedar gate — best-effort; nil gate = permit.
	if err := cedar.CheckRecipeAdd(ctx, a.gate, r.ID, firstCommand(r), recipeTransport(r)); err != nil {
		return err
	}
	if err := a.store.Save(r); err != nil {
		return err
	}
	a.emit(ctx, string(kindMCPRecipeAdded), map[string]any{
		"recipe_id": r.ID,
		"source":    recipes.SourceUser,
		"transport": recipeTransport(r),
	})
	return nil
}

// EditRecipe validates and persists an updated recipe. Equivalent to
// AddRecipe but semantically signals an update to an existing recipe.
// Cedar evaluates the same add_recipe action (same gate posture). On
// success emits mcp.recipe.added (an add supersedes the prior version).
//
// Gate is best-effort: nil gate = permit.
func (a *API) EditRecipe(ctx context.Context, r recipes.Recipe) error {
	if a.store == nil {
		return recipes.ErrUserRecipesDisabled
	}
	if err := r.Validate(); err != nil {
		return err
	}
	// Cedar gate — same action as AddRecipe.
	if err := cedar.CheckRecipeAdd(ctx, a.gate, r.ID, firstCommand(r), recipeTransport(r)); err != nil {
		return err
	}
	if err := a.store.Save(r); err != nil {
		return err
	}
	a.emit(ctx, string(kindMCPRecipeAdded), map[string]any{
		"recipe_id": r.ID,
		"source":    recipes.SourceUser,
		"transport": recipeTransport(r),
	})
	return nil
}

// RemoveRecipe deletes a user recipe by id and emits a mcp.recipe.removed
// audit event on success. No Cedar gate — removal is always permitted.
func (a *API) RemoveRecipe(ctx context.Context, id string) error {
	if a.store == nil {
		return recipes.ErrUserRecipesDisabled
	}
	if err := a.store.Delete(id); err != nil {
		return err
	}
	a.emit(ctx, string(kindMCPRecipeRemoved), map[string]any{
		"recipe_id": id,
		"source":    recipes.SourceUser,
	})
	return nil
}

// kindMCPRecipeAdded / kindMCPRecipeRemoved are the string constants used
// for audit emission. They mirror core/event/kind.KindMCPRecipeAdded to
// avoid an import cycle (core/event/kind → this package would cycle
// through core/rpc). The values MUST match the registry constants.
const (
	kindMCPRecipeAdded   = "mcp.recipe.added"
	kindMCPRecipeRemoved = "mcp.recipe.removed"
)

// ListServers returns every configured MCP server, sorted by name.
// Empty when no registry is wired — that's the expected v1 state and
// the frontend renders its empty-state copy.
func (a *API) ListServers(ctx context.Context) ([]Server, error) {
	a.mu.RLock()
	r := a.registry
	a.mu.RUnlock()

	if r == nil {
		return []Server{}, nil
	}
	got, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Server, len(got))
	copy(out, got)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// StartStream allocates a per-subscription channel for the named
// server's `mcp:event` topic. With no registry wired today the
// channel is created but no events flow until a registry pushes
// through Publish.
func (a *API) StartStream(ctx context.Context, _ string) (string, error) {
	if a.broker == nil {
		return "", nil
	}
	ch := make(chan any, 64)
	id, err := a.broker.Subscribe(ctx, "mcp", "event", ch)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.subs[id] = ch
	a.mu.Unlock()
	return id, nil
}

// StopStream tears down the subscription and releases broker state.
func (a *API) StopStream(_ context.Context, id string) error {
	if a.broker == nil {
		return nil
	}
	a.mu.Lock()
	ch, ok := a.subs[id]
	delete(a.subs, id)
	a.mu.Unlock()
	if ok {
		close(ch)
	}
	return a.broker.Unsubscribe(id)
}

// Publish fans a server event into every active subscriber. Exposed
// for the mcp-client mission to push events without coupling to
// core/rpc directly.
func (a *API) Publish(ev any) {
	a.mu.RLock()
	subs := make([]chan any, 0, len(a.subs))
	for _, ch := range a.subs {
		subs = append(subs, ch)
	}
	a.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Drop rather than block — the canonical record is the
			// mcp-client's own log; this surface is best-effort fan-out.
		}
	}
}
