// Concrete CompactionAPI implementation. Wraps a
// core/agentgraph/compaction.Pipeline.
package compaction

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corecompaction "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	sessioncompaction "github.com/kameas-ai/kenaz-harness/core/compaction"
)

// API is the concrete CompactionAPI.
type API struct {
	pipeline *corecompaction.Pipeline
	// graphLister is an optional callback that returns the available
	// graphs in the agent-graph library. Bundle D leaves it nil; the
	// graph-library mission will inject a real implementation.
	graphLister func(ctx context.Context) ([]CustomStrategy, error)
	// graphResolver maps a graph ID back to an *agentgraph.Graph the
	// custom_subgraph strategy can run. Bundle D leaves it nil; manual
	// compaction with strategy=custom_subgraph + graph_id will return
	// ErrCustomGraphUnavailable until the resolver wires up.
	graphResolver func(ctx context.Context, graphID string) (*agentgraph.Graph, error)
}

// New constructs a CompactionAPI. A nil pipeline is allowed; methods
// return ErrPipelineUnavailable so the frontend renders an empty
// state instead of crashing.
func New(p *corecompaction.Pipeline) *API {
	return &API{pipeline: p}
}

// SetGraphLister wires the agent-graph library lister. Idempotent.
func (a *API) SetGraphLister(fn func(ctx context.Context) ([]CustomStrategy, error)) {
	a.graphLister = fn
}

// SetGraphResolver wires the agent-graph library resolver.
func (a *API) SetGraphResolver(fn func(ctx context.Context, graphID string) (*agentgraph.Graph, error)) {
	a.graphResolver = fn
}

// Sentinels.
var (
	// ErrPipelineUnavailable signals the chassis booted without the
	// pipeline wired in. The frontend renders the empty-state card.
	ErrPipelineUnavailable = errors.New("compaction: pipeline unavailable")
	// ErrInvalidArg covers trivially-invalid request shapes.
	ErrInvalidArg = errors.New("compaction: invalid argument")
	// ErrCustomGraphUnavailable reports an attempt to manually trigger
	// a custom_subgraph strategy whose graph cannot be resolved
	// (resolver not wired or unknown id).
	ErrCustomGraphUnavailable = errors.New("compaction: custom graph unavailable")
)

// GetConfig returns the persisted config at the requested layer + scope id.
func (a *API) GetConfig(_ context.Context, layer Layer, scopeID string) (Config, error) {
	if a == nil || a.pipeline == nil {
		return Config{}, ErrPipelineUnavailable
	}
	if !validLayer(layer) {
		return Config{}, ErrInvalidArg
	}
	cc := a.pipeline.Resolver().Get(layerCore(layer), scopeID)
	return toWireConfig(cc), nil
}

// GetEffective walks the cascade and returns the merged config +
// attribution.
func (a *API) GetEffective(_ context.Context, scope ScopeKey) (EffectiveConfig, error) {
	if a == nil || a.pipeline == nil {
		return EffectiveConfig{}, ErrPipelineUnavailable
	}
	r := a.pipeline.Resolver()
	cc := r.Resolve(coreScope(scope))
	attr := r.Attribution(coreScope(scope))
	return EffectiveConfig{
		Config:      toWireConfig(cc),
		Attribution: toWireAttribution(attr),
	}, nil
}

// SetConfig persists one layer's config.
func (a *API) SetConfig(_ context.Context, layer Layer, scopeID string, cfg Config) error {
	if a == nil || a.pipeline == nil {
		return ErrPipelineUnavailable
	}
	if !validLayer(layer) {
		return ErrInvalidArg
	}
	a.pipeline.Resolver().Set(layerCore(layer), scopeID, fromWireConfig(cfg))
	return nil
}

