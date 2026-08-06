// Package mcp's impl provides a concrete MCPAPI that returns the
// configured server list (currently empty — the mcp-client mission
// hasn't landed yet). The structure is right so the view lights up
// the moment servers register through bundles.
//
// The Server entries returned here are reference-only metadata —
// transport descriptors, capability advertisements, and human-readable
// state strings. No tool-call payloads or credentials traverse this
// surface (privacy CI invariant: rpc never re-emits raw payloads).
package mcp

import (
	"context"
	"errors"
	"sort"
	"sync"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
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

// RecipeCatalog is the read seam used by TestRecipe for recipe lookup.
// *recipes.MergedCatalog satisfies this interface.
type RecipeCatalog interface {
	Get(id string) (recipes.Recipe, bool)
}

// HealthPool is the subset of *stdio.Pool used by HealthSnapshot.
// Defined as an interface so tests can inject a fake.
type HealthPool interface {
	AllRecipeStatuses() []stdio.RecipeStatus
}

// Compile-time witness: *stdio.Pool satisfies HealthPool.
var _ HealthPool = (*stdio.Pool)(nil)

// API is the concrete MCPAPI implementation.
type API struct {
	mu          sync.RWMutex
	registry    Registry
	broker      Subscriber
	catalog     RecipeCatalog
	healthPool  HealthPool
	healthSubs  map[string]chan any
	subs        map[string]chan any
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

// WithCatalog injects the merged recipe catalog used by TestRecipe.
// Without this option TestRecipe returns ErrCatalogNotConfigured.
func WithCatalog(c RecipeCatalog) Option {
	return func(a *API) { a.catalog = c }
}

// WithHealthPool injects the stdio pool used by HealthSnapshot and
// SubscribeHealthChanges (mcp-server-health-ui WP01/WP02). nil is
// safe — HealthSnapshot returns an empty map.
func WithHealthPool(p HealthPool) Option {
	return func(a *API) { a.healthPool = p }
}

// SetHealthPool wires the health pool after construction. The rpc chassis
// builds this view before the transport pools exist, so the pool is
// attached once the dispatch pool is up (spec 091: the served
// Connectors_List/Status surface reads HealthSnapshot).
func (a *API) SetHealthPool(p HealthPool) {
	a.mu.Lock()
	a.healthPool = p
	a.mu.Unlock()
}

// NewAPI constructs the mcp view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{
		subs:       make(map[string]chan any),
		healthSubs: make(map[string]chan any),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

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
// Handles both regular event subs and health-change subs.
func (a *API) StopStream(_ context.Context, id string) error {
	if a.broker == nil {
		return nil
	}
	a.mu.Lock()
	ch, ok := a.subs[id]
	if ok {
		delete(a.subs, id)
	} else {
		// Check health subs.
		ch, ok = a.healthSubs[id]
		if ok {
			delete(a.healthSubs, id)
		}
	}
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

// HealthSnapshot returns the current health for every recipe in the pool as a
// map of recipe-id → HealthEntry. When no pool is wired the map is empty —
// that is the expected v1 state until a pool is registered.
// (mcp-server-health-ui WP01)
func (a *API) HealthSnapshot(_ context.Context) (map[string]HealthEntry, error) {
	a.mu.RLock()
	pool := a.healthPool
	a.mu.RUnlock()

	out := make(map[string]HealthEntry)
	if pool == nil {
		return out, nil
	}
	for _, rs := range pool.AllRecipeStatuses() {
		out[rs.ID] = HealthEntry{
			ID:              rs.ID,
			State:           rs.State,
			LastError:       rs.LastError,
			RestartAttempts: rs.RestartAttempts,
			StderrTail:      rs.StderrTail,
			ToolCount:       rs.ToolCount,
			ServerName:      rs.ServerName,
			ServerVersion:   rs.ServerVersion,
			ProtocolVersion: rs.ProtocolVersion,
		}
	}
	return out, nil
}

// SubscribeHealthChanges registers a broker subscription for
// `mcp:health-changed` events. The caller tears it down via StopStream.
// Events are pushed by PublishHealthChange.
// (mcp-server-health-ui WP02)
func (a *API) SubscribeHealthChanges(ctx context.Context) (string, error) {
	if a.broker == nil {
		return "", nil
	}
	ch := make(chan any, 64)
	id, err := a.broker.Subscribe(ctx, "mcp", "health-changed", ch)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.healthSubs[id] = ch
	a.mu.Unlock()
	return id, nil
}

// PublishHealthChange fans a HealthEntry event to every active health
// subscriber. Called by the pool supervisor or audit hook when a recipe's
// state transitions. Best-effort: drops rather than blocks on slow consumers.
// (mcp-server-health-ui WP02)
func (a *API) PublishHealthChange(entry HealthEntry) {
	a.mu.RLock()
	subs := make([]chan any, 0, len(a.healthSubs))
	for _, ch := range a.healthSubs {
		subs = append(subs, ch)
	}
	a.mu.RUnlock()
	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
}

// ErrCatalogNotConfigured is returned by TestRecipe when no catalog
// has been wired via WithCatalog. Callers interpret this as
// "feature not yet available in this API instance".
var ErrCatalogNotConfigured = errors.New("mcp: TestRecipe catalog not configured")

// TestRecipe looks up the recipe by id in the merged catalog, builds
// a ServerSpec via Recipe.ToServerSpec, and delegates to
// coremcp.TestConnection. env and config override the recipe's
// stored values (nil is safe; the recipe's own defaults apply).
//
// The method satisfies the MCPAPI interface for TestRecipe. It
// returns (coremcp.TestResult{}, err) for pre-flight failures; for
// transport-level failures the error is folded into the returned
// TestResult.Error field and the Go error return is nil.
func (a *API) TestRecipe(ctx context.Context, recipeID string, env map[string]string, config map[string]any) (coremcp.TestResult, error) {
	a.mu.RLock()
	cat := a.catalog
	a.mu.RUnlock()

	if cat == nil {
		return coremcp.TestResult{}, ErrCatalogNotConfigured
	}

	recipe, ok := cat.Get(recipeID)
	if !ok {
		return coremcp.TestResult{}, recipes.ErrRecipeNotFound
	}

	spec := recipe.ToServerSpec(env, config)
	result := TestConnection(ctx, spec)
	return result, nil
}
