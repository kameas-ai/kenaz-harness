// Presets map the five-tier compaction dial (off / conservative /
// balanced / aggressive / maximal) onto FR-041's cascading
// CompactionConfig. The dial is the user-facing control; this file is
// where a tier becomes per-site configuration.
//
// HISTORY, BECAUSE THE POSTURE HERE HAS BEEN WRONG BEFORE.
// compaction-convergence-01PMDL05 WP01 wired FR-041 into production but
// set Enabled: false for SitePreCall AND SitePostTool at *every* tier,
// leaving only SiteManual live. That was a defensible safe landing at
// the time — the dial was still running as a pre-kernel pass
// on the chat surface that this package could not see, so
// enabling a kernel-side site would have compacted the same
// conversation twice on the same turn. But it also meant the entire
// automatic surface was configured and unreachable, which is the
// "wired but default-off" shape agentgraph-total-convergence-01PMGX01
// exists to eliminate.
//
// WP08 removes the reason. There is no pre-kernel pass any more: the
// dial runs as StrategySessionRewrite at SiteManual, reached from the
// `compact` node that chat_default.yaml places between history_read and
// the agent loop. With the second system gone, each site's posture can
// finally be decided on its own merits:
//
//   - SiteManual  — ENABLED at every tier, strategy session_rewrite.
//     This is the dial. "off" is enabled too: the off tier is not
//     "don't run", it is "don't compact, and say so honestly when the
//     session is genuinely full", and that decision is made inside the
//     strategy where the tier semantics live.
//
//   - SitePreCall — ENABLED at every tier except "off", strategy
//     drop_oldest, threshold from the tier. Both preconditions
//     01PMDL05 recorded for this site are now met:
//     (1) DropOldestStrategy became tool-pair aware — it trims in
//     tool_use/tool_result atomic units (dropOldestUnits), so it can no
//     longer strand a tool_result and trigger a hard provider rejection;
//     (2) the two compaction systems are reconciled, which is this WP.
//     A second gate sits in front of this site, but state it precisely,
//     because an over-claim here is how a site ends up firing where
//     nobody expected it: the growth watermark refuses the first
//     pre-call visit of a run and admits later ones only once the live
//     context has grown past the run's baseline — AND ONLY WHEN THE Env
//     CARRIES ONE. A nil Env.AutoCompaction means no watermark and no
//     gate. Both production paths arm one (the chat runner on the Env it
//     builds, and EnvDeps.applyTo for every graph-authored run), so in
//     the shipped harness the guarantee holds everywhere. A caller that
//     constructs a bare Env and wires a Compactor onto it directly gets
//     an ungated site, which is the documented meaning of nil and not an
//     oversight — but it is also why this paragraph does not simply say
//     "refused by construction".
//
//   - SitePostTool — DISABLED at every tier, and this one is honest
//     rather than circular. The work this site was designed to do —
//     bounding an oversized single tool result — is now done
//     unconditionally and earlier by ToolResultCap at dispatch
//     (core/agentgraph/tool_output_cap.go, applied in exec_dispatch.go
//     before the bytes ever become a Message). Enabling this site would
//     be a second, config-gated trim of already-capped bytes, and it
//     would spend an LLM call to do it. If the cap is ever removed this
//     decision must be revisited; while the cap exists, a per-tier
//     toggle here would be a control that changes nothing.
package compaction

import "github.com/kameas-ai/kenaz-harness/core/compactionpolicy"

