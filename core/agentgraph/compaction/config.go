// Cascading config: global > project > session > per-run > per-node
// (FR-043). Each layer can specify per-site strategy + thresholds.
// Resolve walks the layers from most-specific (per-node) to most-
// general (global) and returns the effective config plus an
// attribution map showing which layer supplied each field.
package compaction

import (
	"sort"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// Layer enumerates the cascading layers in canonical order. The
// resolver walks from most-general to most-specific, so later layers
// override earlier ones.
type Layer string

const (
	LayerGlobal  Layer = "global"
	LayerProject Layer = "project"
	LayerSession Layer = "session"
	LayerRun     Layer = "run"
	LayerNode    Layer = "node"
)

// AllLayers returns every supported layer in coarse-to-fine order.
func AllLayers() []Layer {
	return []Layer{LayerGlobal, LayerProject, LayerSession, LayerRun, LayerNode}
}

// SiteConfig is the per-site, per-layer config carried by
// CompactionConfig. A SiteConfig with Enabled=false suppresses
// compaction at that site even if other settings are present.
type SiteConfig struct {
	// Enabled defaults to true on every layer that omits the field.
	// The resolver merges by promoting any layer's explicit `false`
	// (zero is "unset"; we use a *bool for the attribution map).
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Strategy is the strategy name to invoke at this site.
	Strategy Strategy `json:"strategy,omitempty" yaml:"strategy,omitempty"`
	// PreCallThreshold is the fraction of MaxTokens above which
	// pre_call compaction fires (FR-041 default 0.85).
	PreCallThreshold float64 `json:"pre_call_threshold,omitempty" yaml:"pre_call_threshold,omitempty"`
	// ToolResultMaxBytes is the threshold above which post_tool
	// compaction fires (FR-041 default 16 KiB).
	ToolResultMaxBytes int `json:"tool_result_max_bytes,omitempty" yaml:"tool_result_max_bytes,omitempty"`
	// MaxRecursionDepth is the bound on custom_subgraph nesting
	// (FR-045 default 2).
	MaxRecursionDepth int `json:"max_recursion_depth,omitempty" yaml:"max_recursion_depth,omitempty"`
	// DropOldestKeepRecentN is the floor on retained messages for
	// drop_oldest.
	DropOldestKeepRecentN int `json:"drop_oldest_keep_recent_n,omitempty" yaml:"drop_oldest_keep_recent_n,omitempty"`
	// SemanticClusterCount is the cluster count for
	// semantic_cluster.
	SemanticClusterCount int `json:"semantic_cluster_count,omitempty" yaml:"semantic_cluster_count,omitempty"`
	// SummaryProvider + SummaryModel pin the LLM for the summary
	// strategy.
	SummaryProvider string `json:"summary_provider,omitempty" yaml:"summary_provider,omitempty"`
	SummaryModel    string `json:"summary_model,omitempty" yaml:"summary_model,omitempty"`
	// SubgraphInputPort + SubgraphOutputPort pin the ports the
	// custom_subgraph strategy reads/writes from. Defaults
	// "messages" / "messages".
	SubgraphInputPort  string `json:"subgraph_input_port,omitempty" yaml:"subgraph_input_port,omitempty"`
	SubgraphOutputPort string `json:"subgraph_output_port,omitempty" yaml:"subgraph_output_port,omitempty"`
	// CustomGraph is the agentgraph.Graph the custom_subgraph
	// strategy fires. Resolved from the catalog by name; the resolver
	// holds the resolved value once SetConfig has run.
	CustomGraph *agentgraph.Graph `json:"-" yaml:"-"`
	// CustomGraphID is the on-disk reference (graphs library row id)
	// the UI persists. The resolver wires this to CustomGraph by
	// looking it up against the agent-graph library on Resolve.
	CustomGraphID string `json:"custom_graph_id,omitempty" yaml:"custom_graph_id,omitempty"`

	// presence map for cascade attribution. Each entry records the
	// fields the parent layer set explicitly.
	present fieldSet `json:"-" yaml:"-"`
}

// fieldSet is the bitset used by attribution to remember which
// SiteConfig fields were explicitly set on a layer.
type fieldSet uint32

const (
	fEnabled fieldSet = 1 << iota
	fStrategy
	fPreCallThreshold
	fToolResultMaxBytes
	fMaxRecursionDepth
	fDropOldestKeepRecentN
	fSemanticClusterCount
	fSummaryProvider
	fSummaryModel
	fSubgraphInputPort
	fSubgraphOutputPort
	fCustomGraphID
)

// MarkAll declares every field explicitly set. Used by tests + the
// global default seed.
func (s *SiteConfig) MarkAll() {
	s.present = fEnabled | fStrategy | fPreCallThreshold | fToolResultMaxBytes |
		fMaxRecursionDepth | fDropOldestKeepRecentN | fSemanticClusterCount |
		fSummaryProvider | fSummaryModel | fSubgraphInputPort |
		fSubgraphOutputPort | fCustomGraphID
}

// Mark flags one field as explicitly set on this layer.
func (s *SiteConfig) Mark(f fieldSet) { s.present |= f }

// IsSet reports whether the given field bit is set on this layer.
func (s *SiteConfig) IsSet(f fieldSet) bool { return s.present&f != 0 }

// Field-level marking helpers — exported so external callers (RPC
// view, frontend round-trips, tests) can flag individual fields
// without poking at the unexported bitset constants. Each helper
// flips the corresponding presence bit and returns the receiver for
// chaining.

// MarkEnabled flags Enabled as explicitly set.
func (s *SiteConfig) MarkEnabled() *SiteConfig { s.Mark(fEnabled); return s }

// MarkStrategy flags Strategy as explicitly set.
func (s *SiteConfig) MarkStrategy() *SiteConfig { s.Mark(fStrategy); return s }

// MarkPreCallThreshold flags PreCallThreshold as explicitly set.
func (s *SiteConfig) MarkPreCallThreshold() *SiteConfig { s.Mark(fPreCallThreshold); return s }

// MarkToolResultMaxBytes flags ToolResultMaxBytes as explicitly set.
func (s *SiteConfig) MarkToolResultMaxBytes() *SiteConfig { s.Mark(fToolResultMaxBytes); return s }

// MarkMaxRecursionDepth flags MaxRecursionDepth as explicitly set.
func (s *SiteConfig) MarkMaxRecursionDepth() *SiteConfig { s.Mark(fMaxRecursionDepth); return s }

// MarkDropOldestKeepRecentN flags DropOldestKeepRecentN as explicitly set.
func (s *SiteConfig) MarkDropOldestKeepRecentN() *SiteConfig {
	s.Mark(fDropOldestKeepRecentN)
	return s
}

// MarkSemanticClusterCount flags SemanticClusterCount as explicitly set.
func (s *SiteConfig) MarkSemanticClusterCount() *SiteConfig { s.Mark(fSemanticClusterCount); return s }

// MarkSummaryProvider flags SummaryProvider as explicitly set.
func (s *SiteConfig) MarkSummaryProvider() *SiteConfig { s.Mark(fSummaryProvider); return s }

// MarkSummaryModel flags SummaryModel as explicitly set.
func (s *SiteConfig) MarkSummaryModel() *SiteConfig { s.Mark(fSummaryModel); return s }

// MarkSubgraphInputPort flags SubgraphInputPort as explicitly set.
func (s *SiteConfig) MarkSubgraphInputPort() *SiteConfig { s.Mark(fSubgraphInputPort); return s }

// MarkSubgraphOutputPort flags SubgraphOutputPort as explicitly set.
func (s *SiteConfig) MarkSubgraphOutputPort() *SiteConfig { s.Mark(fSubgraphOutputPort); return s }

// MarkCustomGraphID flags CustomGraphID as explicitly set.
func (s *SiteConfig) MarkCustomGraphID() *SiteConfig { s.Mark(fCustomGraphID); return s }

// CompactionConfig is one layer's contribution. Each site has its
// own SiteConfig; un-set sites fall through to the next layer.
type CompactionConfig struct {
	Sites map[Site]SiteConfig `json:"sites,omitempty" yaml:"sites,omitempty"`
}

// ForSite returns the SiteConfig for the given site, or a zero
// SiteConfig if absent. Always safe — never returns nil.
func (c CompactionConfig) ForSite(s Site) SiteConfig {
	if c.Sites == nil {
		return SiteConfig{}
	}
	return c.Sites[s]
}

// Clone returns a deep copy.
func (c CompactionConfig) Clone() CompactionConfig {
	if c.Sites == nil {
		return CompactionConfig{}
	}
	out := CompactionConfig{Sites: make(map[Site]SiteConfig, len(c.Sites))}
	for k, v := range c.Sites {
		out.Sites[k] = v
	}
	return out
}

// ScopeKey identifies one resolution chain. Empty fields fall through
// to the next layer.
type ScopeKey struct {
	ProjectID string
	SessionID string
	RunID     string
	NodeID    string
}

// Resolver is the interface the pipeline uses to fetch the effective
// config for a given scope. Implementations are responsible for
// persistence (memory / yaml / project metadata / etc.).
type Resolver interface {
	// Resolve walks the cascade and returns the effective config.
	Resolve(scope ScopeKey) CompactionConfig
	// Get returns the config persisted at one layer for the given
	// scope id (project id / session id / etc.).
	Get(layer Layer, scopeID string) CompactionConfig
	// Set persists one layer's config. Implementations must clone
	// the input before storing to insulate callers from mutation.
	Set(layer Layer, scopeID string, cfg CompactionConfig)
	// Attribution returns, for the given resolved scope, the layer
	// that supplied each field of each site.
	Attribution(scope ScopeKey) Attribution
}

// Attribution maps Site → field name → contributing Layer. The
// frontend renders this so users can see which scope ultimately
// supplied each value.
type Attribution map[Site]map[string]Layer

// MemoryResolver is the in-process Resolver used by tests + the
// default kernel wiring. Production swaps this for a YAML-backed
// resolver; the surface is identical.
type MemoryResolver struct {
	mu     sync.RWMutex
	layers map[Layer]map[string]CompactionConfig // layer -> scopeID -> config
}

// NewMemoryResolver constructs an empty resolver populated with
// SafeDefaults at the global layer.
func NewMemoryResolver() *MemoryResolver {
	r := &MemoryResolver{layers: make(map[Layer]map[string]CompactionConfig)}
	for _, l := range AllLayers() {
		r.layers[l] = make(map[string]CompactionConfig)
	}
	r.layers[LayerGlobal][""] = SafeDefaults()
	return r
}

// Resolve walks global → project → session → run → node and merges
// the layers into one effective CompactionConfig.
func (r *MemoryResolver) Resolve(scope ScopeKey) CompactionConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := CompactionConfig{Sites: make(map[Site]SiteConfig, len(AllSites()))}
	for _, site := range AllSites() {
		out.Sites[site] = SiteConfig{Enabled: true}
	}
	for _, layer := range AllLayers() {
		key := scopeIDFor(layer, scope)
		cfg, ok := r.layers[layer][key]
		if !ok {
			continue
		}
		for _, site := range AllSites() {
			lc := cfg.ForSite(site)
			out.Sites[site] = mergeSiteConfig(out.Sites[site], lc)
		}
	}
	return out
}

