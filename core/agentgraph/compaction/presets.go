// Presets map the pre-existing 5-tier compaction dial (core/compaction's
// CompactionAggressiveness: off/conservative/balanced/aggressive/maximal,
// invoked from chat_runner.go's runPreSendCompaction) onto FR-041's
// cascading CompactionConfig, so production kernel wiring can boot the
// global layer with something that reproduces today's shipped behaviour
// instead of FR-041's own SafeDefaults.
//
// SitePreCall and SitePostTool are disabled (Enabled: false)
// in every tier preset, including "balanced" (ProductionDefaults). The
// pre-send dial already performs all automatic context-shrinking on
// persisted session history before the kernel ever runs; enabling the
// kernel-side pre_call/post_tool sites in addition would double-compact
// every real conversation the day this ships. The dial's tier is
// preserved here as PreCallThreshold, which Pipeline.Run DOES now
// evaluate against the model's ContextWindow — so a future WP that
// retires the pre-send path can re-enable these sites without losing
// the user's chosen aggressiveness, and the gate is already correct
// when it does. See the Enabled comment below for the two hard
// preconditions (tool-pair-aware trimming, and reconciling the two
// compaction systems) that must land first.
//
// SiteManual is enabled at every tier (including "off"): it is the
// explicit user/graph-author "compact now" trigger, a distinct feature
// from the automatic dial, and today returns a hard error in
// production because no Compactor is wired at all (see
// exec_compute.go's compactExecutor). Wiring FR-041 in fixes that as a
// byproduct.
package compaction

// PresetForTier maps a core/compaction dial tier name to an FR-041
// CompactionConfig. Accepts the same string values as
// core/compaction.CompactionAggressiveness ("off", "conservative",
// "balanced", "aggressive", "maximal"); unknown values fall back to
// "balanced", mirroring core/compaction.Tier's own default behaviour.
func PresetForTier(tier string) CompactionConfig {
	pre := SiteConfig{
		// DISABLED by default, deliberately. Automatic compaction
		// already happens: ChatRunner.runPreSendCompaction fires on
		// every send (chat_runner.go:482), triggering at 80% of the
		// model cap on the "balanced" default tier and using the real
		// tokenizer. Enabling this kernel site as well would compact a
		// second time, on the slice the pre-send pass just produced,
		// with a different token estimate and a different context-window
		// table — strictly worse than either alone.
		//
		// Two hard preconditions before this may ever default to true:
		//  1. DropOldestStrategy must become tool_use/tool_result pair
		//     aware. It trims front-to-back (out = out[1:]) with zero
		//     knowledge of ToolCallID, so it can drop a tool_use and
		//     strand its tool_result — which OpenAI-compat providers
		//     reject outright (see llm_provider_adapter.go:265-271).
		//     That is a hard request failure, not a quality regression.
		//  2. The two systems must be reconciled — either one is
		//     authoritative, or they share a trigger. Note also that
		//     ProductionDefaults() is hardcoded to PresetForTier
		//     ("balanced"), so the user's own aggressiveness dial
		//     (including "off") does not reach this site at all.
		//
		// The threshold machinery below is real and correct; it is what
		// makes this site safe to enable once the above are addressed.
		Enabled:               false,
		Strategy:              StrategyDropOldest,
		PreCallThreshold:      preCallThresholdForTier(tier),
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	pre.MarkAll()

	post := SiteConfig{
		Enabled:               false,
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

// preCallThresholdForTier mirrors core/compaction.Tier's TriggerPct
// numerics without importing core/compaction (that package already
// imports core/agentgraph/... indirectly via other production wiring;
// keeping this package dependency-free of the dial engine avoids an
// import cycle risk and keeps the two systems' coupling to this one
// string-keyed function). The values are duplicated intentionally —
// see core/compaction/policy.go for the source of truth.
func preCallThresholdForTier(tier string) float64 {
	switch tier {
	case "off":
		return 0
	case "conservative":
		return 0.95
	case "aggressive":
		return 0.60
	case "maximal":
		return 0.50
	case "balanced":
		return 0.80
	default:
		return 0.80
	}
}

// ProductionDefaults is the global-layer CompactionConfig production
// kernel construction boots with. It reproduces today's default dial
// tier ("balanced") in FR-041 terms: pre_call/post_tool dormant
// (the pre-send dial remains authoritative for automatic compaction),
// manual enabled. See mission compaction-convergence-01PMDL05 WP01.
func ProductionDefaults() CompactionConfig {
	return PresetForTier("balanced")
}