// TriggerManualCompaction fires the manual invocation site for the
// given session. The pipeline resolves the cascade with the session's
// scope; the optional Override on opts forces a specific strategy.
func (a *API) TriggerManualCompaction(ctx context.Context, sessionID string, opts ManualOpts) (ManualResult, error) {
	if a == nil || a.pipeline == nil {
		return ManualResult{}, ErrPipelineUnavailable
	}
	if sessionID == "" {
		return ManualResult{}, ErrInvalidArg
	}
	scope := corecompaction.ScopeKey{SessionID: sessionID}
	var customGraph *agentgraph.Graph
	if opts.CustomGraphID != "" {
		if a.graphResolver == nil {
			return ManualResult{}, ErrCustomGraphUnavailable
		}
		g, err := a.graphResolver(ctx, opts.CustomGraphID)
		if err != nil {
			return ManualResult{}, err
		}
		customGraph = g
	}
	req := corecompaction.CompactRequest{
		RunID: "manual:" + sessionID,
		Scope: scope,
		Site:  corecompaction.SiteManual,
		Override: corecompaction.Strategy(opts.Strategy),
		Opts: corecompaction.CompactOpts{
			DropOldestKeepRecentN: opts.DropOldestKeepRecentN,
			SemanticClusterCount:  opts.SemanticClusterCount,
			SummaryProvider:       opts.SummaryProvider,
			SummaryModel:          opts.SummaryModel,
			CustomGraph:           customGraph,
		},
	}
	res, err := a.pipeline.Run(ctx, req)
	if err != nil {
		return ManualResult{}, err
	}
	return ManualResult{
		Strategy:   Strategy(res.Compacted.Strategy),
		BytesSaved: res.Compacted.BytesSaved,
		Skipped:    res.Skipped,
		Reason:     res.Reason,
	}, nil
}

// ListCustomStrategies returns the agent-graph library entries that
// are wired as custom_subgraph strategies.
func (a *API) ListCustomStrategies(ctx context.Context) ([]CustomStrategy, error) {
	if a == nil {
		return nil, ErrPipelineUnavailable
	}
	if a.graphLister == nil {
		return []CustomStrategy{}, nil
	}
	return a.graphLister(ctx)
}

// GetTierExplain returns the static tier-explain payload the Settings
// panel renders inside the "What does this mean?" disclosure on the
// compaction-aggressiveness dial (mission
// compaction-strategy-ui-01KQ8TDI §2.2 / §2.9).
//
// The numerics come from core/compaction.Tier() so the engine and UI
// share a single source of truth — the descriptions are static copy
// from plan §2.2.
//
// This method is deliberately pipeline-independent: a nil receiver still
// returns the payload so a fresh install with no compaction pipeline
// wired in (or a chassis booted with the feature flag off) still surfaces
// the dial copy in Settings.
func (a *API) GetTierExplain(_ context.Context) ([]TierExplain, error) {
	return tierExplainRows(), nil
}

// tierExplainRows builds the five-row payload from core/compaction.Tier
// + the locked descriptions. Centralised so the binding test and the
// view test can share a fixture without a separate helper.
func tierExplainRows() []TierExplain {
	type row struct {
		tier        sessioncompaction.CompactionAggressiveness
		label       string
		description string
	}
	rows := []row{
		{
			tier:  sessioncompaction.AggressivenessOff,
			label: "Off",
			description: "Never compact. Hitting the context cap will fail honestly with a " +
				"'session full' error. Recommended only when you want full transparency " +
				"about hitting the cap.",
		},
		{
			tier:  sessioncompaction.AggressivenessConservative,
			label: "Conservative",
			description: "Compact only when you've used 95% of the context window, summarising " +
				"the oldest 20% of turns. Preserves the most history.",
		},
		{
			tier:  sessioncompaction.AggressivenessBalanced,
			label: "Balanced (default)",
			description: "Compact at 80% of the context window, summarising the oldest 30% of " +
				"turns. The recommended sweet spot for long sessions.",
		},
		{
			tier:  sessioncompaction.AggressivenessAggressive,
			label: "Aggressive",
			description: "Compact at 60% of the context window, summarising the oldest 40% of " +
				"turns. Long sessions stay cheap, with more nuance lost to summary.",
		},
		{
			tier:  sessioncompaction.AggressivenessMaximal,
			label: "Maximal",
			description: "Continuous rolling summary on every turn. Best for 'infinite chat' UX " +
				"where session length matters more than per-turn fidelity. Pays for an " +
				"extra summarisation call every turn.",
		},
	}

	out := make([]TierExplain, len(rows))
	for i, r := range rows {
		params := sessioncompaction.Tier(r.tier)
		out[i] = TierExplain{
			Aggressiveness: string(r.tier),
			Label:          r.label,
			Description:    r.description,
			TriggerPct:     params.TriggerPct,
			SummarizePct:   params.SummarizePct,
			Mode:           modeString(params.Mode),
		}
	}
	return out
}

// modeString renders a CompactionMode integer as the wire-stable string
// the frontend expects ("none" | "threshold" | "rolling"). Centralised
// so any future mode addition has a single mapping point.
func modeString(m sessioncompaction.CompactionMode) string {
	switch m {
	case sessioncompaction.ModeNone:
		return "none"
	case sessioncompaction.ModeThreshold:
		return "threshold"
	case sessioncompaction.ModeRolling:
		return "rolling"
	default:
		return "none"
	}
}

// ---- conversions ----

