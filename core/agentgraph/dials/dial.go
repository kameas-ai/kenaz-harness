package dials

import "time"

// Scope identifies which cascading layer a DialConfig belongs to.
// Layers are ordered weakest → strongest in DefaultLayerOrder.
type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeProject  Scope = "project"
	ScopeSession  Scope = "session"
	ScopePerGraph Scope = "graph"
	ScopePerRun   Scope = "run"
)

// DefaultLayerOrder returns the cascade order from weakest to
// strongest. Higher-precedence layers override lower ones field-by-
// field.
func DefaultLayerOrder() []Scope {
	return []Scope{ScopeGlobal, ScopeProject, ScopeSession, ScopePerGraph, ScopePerRun}
}

// DialConfig is a sparse per-layer override. Each pointer field is
// "unset when nil"; Resolve() walks the layers and copies the first
// non-nil pointer into the result, recording which layer set the
// value. Pointer fields keep zero-valued explicit overrides distinct
// from "not specified".
type DialConfig struct {
	// MaxTokensPerRun caps total LLM tokens consumed in one run.
	MaxTokensPerRun *int `json:"maxTokensPerRun,omitempty"`
	// MaxWallclock caps the elapsed wall-clock for one run.
	MaxWallclock *time.Duration `json:"maxWallclock,omitempty"`
	// MaxLLMCalls caps the number of LLM dispatches per run.
	MaxLLMCalls *int `json:"maxLLMCalls,omitempty"`
	// MaxToolCalls caps tool dispatches per run.
	MaxToolCalls *int `json:"maxToolCalls,omitempty"`
	// MaxCostUSD caps approximate USD spend per run.
	MaxCostUSD *float64 `json:"maxCostUSD,omitempty"`
	// PlanVerbosity selects how much the PlanNode emits ("terse",
	// "normal", "verbose"). Empty string ⇒ unset.
	PlanVerbosity *string `json:"planVerbosity,omitempty"`
	// AskThreshold controls when ReflectNode escalates to AskNode
	// (0.0..1.0). Lower = more interruptions.
	AskThreshold *float64 `json:"askThreshold,omitempty"`
	// ReflectFrequency tunes how often ReflectNode fires (every N
	// turns).
	ReflectFrequency *int `json:"reflectFrequency,omitempty"`
	// CompactionAggressiveness biases the compaction subsystem
	// (0.0 = never compact, 1.0 = compact at every opportunity).
	CompactionAggressiveness *float64 `json:"compactionAggressiveness,omitempty"`
	// ReviewIterationsCap limits ReviewNode iteration count.
	ReviewIterationsCap *int `json:"reviewIterationsCap,omitempty"`
	// MemoryHooksEnabled toggles greedy memory writes wholesale.
	MemoryHooksEnabled *bool `json:"memoryHooksEnabled,omitempty"`
	// MemoryPruneInterval is the prune-sweep cadence (Bundle E
	// WP15). Defaults to 24h.
	MemoryPruneInterval *time.Duration `json:"memoryPruneInterval,omitempty"`
	// ManifestDriftMode controls how the runtime responds when a graph
	// node's manifest fingerprint differs from the fingerprint at author
	// time. Values:
	//   "warn"  — emit a manifest_drift warning event and continue (default).
	//   "block" — refuse to run the graph and return an error.
	// Empty string ⇒ unset (inherits from lower layer; global default is "warn").
	ManifestDriftMode *string `json:"manifestDriftMode,omitempty"`
}

// IntPtr / DurPtr / Float64Ptr / StringPtr / BoolPtr — pointer-of-
// primitive helpers for callers (tests, config readers) that want to
// declare a one-field override inline. They are the same trick used
// by k8s' apimachinery.
func IntPtr(v int) *int                        { return &v }
func DurPtr(v time.Duration) *time.Duration    { return &v }
func Float64Ptr(v float64) *float64            { return &v }
func StringPtr(v string) *string               { return &v }
func BoolPtr(v bool) *bool                     { return &v }

// EffectiveDials is the resolved cascade output. Every field has a
// concrete value (not a pointer) plus the Scope that contributed it.
// Used by the inspector UI: "MaxTokensPerRun = 16000 (from project)".
type EffectiveDials struct {
	MaxTokensPerRun          int
	MaxTokensPerRunFrom      Scope
	MaxWallclock             time.Duration
	MaxWallclockFrom         Scope
	MaxLLMCalls              int
	MaxLLMCallsFrom          Scope
	MaxToolCalls             int
	MaxToolCallsFrom         Scope
	MaxCostUSD               float64
	MaxCostUSDFrom           Scope
	PlanVerbosity            string
	PlanVerbosityFrom        Scope
	AskThreshold             float64
	AskThresholdFrom         Scope
	ReflectFrequency         int
	ReflectFrequencyFrom     Scope
	CompactionAggressiveness float64
	CompactionAggressivenessFrom Scope
	ReviewIterationsCap      int
	ReviewIterationsCapFrom  Scope
	MemoryHooksEnabled       bool
	MemoryHooksEnabledFrom   Scope
	MemoryPruneInterval      time.Duration
	MemoryPruneIntervalFrom  Scope
}

// Defaults returns the harness-default EffectiveDials. Used by the
// resolver when no layer set a field. Mirrors the spec's per-run
// caps + dial defaults.
func Defaults() EffectiveDials {
	return EffectiveDials{
		MaxTokensPerRun:          200_000,
		MaxTokensPerRunFrom:      ScopeGlobal,
		MaxWallclock:             10 * time.Minute,
		MaxWallclockFrom:         ScopeGlobal,
		MaxLLMCalls:              200,
		MaxLLMCallsFrom:          ScopeGlobal,
		MaxToolCalls:             200,
		MaxToolCallsFrom:         ScopeGlobal,
		MaxCostUSD:               5.0,
		MaxCostUSDFrom:           ScopeGlobal,
		PlanVerbosity:            "normal",
		PlanVerbosityFrom:        ScopeGlobal,
		AskThreshold:             0.4,
		AskThresholdFrom:         ScopeGlobal,
		ReflectFrequency:         1,
		ReflectFrequencyFrom:     ScopeGlobal,
		CompactionAggressiveness: 0.5,
		CompactionAggressivenessFrom: ScopeGlobal,
		ReviewIterationsCap:      3,
		ReviewIterationsCapFrom:  ScopeGlobal,
		MemoryHooksEnabled:       true,
		MemoryHooksEnabledFrom:   ScopeGlobal,
		MemoryPruneInterval:      24 * time.Hour,
		MemoryPruneIntervalFrom:  ScopeGlobal,
	}
}
