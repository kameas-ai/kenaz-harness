package dials

import "time"

// Layer pairs a Scope with its DialConfig. Resolve() takes a slice of
// these in weakest-first order and folds them into an EffectiveDials.
type Layer struct {
	Scope  Scope
	Config DialConfig
}

// Resolve walks layers in DefaultLayerOrder and produces the
// effective dial values plus the contributing-layer attribution. If
// layers contain entries in an unexpected order, the slice ordering
// is what matters — the function does NOT re-sort.
//
// Resolve is pure: same input always yields same output, and it does
// not mutate any input. Callers that have unsorted layers should
// pass them in the order they want precedence checked.
func Resolve(layers []Layer) EffectiveDials {
	eff := Defaults()
	for _, l := range layers {
		applyLayer(&eff, l)
	}
	return eff
}

// applyLayer copies any non-nil pointer field from cfg into eff and
// stamps the contributing scope. Higher-precedence layers (later in
// the slice) overwrite earlier ones field-by-field — the cascade.
func applyLayer(eff *EffectiveDials, l Layer) {
	c := l.Config
	if c.MaxTokensPerRun != nil {
		eff.MaxTokensPerRun = *c.MaxTokensPerRun
		eff.MaxTokensPerRunFrom = l.Scope
	}
	if c.MaxWallclock != nil {
		eff.MaxWallclock = *c.MaxWallclock
		eff.MaxWallclockFrom = l.Scope
	}
	if c.MaxLLMCalls != nil {
		eff.MaxLLMCalls = *c.MaxLLMCalls
		eff.MaxLLMCallsFrom = l.Scope
	}
	if c.MaxToolCalls != nil {
		eff.MaxToolCalls = *c.MaxToolCalls
		eff.MaxToolCallsFrom = l.Scope
	}
	if c.MaxCostUSD != nil {
		eff.MaxCostUSD = *c.MaxCostUSD
		eff.MaxCostUSDFrom = l.Scope
	}
	if c.PlanVerbosity != nil {
		eff.PlanVerbosity = *c.PlanVerbosity
		eff.PlanVerbosityFrom = l.Scope
	}
	if c.AskThreshold != nil {
		eff.AskThreshold = *c.AskThreshold
		eff.AskThresholdFrom = l.Scope
	}
	if c.ReflectFrequency != nil {
		eff.ReflectFrequency = *c.ReflectFrequency
		eff.ReflectFrequencyFrom = l.Scope
	}
	if c.CompactionAggressiveness != nil {
		eff.CompactionAggressiveness = *c.CompactionAggressiveness
		eff.CompactionAggressivenessFrom = l.Scope
	}
	if c.ReviewIterationsCap != nil {
		eff.ReviewIterationsCap = *c.ReviewIterationsCap
		eff.ReviewIterationsCapFrom = l.Scope
	}
	if c.MemoryHooksEnabled != nil {
		eff.MemoryHooksEnabled = *c.MemoryHooksEnabled
		eff.MemoryHooksEnabledFrom = l.Scope
	}
	if c.MemoryPruneInterval != nil {
		eff.MemoryPruneInterval = *c.MemoryPruneInterval
		eff.MemoryPruneIntervalFrom = l.Scope
	}
}

// Bump applies a delta to a DialConfig in-place. Used by
// `BumpAndResume` — when a cap fires, the user can bump the offending
// dial and resume; the call site translates a "double the token cap"
// into a Bump that produces a fresh DialConfig stored on the run
// scope.
//
// Only numeric fields can be bumped. Booleans + strings are left
// untouched. Nil pointer fields stay nil (Bump never invents
// overrides — callers must explicitly set the dial first).
func (c *DialConfig) Bump(delta DialDelta) {
	if c == nil {
		return
	}
	if delta.AddTokensPerRun != 0 && c.MaxTokensPerRun != nil {
		v := *c.MaxTokensPerRun + delta.AddTokensPerRun
		c.MaxTokensPerRun = &v
	}
	if delta.AddWallclock != 0 && c.MaxWallclock != nil {
		v := *c.MaxWallclock + delta.AddWallclock
		c.MaxWallclock = &v
	}
	if delta.AddLLMCalls != 0 && c.MaxLLMCalls != nil {
		v := *c.MaxLLMCalls + delta.AddLLMCalls
		c.MaxLLMCalls = &v
	}
	if delta.AddToolCalls != 0 && c.MaxToolCalls != nil {
		v := *c.MaxToolCalls + delta.AddToolCalls
		c.MaxToolCalls = &v
	}
	if delta.AddCostUSD != 0 && c.MaxCostUSD != nil {
		v := *c.MaxCostUSD + delta.AddCostUSD
		c.MaxCostUSD = &v
	}
}

// DialDelta is an additive bump applied via Bump() or
// BumpAndResume. Zero fields are ignored.
type DialDelta struct {
	AddTokensPerRun int           `json:"addTokensPerRun,omitempty"`
	AddWallclock    time.Duration `json:"addWallclock,omitempty"`
	AddLLMCalls     int           `json:"addLLMCalls,omitempty"`
	AddToolCalls    int           `json:"addToolCalls,omitempty"`
	AddCostUSD      float64       `json:"addCostUSD,omitempty"`
}
