// Package compactionpolicy is the single source of truth for the five
// compaction aggressiveness tiers: the enum, its locked numerics, and
// the lookup that turns one into the other.
//
// WHAT THIS PACKAGE IS. It is a *value* package — one enum, its
// numerics, and a function that maps between them. Nothing more. It
// holds no state, performs no I/O, and imports nothing (not even from
// the standard library). It exists so that a consumer which must not
// depend on a compaction *implementation* can still name a tier.
//
// WHY IT HAD TO BE EXTRACTED. Two consumers sit on the wrong side of
// the dependency graph from the compaction engine:
//
//   - core/llm's model profile (model_profile.go,
//     model_profile_schema.go) carries a per-model compaction tier as a
//     persisted user setting. core/llm is imported by core/agentgraph,
//     which is imported by core/agentgraph/compaction — so core/llm
//     naming a tier from the compaction package would close the loop
//     core/llm -> compaction -> agentgraph -> core/llm.
//
//   - the kernel-facing FR-041 presets (core/agentgraph/compaction's
//     presets.go) map a tier onto per-site CompactionConfig. That file
//     used to carry its own hand-copied duplicate of the trigger
//     percentages precisely because it could not import the tier table
//     across that same cycle.
//
// Extracting the tier table to a leaf with no imports resolves both:
// core/llm and the FR-041 presets each depend on the tier *values*
// without depending on anything that compacts.
//
// WHAT THIS PACKAGE IS NOT. It is not a third compaction system. There
// is exactly one compaction package — core/agentgraph/compaction — and
// this is not a second one hiding behind a smaller name. The property
// that keeps that true is mechanical and worth stating plainly: this
// package has no imports and no behaviour beyond a pure lookup. Adding
// either would turn it into an implementation, and an implementation
// reachable from core/llm is the cycle this extraction exists to avoid.
// If you need somewhere to put compaction logic, it goes in
// core/agentgraph/compaction.
package compactionpolicy

// CompactionAggressiveness is the user-facing tier name for the
// compaction dial. The five values are locked by the plan and must not
// be renamed without a settings migration.
type CompactionAggressiveness string

const (
	// AggressivenessOff disables compaction entirely. The chat runner
	// surfaces ErrSessionFull when the session would exceed its model
	// cap; it never silently truncates.
	AggressivenessOff CompactionAggressiveness = "off"

	// AggressivenessConservative triggers compaction only at 95% of cap
	// and folds 20% of the oldest tokens into the summary.
	AggressivenessConservative CompactionAggressiveness = "conservative"

	// AggressivenessBalanced is the default tier: trigger at 80% of
	// cap, summarize 30% of oldest tokens.
	AggressivenessBalanced CompactionAggressiveness = "balanced"

	// AggressivenessAggressive triggers earlier (60% of cap) and folds
	// a larger fraction (40%) into the summary.
	AggressivenessAggressive CompactionAggressiveness = "aggressive"

	// AggressivenessMaximal switches modes entirely: every turn the
	// rolling-summary path runs, collapsing everything outside the
	// recent window into a single rolling summary message.
	AggressivenessMaximal CompactionAggressiveness = "maximal"
)

// CompactionMode is the engine path a tier selects: ModeNone skips
// compaction, ModeThreshold gates on a percent-of-cap trigger, and
// ModeRolling runs every turn.
type CompactionMode int

const (
	// ModeNone disables compaction. Used by AggressivenessOff.
	ModeNone CompactionMode = iota

	// ModeThreshold gates on TriggerPct. Used by the three middle
	// tiers (conservative, balanced, aggressive).
	ModeThreshold

	// ModeRolling runs every turn. Used by AggressivenessMaximal.
	ModeRolling
)

// TierParams carries the locked numerics for a tier.
//
// TriggerPct and SummarizePct are zero in non-threshold modes;
// consumers must check Mode before reading them.
type TierParams struct {
	// TriggerPct is the current/cap fraction at which threshold-mode
	// compaction kicks off. Zero when Mode != ModeThreshold.
	TriggerPct float64

	// SummarizePct is the fraction of the oldest tokens to fold into
	// the summary. Zero when Mode != ModeThreshold.
	SummarizePct float64

	// Mode selects the engine path.
	Mode CompactionMode
}

// Tier returns the locked parameters for a given aggressiveness tier.
// Unknown or empty values fall back to the balanced tier so callers
// never have to worry about a missing setting.
//
// The numerics here are the single source of truth. Frontend tooltips,
// chat-runner trigger checks, and settings UI all read through this
// function (directly or via the Compaction.GetTierExplain RPC).
func Tier(s CompactionAggressiveness) TierParams {
	switch s {
	case AggressivenessOff:
		return TierParams{Mode: ModeNone}
	case AggressivenessConservative:
		return TierParams{TriggerPct: 0.95, SummarizePct: 0.20, Mode: ModeThreshold}
	case AggressivenessBalanced:
		return TierParams{TriggerPct: 0.80, SummarizePct: 0.30, Mode: ModeThreshold}
	case AggressivenessAggressive:
		return TierParams{TriggerPct: 0.60, SummarizePct: 0.40, Mode: ModeThreshold}
	case AggressivenessMaximal:
		return TierParams{Mode: ModeRolling}
	default:
		return Tier(AggressivenessBalanced)
	}
}