func toWireConfig(c corecompaction.CompactionConfig) Config {
	if c.Sites == nil {
		return Config{Sites: map[Site]SiteConfig{}}
	}
	out := Config{Sites: make(map[Site]SiteConfig, len(c.Sites))}
	for s, sc := range c.Sites {
		out.Sites[Site(s)] = SiteConfig{
			Enabled:               sc.Enabled,
			Strategy:              Strategy(sc.Strategy),
			PreCallThreshold:      sc.PreCallThreshold,
			ToolResultMaxBytes:    sc.ToolResultMaxBytes,
			MaxRecursionDepth:     sc.MaxRecursionDepth,
			DropOldestKeepRecentN: sc.DropOldestKeepRecentN,
			SemanticClusterCount:  sc.SemanticClusterCount,
			SummaryProvider:       sc.SummaryProvider,
			SummaryModel:          sc.SummaryModel,
			SubgraphInputPort:     sc.SubgraphInputPort,
			SubgraphOutputPort:    sc.SubgraphOutputPort,
			CustomGraphID:         sc.CustomGraphID,
		}
	}
	return out
}

func fromWireConfig(c Config) corecompaction.CompactionConfig {
	if c.Sites == nil {
		return corecompaction.CompactionConfig{Sites: map[corecompaction.Site]corecompaction.SiteConfig{}}
	}
	out := corecompaction.CompactionConfig{
		Sites: make(map[corecompaction.Site]corecompaction.SiteConfig, len(c.Sites)),
	}
	for s, sc := range c.Sites {
		core := corecompaction.SiteConfig{
			Enabled:               sc.Enabled,
			Strategy:              corecompaction.Strategy(sc.Strategy),
			PreCallThreshold:      sc.PreCallThreshold,
			ToolResultMaxBytes:    sc.ToolResultMaxBytes,
			MaxRecursionDepth:     sc.MaxRecursionDepth,
			DropOldestKeepRecentN: sc.DropOldestKeepRecentN,
			SemanticClusterCount:  sc.SemanticClusterCount,
			SummaryProvider:       sc.SummaryProvider,
			SummaryModel:          sc.SummaryModel,
			SubgraphInputPort:     sc.SubgraphInputPort,
			SubgraphOutputPort:    sc.SubgraphOutputPort,
			CustomGraphID:         sc.CustomGraphID,
		}
		// Mark every non-zero / non-empty field as explicitly set so
		// the resolver merges only the provided values.
		core.MarkEnabled() // bool: zero value is meaningful, so always mark.
		if core.Strategy != "" {
			core.MarkStrategy()
		}
		if core.PreCallThreshold != 0 {
			core.MarkPreCallThreshold()
		}
		if core.ToolResultMaxBytes != 0 {
			core.MarkToolResultMaxBytes()
		}
		if core.MaxRecursionDepth != 0 {
			core.MarkMaxRecursionDepth()
		}
		if core.DropOldestKeepRecentN != 0 {
			core.MarkDropOldestKeepRecentN()
		}
		if core.SemanticClusterCount != 0 {
			core.MarkSemanticClusterCount()
		}
		if core.SummaryProvider != "" {
			core.MarkSummaryProvider()
		}
		if core.SummaryModel != "" {
			core.MarkSummaryModel()
		}
		if core.SubgraphInputPort != "" {
			core.MarkSubgraphInputPort()
		}
		if core.SubgraphOutputPort != "" {
			core.MarkSubgraphOutputPort()
		}
		if core.CustomGraphID != "" {
			core.MarkCustomGraphID()
		}
		out.Sites[corecompaction.Site(s)] = core
	}
	return out
}

func toWireAttribution(a corecompaction.Attribution) map[Site]map[string]Layer {
	out := make(map[Site]map[string]Layer, len(a))
	for s, fields := range a {
		fm := make(map[string]Layer, len(fields))
		for f, l := range fields {
			fm[f] = Layer(l)
		}
		out[Site(s)] = fm
	}
	return out
}

func layerCore(l Layer) corecompaction.Layer {
	return corecompaction.Layer(l)
}

func coreScope(s ScopeKey) corecompaction.ScopeKey {
	return corecompaction.ScopeKey{
		ProjectID: s.ProjectID,
		SessionID: s.SessionID,
		RunID:     s.RunID,
		NodeID:    s.NodeID,
	}
}

func validLayer(l Layer) bool {
	switch l {
	case LayerGlobal, LayerProject, LayerSession, LayerRun, LayerNode:
		return true
	}
	return false
}

// Compile-time witness.
var _ CompactionAPI = (*API)(nil)
