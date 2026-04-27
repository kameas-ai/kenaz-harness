// Package compaction is the view-scoped accessor for the configurable
// compaction subsystem (mission agent-kernel-graph; Bundle D WP12/WP13).
// Wraps core/agentgraph/compaction.Pipeline behind the rpc boundary so
// the frontend never imports the kernel layout directly.
//
// Wire shapes mirror core/agentgraph/compaction types but trim
// non-portable fields (the Compactor interface, opaque agentgraph.Graph
// pointers) so JSON round-trips through Wails cleanly.
package compaction

import (
	"context"
)

// Site is the wire-stable string for an invocation site.
type Site string

const (
	SitePreCall  Site = "pre_call"
	SitePostTool Site = "post_tool"
	SiteManual   Site = "manual"
)

// Strategy is the wire-stable string for a strategy.
type Strategy string

const (
	StrategySummary         Strategy = "summary"
	StrategyDropOldest      Strategy = "drop_oldest"
	StrategySemanticCluster Strategy = "semantic_cluster"
	StrategyCustomSubgraph  Strategy = "custom_subgraph"
)

// Layer is the wire-stable string for a cascade layer.
type Layer string

const (
	LayerGlobal  Layer = "global"
	LayerProject Layer = "project"
	LayerSession Layer = "session"
	LayerRun     Layer = "run"
	LayerNode    Layer = "node"
)

// SiteConfig is the wire shape for one site's config layer. Mirrors
// core/agentgraph/compaction.SiteConfig minus the unexported
// presence-bitset (the impl re-derives presence from non-zero
// fields when persisting).
type SiteConfig struct {
	Enabled               bool     `json:"enabled"`
	Strategy              Strategy `json:"strategy,omitempty"`
	PreCallThreshold      float64  `json:"preCallThreshold,omitempty"`
	ToolResultMaxBytes    int      `json:"toolResultMaxBytes,omitempty"`
	MaxRecursionDepth     int      `json:"maxRecursionDepth,omitempty"`
	DropOldestKeepRecentN int      `json:"dropOldestKeepRecentN,omitempty"`
	SemanticClusterCount  int      `json:"semanticClusterCount,omitempty"`
	SummaryProvider       string   `json:"summaryProvider,omitempty"`
	SummaryModel          string   `json:"summaryModel,omitempty"`
	SubgraphInputPort     string   `json:"subgraphInputPort,omitempty"`
	SubgraphOutputPort    string   `json:"subgraphOutputPort,omitempty"`
	CustomGraphID         string   `json:"customGraphId,omitempty"`
}

// Config is the wire shape for a CompactionConfig (one layer's
// contribution).
type Config struct {
	Sites map[Site]SiteConfig `json:"sites,omitempty"`
}

// EffectiveConfig is the resolved cascade view: the merged values
// plus a per-(site, field) attribution map showing which layer
// supplied each value.
type EffectiveConfig struct {
	Config      Config                       `json:"config"`
	Attribution map[Site]map[string]Layer    `json:"attribution"`
}

// ScopeKey identifies a resolution chain.
type ScopeKey struct {
	ProjectID string `json:"projectId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	RunID     string `json:"runId,omitempty"`
	NodeID    string `json:"nodeId,omitempty"`
}

// CustomStrategy is the wire shape for one row in the
// agent-graph library that can be wired as a custom_subgraph
// strategy. Bundle D ships an empty list — Bundle C/E populates it
// once the graph library RPC lands.
type CustomStrategy struct {
	GraphID     string `json:"graphId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ManualOpts are the knobs the manual-compaction trigger surfaces.
// Empty fields fall back to the resolved cascade.
type ManualOpts struct {
	Strategy              Strategy `json:"strategy,omitempty"`
	DropOldestKeepRecentN int      `json:"dropOldestKeepRecentN,omitempty"`
	SemanticClusterCount  int      `json:"semanticClusterCount,omitempty"`
	SummaryProvider       string   `json:"summaryProvider,omitempty"`
	SummaryModel          string   `json:"summaryModel,omitempty"`
	CustomGraphID         string   `json:"customGraphId,omitempty"`
}

// ManualResult is the wire shape returned by TriggerManualCompaction.
type ManualResult struct {
	Strategy   Strategy `json:"strategy"`
	BytesSaved int      `json:"bytesSaved"`
	Skipped    bool     `json:"skipped,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// CompactionAPI is the view-scoped surface backing the frontend's
// CompactionSettings view. Exactly four operations:
//
//   - GetConfig(layer, scopeID) — fetch the persisted config layer.
//   - GetEffective(scope) — fetch the merged-cascade view + attribution.
//   - SetConfig(layer, scopeID, config) — persist one layer's config.
//   - TriggerManualCompaction(sessionID, opts) — fire the manual site.
//   - ListCustomStrategies() — return the registered custom_subgraph
//     graphs available as strategies.
type CompactionAPI interface {
	GetConfig(ctx context.Context, layer Layer, scopeID string) (Config, error)
	GetEffective(ctx context.Context, scope ScopeKey) (EffectiveConfig, error)
	SetConfig(ctx context.Context, layer Layer, scopeID string, cfg Config) error
	TriggerManualCompaction(ctx context.Context, sessionID string, opts ManualOpts) (ManualResult, error)
	ListCustomStrategies(ctx context.Context) ([]CustomStrategy, error)
}
