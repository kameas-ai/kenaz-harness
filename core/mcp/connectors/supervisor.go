package connectors

import (
	"context"
	"log/slog"
	"sync"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// PoolOpener is the slice of the transport dispatch pool the supervisor
// needs. *dispatch.Pool satisfies it.
type PoolOpener interface {
	OpenOne(ctx context.Context, spec coremcp.ServerSpec) error
}

// TokenSource mints short-lived access tokens for whitelisted OAuth
// connectors from the host auth broker (spec 091 D8).
// *authbroker.ConnectorTokens satisfies it.
type TokenSource interface {
	ConnectorToken(ctx context.Context, recipeID string) (string, error)
}

// Spawn states surfaced through the Connectors_Status read RPC.
const (
	// SpawnStateUnavailable — the whitelisted id did not resolve in the
	// image's embedded catalog, or could not be prepared for spawn. The
	// ADR's "warn and drop": the launch does not fail, the connector is
	// visibly unavailable.
	SpawnStateUnavailable = "unavailable"
	// SpawnStateOK — the connector opened onto the pool at boot.
	SpawnStateOK = "ok"
	// SpawnStateFailed — the spawn was attempted and failed.
	SpawnStateFailed = "failed"
)

// Spawn/unavailability reason classes. Stable strings; never raw error
// text (which could carry argv or path fragments).
const (
	ReasonUnknownRecipe = "unknown_recipe"
	ReasonMissingEnv    = "missing_env"
	ReasonOpenFailed    = "open_failed"
	ReasonNoPool        = "no_pool"
)

// State is the per-connector boot outcome, in whitelist order.
type State struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Category    string `json:"category"`
	Transport   string `json:"transport"`
	// Available is true when the id resolved in the embedded catalog.
	Available bool `json:"available"`
	// Enabled is true when the connector was enabled at boot (resolved +
	// whitelisted; a spawn was attempted).
	Enabled bool `json:"enabled"`
	// SpawnState is one of the SpawnState* values ("" before Bootstrap).
	SpawnState string `json:"spawn_state,omitempty"`
	// Reason is a Reason* class when SpawnState is not ok.
	Reason string `json:"reason,omitempty"`
}

// SupervisorConfig configures NewSupervisor. Getenv is required;
// everything else is nil-tolerant.
type SupervisorConfig struct {
	// Provisioning is the parsed KENAZ_MCP_ALLOWLIST outcome
	// (ProvisionFromEnv's return value).
	Provisioning Provisioning
	// Getenv resolves the namespaced credential grants (DenamespaceEnv).
	Getenv func(string) string
	// Tokens mints broker access tokens for OAuth connectors. nil —
	// OAuth connectors spawn unauthenticated (deferred auth).
	Tokens TokenSource
	// Ledger emits the FR-014 connector lifecycle events. nil disables.
	Ledger *LedgerEmitter
	// Catalog snapshots the embedded recipe catalog. nil uses the
	// shipped + curated-registry merge (the same view the rpc boot path
	// consults).
	Catalog func() *recipes.Catalog
	Logger  *slog.Logger
}

// Supervisor owns the served-mode connector boot: it resolves the
// whitelisted ids against the image's embedded catalog, de-namespaces each
// recipe's credential grants, spawns onto the dispatch pool with an
// isolated child env, and records per-connector outcomes for the
// Connectors_List / Connectors_Status read surface (spec 091 D11).
//
// It replaces the persisted-enabled recipe bootstrap in served mode: the
// profile whitelist is the ONLY thing that enables a connector (FR-004).
type Supervisor struct {
	cfg SupervisorConfig

	mu          sync.RWMutex
	pool        PoolOpener
	states      map[string]*State
	order       []string
	whitelisted map[string]bool
}

// NewSupervisor builds a Supervisor. Call SetPool before Bootstrap (the
// rpc chassis wires the dispatch pool during construction).
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.Getenv == nil {
		cfg.Getenv = func(string) string { return "" }
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Catalog == nil {
		cfg.Catalog = func() *recipes.Catalog {
			mc := recipes.NewMergedCatalog(
				func() []recipes.Recipe { return recipes.Shipped().List() },
				func() []recipes.Recipe { return recipes.Registry().List() },
				nil,
			)
			return &recipes.Catalog{Version: 1, Recipes: mc.Recipes()}
		}
	}
	s := &Supervisor{
		cfg:         cfg,
		states:      make(map[string]*State, len(cfg.Provisioning.IDs)),
		order:       cfg.Provisioning.IDs,
		whitelisted: make(map[string]bool, len(cfg.Provisioning.IDs)),
	}
	for _, id := range cfg.Provisioning.IDs {
		s.whitelisted[id] = true
	}
	return s
}

// SetPool wires the transport dispatch pool. Called by the rpc chassis
// once the pool exists; must precede Bootstrap.
func (s *Supervisor) SetPool(p PoolOpener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool = p
}

// Provisioning returns the parsed whitelist outcome.
func (s *Supervisor) Provisioning() Provisioning {
	return s.cfg.Provisioning
}