// Get returns the persisted config at the requested layer + scope id.
func (r *MemoryResolver) Get(layer Layer, scopeID string) CompactionConfig {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if m, ok := r.layers[layer]; ok {
		if c, ok := m[scopeID]; ok {
			return c.Clone()
		}
	}
	return CompactionConfig{}
}

// Set persists one layer's config. Empty Sites map is a valid
// "clear-all" signal.
func (r *MemoryResolver) Set(layer Layer, scopeID string, cfg CompactionConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.layers[layer]; !ok {
		r.layers[layer] = make(map[string]CompactionConfig)
	}
	r.layers[layer][scopeID] = cfg.Clone()
}

// Attribution returns the per-(site, field) layer attribution. Walks
// the cascade and records the highest-precedence layer that supplied
// each field.
func (r *MemoryResolver) Attribution(scope ScopeKey) Attribution {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(Attribution, len(AllSites()))
	for _, site := range AllSites() {
		out[site] = make(map[string]Layer)
	}
	for _, layer := range AllLayers() {
		key := scopeIDFor(layer, scope)
		cfg, ok := r.layers[layer][key]
		if !ok {
			continue
		}
		for _, site := range AllSites() {
			lc := cfg.ForSite(site)
			if lc.IsSet(fEnabled) {
				out[site]["enabled"] = layer
			}
			if lc.IsSet(fStrategy) {
				out[site]["strategy"] = layer
			}
			if lc.IsSet(fPreCallThreshold) {
				out[site]["pre_call_threshold"] = layer
			}
			if lc.IsSet(fToolResultMaxBytes) {
				out[site]["tool_result_max_bytes"] = layer
			}
			if lc.IsSet(fMaxRecursionDepth) {
				out[site]["max_recursion_depth"] = layer
			}
			if lc.IsSet(fDropOldestKeepRecentN) {
				out[site]["drop_oldest_keep_recent_n"] = layer
			}
			if lc.IsSet(fSemanticClusterCount) {
				out[site]["semantic_cluster_count"] = layer
			}
			if lc.IsSet(fSummaryProvider) {
				out[site]["summary_provider"] = layer
			}
			if lc.IsSet(fSummaryModel) {
				out[site]["summary_model"] = layer
			}
			if lc.IsSet(fSubgraphInputPort) {
				out[site]["subgraph_input_port"] = layer
			}
			if lc.IsSet(fSubgraphOutputPort) {
				out[site]["subgraph_output_port"] = layer
			}
			if lc.IsSet(fCustomGraphID) {
				out[site]["custom_graph_id"] = layer
			}
		}
	}
	return out
}