// PresetForTier maps a dial tier name to an FR-041 CompactionConfig.
// Accepts the tier strings the settings surface stores ("off",
// "conservative", "balanced", "aggressive", "maximal"); unknown values
// fall back to "balanced", mirroring the tier table's own default.
func PresetForTier(tier string) CompactionConfig {
	pre := SiteConfig{
		// Enabled everywhere except "off". "off" means the user asked
		// for no automatic compaction at all; honouring that at the
		// automatic site is the whole point of the tier, and the
		// session-full path at SiteManual is what tells them when the
		// choice has consequences.
		Enabled:               tier != "off",
		Strategy:              StrategyDropOldest,
		PreCallThreshold:      preCallThresholdForTier(tier),
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	pre.MarkAll()

	post := SiteConfig{
		// See the package comment: ToolResultCap already bounds tool
		// results unconditionally at dispatch. This is not a deferral —
		// there is no work left for this site to do.
		Enabled:               false,
		Strategy:              StrategyDropOldest,
		ToolResultMaxBytes:    16 * 1024,
		MaxRecursionDepth:     DefaultMaxRecursionDepth,
		DropOldestKeepRecentN: 2,
		SemanticClusterCount:  4,
	}
	post.MarkAll()

	manual := SiteConfig{
		// The dial itself. Enabled at every tier including "off" —
		// the tier's meaning is resolved inside the strategy, which is
		// the only place that can distinguish "don't compact" from
		// "don't compact AND the session is over cap, so fail honestly".
		Enabled:  true,
		Strategy: StrategySessionRewrite,
		// CK-04/CK-05/CK-06 justify(blocker: "pipeline.go's threshold
		// gate is unconditionally skipped for SiteManual
		// (`req.Site != SiteManual`), by design — a manual 'compact now'
		// means it, and honouring an implicit threshold on an explicit
		// request would be the wrong behaviour, not a missing wire",
		// owner: alec, date: 2026-08-29; chat-turn-integrity-01PMZ606
		// WP13): PreCallThreshold is computed, marked and round-tripped
		// here for SiteManual same as the other two sites, but
		// pipeline.go's Run never reads siteCfg.PreCallThreshold for
		// this site — the field is structurally unreachable for
		// SiteManual specifically, not merely unread today.
		PreCallThreshold:      preCallThresholdForTier(tier),
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

// rollingPreCallThreshold is the pre-call threshold for the one tier the
// tier table cannot supply a trigger percentage for.
//
// compactionpolicy.Tier reports ModeRolling for "maximal" with a zero
// TriggerPct, and that zero is meaningful rather than missing: rolling
// mode runs the session rewrite on *every* turn, so there is no
// percent-of-cap gate to express. The automatic pre-call site is a
// different mechanism that still needs a numeric, and 0.50 is the value
// it has always used — the most aggressive threshold of any tier, which
// is the right posture underneath a tier whose whole premise is
// compacting constantly.
//
// This constant is NOT a duplicate of anything in the tier table. It is
// the single numeric the table genuinely does not carry.
const rollingPreCallThreshold = 0.50

// preCallThresholdForTier is the fraction of the model's context window
// above which the automatic pre-call site compacts.
//
// It reads the tier table (core/compactionpolicy) rather than restating
// it. Until WP10a this function held a hand-copied second copy of
// 0.95/0.80/0.60, because the tier table lived in the session
// compaction package and this package could not import it without a
// cycle — the session layer's wiring reaches session storage, and this
// package is consumed by the kernel. WP10a extracted the table to
// core/compactionpolicy, a leaf with no imports at all, which both
// packages can depend on. There is now one place the trigger
// percentages are written down.
//
// The mapping from a tier's mode to a pre-call threshold:
//
//   - ModeNone ("off") → 0. The user asked for no automatic compaction;
//     the site is disabled at this tier anyway (see PresetForTier), so
//     the threshold is moot, but zero is the honest value.
//   - ModeThreshold (conservative / balanced / aggressive) → the tier's
//     own TriggerPct, read straight from the table.
//   - ModeRolling ("maximal") → rollingPreCallThreshold; see above for
//     why the table has nothing to offer here.
//
// Unknown tier strings need no case of their own: compactionpolicy.Tier
// already falls back to balanced, so they land on 0.80 through the
// ModeThreshold branch.
func preCallThresholdForTier(tier string) float64 {
	params := compactionpolicy.Tier(compactionpolicy.CompactionAggressiveness(tier))
	switch params.Mode {
	case compactionpolicy.ModeNone:
		return 0
	case compactionpolicy.ModeRolling:
		return rollingPreCallThreshold
	default:
		return params.TriggerPct
	}
}

// ProductionDefaults is the global-layer CompactionConfig production
// kernel construction boots with: the shipped default dial tier
// ("balanced") expressed in FR-041 terms.
//
// The per-session tier the user actually chose reaches the pipeline
// through the session-rewrite strategy the chat surface binds per run,
// which reads the live settings value on every turn. This global layer
// is the floor for runs that have no chat session behind them.
func ProductionDefaults() CompactionConfig {
	return PresetForTier("balanced")
}