// States returns the per-connector boot outcomes in whitelist order.
func (s *Supervisor) States() []State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]State, 0, len(s.order))
	for _, id := range s.order {
		if st := s.states[id]; st != nil {
			out = append(out, *st)
		}
	}
	return out
}

// ObserveToolCall emits the FR-014 connector.tool_call ledger event when
// server names a whitelisted connector. Wired as the dispatch pool's call
// observer; tool arguments are never seen here, by construction.
func (s *Supervisor) ObserveToolCall(server, tool string) {
	s.mu.RLock()
	isConnector := s.whitelisted[server]
	s.mu.RUnlock()
	if !isConnector {
		return
	}
	s.cfg.Ledger.EmitToolCall(server, tool)
}

// Bootstrap resolves and spawns every whitelisted connector. It matches
// the core.SetMCPRecipeBootstrap signature and replaces the persisted-
// enabled bootstrap in served mode. Per-connector failures warn and mark
// the connector unavailable — the boot never fails (FR-011: degrade to a
// visible state, never a boot failure).
//
// The Cedar interactive credential gate is deliberately NOT consulted
// here: the operator granted each connector on the HOST consent surface
// (spec 091 D5), and a headless served boot has no one to prompt. The
// whitelist is the grant.
func (s *Supervisor) Bootstrap(ctx context.Context) error {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	prov := s.cfg.Provisioning
	if !prov.Provisioned {
		s.cfg.Logger.Info("connectors: bootstrap skipped — not provisioned (block-all)")
		return nil
	}

	catalog := s.cfg.Catalog()
	for _, id := range prov.IDs {
		st := &State{ID: id, DisplayName: id}
		s.setState(st)

		recipe, ok := catalog.Get(id)
		if !ok {
			// Version skew (host vendored ahead of the image pin):
			// warn and drop, per the ADR's skew table.
			st.SpawnState = SpawnStateUnavailable
			st.Reason = ReasonUnknownRecipe
			s.setState(st)
			s.cfg.Logger.Warn("connectors: whitelisted id not in embedded catalog — unavailable",
				"connector_id", id)
			continue
		}
		st.DisplayName = recipe.DisplayName
		st.Category = recipe.Category
		st.Transport = recipe.Transport
		if st.Transport == "" {
			st.Transport = recipes.TransportStdio
		}
		st.Available = true
		st.Enabled = true
		s.setState(st)
		s.cfg.Ledger.EmitEnabled(id)

		env := DenamespaceEnv(s.cfg.Getenv, recipe)
		if missing := MissingRequiredKeys(recipe, env); len(missing) > 0 {
			st.SpawnState = SpawnStateFailed
			st.Reason = ReasonMissingEnv
			s.setState(st)
			// Key NAMES are declared metadata (they enter the image
			// bake); values are never logged.
			s.cfg.Logger.Warn("connectors: required env grants missing — connector unavailable",
				"connector_id", id, "missing", missing)
			s.cfg.Ledger.EmitSpawn(id, false, ReasonMissingEnv)
			continue
		}

		spec := recipe.ToServerSpec(env, nil)
		// D6: the served process env carries every connector's grants; a
		// spawned child must see only its own (asserted in transport tests).
		spec.IsolateEnv = true

		// D8: OAuth connectors authenticate with a host-brokered
		// short-lived access token; the refresh token never crosses.
		// A missing token is deferred auth, not a spawn failure —
		// matching the tools view's injectOAuthBearer posture.
		if recipe.Auth != nil && recipe.Auth.Kind == recipes.AuthKindMCPOAuth && s.cfg.Tokens != nil {
			tok, err := s.cfg.Tokens.ConnectorToken(ctx, id)
			switch {
			case err != nil:
				s.cfg.Logger.Warn("connectors: broker token unavailable — spawning unauthenticated",
					"connector_id", id, "err", err.Error())
			case tok != "":
				if spec.HeadersTemplate == nil {
					spec.HeadersTemplate = map[string]string{}
				}
				spec.HeadersTemplate["Authorization"] = "Bearer " + tok
			}
		}

		if pool == nil {
			st.SpawnState = SpawnStateFailed
			st.Reason = ReasonNoPool
			s.setState(st)
			s.cfg.Ledger.EmitSpawn(id, false, ReasonNoPool)
			continue
		}
		if err := pool.OpenOne(ctx, spec); err != nil {
			st.SpawnState = SpawnStateFailed
			st.Reason = ReasonOpenFailed
			s.setState(st)
			s.cfg.Logger.Warn("connectors: spawn failed", "connector_id", id, "err", err.Error())
			s.cfg.Ledger.EmitSpawn(id, false, ReasonOpenFailed)
			continue
		}
		st.SpawnState = SpawnStateOK
		s.setState(st)
		s.cfg.Logger.Info("connectors: connector up", "connector_id", id, "transport", st.Transport)
		s.cfg.Ledger.EmitSpawn(id, true, "")
	}
	return nil
}

func (s *Supervisor) setState(st *State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *st
	s.states[st.ID] = &cp
}
