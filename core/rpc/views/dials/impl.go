// Concrete DialsAPI implementation. Holds an in-memory layer store
// keyed by (Scope, ID). Persistence is the host's responsibility — the
// rpc layer hands a Persister callback at construction time so layer
// writes propagate to the project / session row in storage.
//
// The impl is read-mostly: GetEffective is the hot path (the chat
// surface polls it on every node fire). Layer updates are rare (the
// user clicking save in the dials panel). A single sync.RWMutex is
// fine.
package dials

import (
	"context"
	"errors"
	"sync"
	"time"

	coredials "github.com/sigil-tech/kaneaz-harness/core/agentgraph/dials"
)

// ErrNoPause is returned by BumpAndResume when the run is not
// currently paused at a cap-hit boundary.
var ErrNoPause = errors.New("dials: run is not paused at a cap-hit")

// Resumer is the optional capability the impl uses to nudge the
// kernel into resuming a paused run after a cap bump. Wired by the
// rpc layer when a kernel is bound; nil during chassis-only test
// fixtures.
type Resumer interface {
	Resume(ctx context.Context, runID string) error
}

// Persister is the optional callback invoked after every SetDials
// write so the host can mirror the in-memory store onto disk.
// runID-scoped writes never persist (they vanish with the run).
type Persister func(key ScopeKey, cfg DialConfig) error

// API is the concrete DialsAPI.
type API struct {
	mu       sync.RWMutex
	store    map[ScopeKey]DialConfig
	resumer  Resumer
	persist  Persister
}

// Config bundles dependencies for New.
type Config struct {
	Resumer   Resumer
	Persister Persister
}

// New constructs a DialsAPI. cfg may be the zero value — the impl
// degrades gracefully (BumpAndResume returns ErrNoPause; SetDials
// updates the in-memory store only).
func New(cfg Config) *API {
	return &API{
		store:   map[ScopeKey]DialConfig{},
		resumer: cfg.Resumer,
		persist: cfg.Persister,
	}
}

// GetDials reads back a single layer's overrides.
func (a *API) GetDials(_ context.Context, key ScopeKey) (DialConfig, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.store[key], nil
}

// SetDials writes a layer's overrides + invokes the host persister.
func (a *API) SetDials(_ context.Context, key ScopeKey, cfg DialConfig) error {
	cfg.UpdatedAt = time.Now().UTC()
	a.mu.Lock()
	a.store[key] = cfg
	persist := a.persist
	a.mu.Unlock()
	// Run scope is ephemeral by design; never persist.
	if persist != nil && key.Scope != string(coredials.ScopePerRun) {
		return persist(key, cfg)
	}
	return nil
}

// GetEffective resolves the full cascade and returns the effective
// value plus the contributing scope per field.
func (a *API) GetEffective(_ context.Context, projectID, sessionID, graphID, runID string) (EffectiveDials, error) {
	a.mu.RLock()
	layers := []coredials.Layer{
		{Scope: coredials.ScopeGlobal, Config: toCoreConfig(a.store[ScopeKey{Scope: string(coredials.ScopeGlobal)}])},
	}
	if projectID != "" {
		layers = append(layers, coredials.Layer{
			Scope:  coredials.ScopeProject,
			Config: toCoreConfig(a.store[ScopeKey{Scope: string(coredials.ScopeProject), ID: projectID}]),
		})
	}
	if sessionID != "" {
		layers = append(layers, coredials.Layer{
			Scope:  coredials.ScopeSession,
			Config: toCoreConfig(a.store[ScopeKey{Scope: string(coredials.ScopeSession), ID: sessionID}]),
		})
	}
	if graphID != "" {
		layers = append(layers, coredials.Layer{
			Scope:  coredials.ScopePerGraph,
			Config: toCoreConfig(a.store[ScopeKey{Scope: string(coredials.ScopePerGraph), ID: graphID}]),
		})
	}
	if runID != "" {
		layers = append(layers, coredials.Layer{
			Scope:  coredials.ScopePerRun,
			Config: toCoreConfig(a.store[ScopeKey{Scope: string(coredials.ScopePerRun), ID: runID}]),
		})
	}
	a.mu.RUnlock()
	eff := coredials.Resolve(layers)
	return toViewEffective(eff), nil
}

// BumpAndResume bumps the run-scoped layer's caps by delta and
// notifies the resumer.
func (a *API) BumpAndResume(ctx context.Context, runID string, delta DialDelta) error {
	if runID == "" {
		return errors.New("dials: empty runID")
	}
	key := ScopeKey{Scope: string(coredials.ScopePerRun), ID: runID}
	a.mu.Lock()
	current := a.store[key]
	current = applyDelta(current, delta)
	current.UpdatedAt = time.Now().UTC()
	a.store[key] = current
	resumer := a.resumer
	a.mu.Unlock()
	if resumer == nil {
		return ErrNoPause
	}
	return resumer.Resume(ctx, runID)
}

