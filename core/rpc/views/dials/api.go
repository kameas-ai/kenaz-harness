// Package dials is the view-scoped accessor for cascading dial
// configuration (Bundle E WP17 of agent-kernel-graph). The frontend's
// DialsView reads effective values + per-layer attribution from this
// surface and writes per-scope overrides through SetDials.
//
// The wire shapes mirror core/agentgraph/dials but expose JSON-friendly
// (no pointer-of-primitive) fields so the Wails bridge can serialise
// them cleanly. Pointer-encoded "unset" semantics are preserved by the
// `*Set` boolean siblings — when `MaxTokensPerRunSet` is false, the
// frontend treats `MaxTokensPerRun` as "use cascade default".
package dials

import (
	"context"
	"time"
)

// DialConfig is the wire shape for one cascading layer's overrides.
// Each `*Set` boolean toggles whether the matching value is an
// explicit override or "use cascade".
type DialConfig struct {
	MaxTokensPerRun             int           `json:"maxTokensPerRun,omitempty"`
	MaxTokensPerRunSet          bool          `json:"maxTokensPerRunSet,omitempty"`
	MaxWallclockSeconds         int           `json:"maxWallclockSeconds,omitempty"`
	MaxWallclockSet             bool          `json:"maxWallclockSet,omitempty"`
	MaxLLMCalls                 int           `json:"maxLLMCalls,omitempty"`
	MaxLLMCallsSet              bool          `json:"maxLLMCallsSet,omitempty"`
	MaxToolCalls                int           `json:"maxToolCalls,omitempty"`
	MaxToolCallsSet             bool          `json:"maxToolCallsSet,omitempty"`
	MaxCostUSD                  float64       `json:"maxCostUSD,omitempty"`
	MaxCostUSDSet               bool          `json:"maxCostUSDSet,omitempty"`
	PlanVerbosity               string        `json:"planVerbosity,omitempty"`
	PlanVerbositySet            bool          `json:"planVerbositySet,omitempty"`
	AskThreshold                float64       `json:"askThreshold,omitempty"`
	AskThresholdSet             bool          `json:"askThresholdSet,omitempty"`
	ReflectFrequency            int           `json:"reflectFrequency,omitempty"`
	ReflectFrequencySet         bool          `json:"reflectFrequencySet,omitempty"`
	CompactionAggressiveness    float64       `json:"compactionAggressiveness,omitempty"`
	CompactionAggressivenessSet bool          `json:"compactionAggressivenessSet,omitempty"`
	ReviewIterationsCap         int           `json:"reviewIterationsCap,omitempty"`
	ReviewIterationsCapSet      bool          `json:"reviewIterationsCapSet,omitempty"`
	MemoryHooksEnabled          bool          `json:"memoryHooksEnabled,omitempty"`
	MemoryHooksEnabledSet       bool          `json:"memoryHooksEnabledSet,omitempty"`
	MemoryPruneIntervalSeconds  int           `json:"memoryPruneIntervalSeconds,omitempty"`
	MemoryPruneIntervalSet      bool          `json:"memoryPruneIntervalSet,omitempty"`
	UpdatedAt                   time.Time     `json:"updatedAt,omitempty"`
}

// EffectiveField pairs an effective value with the Scope that
// contributed it. Used by the inspector UI to render the cascade
// attribution row.
type EffectiveField[T any] struct {
	Value T      `json:"value"`
	From  string `json:"from"`
}

// EffectiveDials is the resolved cascade (read-only output of
// GetEffective). Each numeric field's `From` records the contributing
// scope ("global", "project", "session", "graph", "run").
type EffectiveDials struct {
	MaxTokensPerRun          EffectiveField[int]     `json:"maxTokensPerRun"`
	MaxWallclockSeconds      EffectiveField[int]     `json:"maxWallclockSeconds"`
	MaxLLMCalls              EffectiveField[int]     `json:"maxLLMCalls"`
	MaxToolCalls             EffectiveField[int]     `json:"maxToolCalls"`
	MaxCostUSD               EffectiveField[float64] `json:"maxCostUSD"`
	PlanVerbosity            EffectiveField[string]  `json:"planVerbosity"`
	AskThreshold             EffectiveField[float64] `json:"askThreshold"`
	ReflectFrequency         EffectiveField[int]     `json:"reflectFrequency"`
	CompactionAggressiveness EffectiveField[float64] `json:"compactionAggressiveness"`
	ReviewIterationsCap      EffectiveField[int]     `json:"reviewIterationsCap"`
	MemoryHooksEnabled       EffectiveField[bool]    `json:"memoryHooksEnabled"`
	MemoryPruneIntervalSeconds EffectiveField[int]   `json:"memoryPruneIntervalSeconds"`
}

// DialDelta is the additive bump used by BumpAndResume. Mirrors
// core/agentgraph/dials.DialDelta but uses seconds (int) instead of
// time.Duration so the Wails bridge can serialise it.
type DialDelta struct {
	AddTokensPerRun     int     `json:"addTokensPerRun,omitempty"`
	AddWallclockSeconds int     `json:"addWallclockSeconds,omitempty"`
	AddLLMCalls         int     `json:"addLLMCalls,omitempty"`
	AddToolCalls        int     `json:"addToolCalls,omitempty"`
	AddCostUSD          float64 `json:"addCostUSD,omitempty"`
}

// ScopeKey identifies the layer being read or written. ID is the
// project / session / graph / run id; empty for the global layer.
type ScopeKey struct {
	Scope string `json:"scope"`
	ID    string `json:"id,omitempty"`
}

// DialsAPI is the view-scoped accessor.
type DialsAPI interface {
	// GetDials returns the persisted DialConfig for a single layer
	// (no cascade resolution). Empty config when nothing is stored.
	GetDials(ctx context.Context, key ScopeKey) (DialConfig, error)
	// SetDials persists overrides for a single layer. The backend
	// store implementation lives in core; this surface is the wire
	// boundary.
	SetDials(ctx context.Context, key ScopeKey, cfg DialConfig) error
	// GetEffective resolves the full cascade for a (project, session,
	// graph, run) tuple and returns the effective value + attribution
	// per field. Any of the IDs may be empty.
	GetEffective(ctx context.Context, projectID, sessionID, graphID, runID string) (EffectiveDials, error)
	// BumpAndResume bumps the cap-hit dial on the run scope and
	// resumes the run. Returns ErrNoPause when the run is not
	// awaiting a cap-bump resume.
	BumpAndResume(ctx context.Context, runID string, delta DialDelta) error
}