// scopeIDFor returns the scope id key the resolver stores against
// for a given layer + scope.
func scopeIDFor(layer Layer, scope ScopeKey) string {
	switch layer {
	case LayerGlobal:
		return ""
	case LayerProject:
		return scope.ProjectID
	case LayerSession:
		return scope.SessionID
	case LayerRun:
		return scope.RunID
	case LayerNode:
		return scope.RunID + "/" + scope.NodeID
	}
	return ""
}

// mergeSiteConfig merges `incoming` on top of `base`. Fields the
// incoming layer flagged as present win; untouched fields stay at the
// base layer's value. Presence bits are OR-ed.
func mergeSiteConfig(base, incoming SiteConfig) SiteConfig {
	out := base
	if incoming.IsSet(fEnabled) {
		out.Enabled = incoming.Enabled
		out.Mark(fEnabled)
	}
	if incoming.IsSet(fStrategy) {
		out.Strategy = incoming.Strategy
		out.Mark(fStrategy)
	}
	if incoming.IsSet(fPreCallThreshold) {
		out.PreCallThreshold = incoming.PreCallThreshold
		out.Mark(fPreCallThreshold)
	}
	if incoming.IsSet(fToolResultMaxBytes) {
		out.ToolResultMaxBytes = incoming.ToolResultMaxBytes
		out.Mark(fToolResultMaxBytes)
	}
	if incoming.IsSet(fMaxRecursionDepth) {
		out.MaxRecursionDepth = incoming.MaxRecursionDepth
		out.Mark(fMaxRecursionDepth)
	}
	if incoming.IsSet(fDropOldestKeepRecentN) {
		out.DropOldestKeepRecentN = incoming.DropOldestKeepRecentN
		out.Mark(fDropOldestKeepRecentN)
	}
	if incoming.IsSet(fSemanticClusterCount) {
		out.SemanticClusterCount = incoming.SemanticClusterCount
		out.Mark(fSemanticClusterCount)
	}
	if incoming.IsSet(fSummaryProvider) {
		out.SummaryProvider = incoming.SummaryProvider
		out.Mark(fSummaryProvider)
	}
	if incoming.IsSet(fSummaryModel) {
		out.SummaryModel = incoming.SummaryModel
		out.Mark(fSummaryModel)
	}
	if incoming.IsSet(fSubgraphInputPort) {
		out.SubgraphInputPort = incoming.SubgraphInputPort
		out.Mark(fSubgraphInputPort)
	}
	if incoming.IsSet(fSubgraphOutputPort) {
		out.SubgraphOutputPort = incoming.SubgraphOutputPort
		out.Mark(fSubgraphOutputPort)
	}
	if incoming.IsSet(fCustomGraphID) {
		out.CustomGraphID = incoming.CustomGraphID
		out.Mark(fCustomGraphID)
	}
	if incoming.CustomGraph != nil {
		out.CustomGraph = incoming.CustomGraph
	}
	return out
}

// SafeDefaults returns the global-layer defaults the kernel applies
// when no config is present. Mirrors FR-041 / FR-045 thresholds.
func SafeDefaults() CompactionConfig {
	pre := SiteConfig{
		Enabled:               true,
		Strategy:              StrategyDropOldest,
		PreCallThreshold:      0.85,
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	pre.MarkAll()
	post := SiteConfig{
		Enabled:               true,
		Strategy:              StrategyDropOldest,
		ToolResultMaxBytes:    16 * 1024,
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	post.MarkAll()
	manual := SiteConfig{
		Enabled:               true,
		Strategy:              StrategySummary,
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	manual.MarkAll()
	return CompactionConfig{Sites: map[Site]SiteConfig{
		SitePreCall:  pre,
		SitePostTool: post,
		SiteManual:   manual,
	}}
}

// SortedSites returns the sites in canonical order — used by
// resolver implementations and tests that iterate over the cascade.
func SortedSites() []Site {
	out := AllSites()
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}