// applyDelta adds the delta to a wire-shape DialConfig. Returns the
// updated config; zero-pointer fields stay zero (Bump never invents
// overrides).
func applyDelta(cfg DialConfig, d DialDelta) DialConfig {
	if d.AddTokensPerRun != 0 {
		cfg.MaxTokensPerRun += d.AddTokensPerRun
		cfg.MaxTokensPerRunSet = true
	}
	if d.AddWallclockSeconds != 0 {
		cfg.MaxWallclockSeconds += d.AddWallclockSeconds
		cfg.MaxWallclockSet = true
	}
	if d.AddLLMCalls != 0 {
		cfg.MaxLLMCalls += d.AddLLMCalls
		cfg.MaxLLMCallsSet = true
	}
	if d.AddToolCalls != 0 {
		cfg.MaxToolCalls += d.AddToolCalls
		cfg.MaxToolCallsSet = true
	}
	if d.AddCostUSD != 0 {
		cfg.MaxCostUSD += d.AddCostUSD
		cfg.MaxCostUSDSet = true
	}
	return cfg
}

// toCoreConfig translates the wire shape into the core/agentgraph/dials
// DialConfig (pointer-encoded "unset"). The booleans drive whether
// the pointer is set.
func toCoreConfig(c DialConfig) coredials.DialConfig {
	out := coredials.DialConfig{}
	if c.MaxTokensPerRunSet {
		v := c.MaxTokensPerRun
		out.MaxTokensPerRun = &v
	}
	if c.MaxWallclockSet {
		v := time.Duration(c.MaxWallclockSeconds) * time.Second
		out.MaxWallclock = &v
	}
	if c.MaxLLMCallsSet {
		v := c.MaxLLMCalls
		out.MaxLLMCalls = &v
	}
	if c.MaxToolCallsSet {
		v := c.MaxToolCalls
		out.MaxToolCalls = &v
	}
	if c.MaxCostUSDSet {
		v := c.MaxCostUSD
		out.MaxCostUSD = &v
	}
	if c.PlanVerbositySet {
		v := c.PlanVerbosity
		out.PlanVerbosity = &v
	}
	if c.AskThresholdSet {
		v := c.AskThreshold
		out.AskThreshold = &v
	}
	if c.ReflectFrequencySet {
		v := c.ReflectFrequency
		out.ReflectFrequency = &v
	}
	if c.CompactionAggressivenessSet {
		v := c.CompactionAggressiveness
		out.CompactionAggressiveness = &v
	}
	if c.ReviewIterationsCapSet {
		v := c.ReviewIterationsCap
		out.ReviewIterationsCap = &v
	}
	if c.MemoryHooksEnabledSet {
		v := c.MemoryHooksEnabled
		out.MemoryHooksEnabled = &v
	}
	if c.MemoryPruneIntervalSet {
		v := time.Duration(c.MemoryPruneIntervalSeconds) * time.Second
		out.MemoryPruneInterval = &v
	}
	return out
}

// toViewEffective converts the resolved cascade into the wire shape.
func toViewEffective(eff coredials.EffectiveDials) EffectiveDials {
	return EffectiveDials{
		MaxTokensPerRun:          EffectiveField[int]{Value: eff.MaxTokensPerRun, From: string(eff.MaxTokensPerRunFrom)},
		MaxWallclockSeconds:      EffectiveField[int]{Value: int(eff.MaxWallclock / time.Second), From: string(eff.MaxWallclockFrom)},
		MaxLLMCalls:              EffectiveField[int]{Value: eff.MaxLLMCalls, From: string(eff.MaxLLMCallsFrom)},
		MaxToolCalls:             EffectiveField[int]{Value: eff.MaxToolCalls, From: string(eff.MaxToolCallsFrom)},
		MaxCostUSD:               EffectiveField[float64]{Value: eff.MaxCostUSD, From: string(eff.MaxCostUSDFrom)},
		PlanVerbosity:            EffectiveField[string]{Value: eff.PlanVerbosity, From: string(eff.PlanVerbosityFrom)},
		AskThreshold:             EffectiveField[float64]{Value: eff.AskThreshold, From: string(eff.AskThresholdFrom)},
		ReflectFrequency:         EffectiveField[int]{Value: eff.ReflectFrequency, From: string(eff.ReflectFrequencyFrom)},
		CompactionAggressiveness: EffectiveField[float64]{Value: eff.CompactionAggressiveness, From: string(eff.CompactionAggressivenessFrom)},
		ReviewIterationsCap:      EffectiveField[int]{Value: eff.ReviewIterationsCap, From: string(eff.ReviewIterationsCapFrom)},
		MemoryHooksEnabled:       EffectiveField[bool]{Value: eff.MemoryHooksEnabled, From: string(eff.MemoryHooksEnabledFrom)},
		MemoryPruneIntervalSeconds: EffectiveField[int]{
			Value: int(eff.MemoryPruneInterval / time.Second),
			From:  string(eff.MemoryPruneIntervalFrom),
		},
	}
}

// Compile-time witness: *API satisfies DialsAPI.
var _ DialsAPI = (*API)(nil)
