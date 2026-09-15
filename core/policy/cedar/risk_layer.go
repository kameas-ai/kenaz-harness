package cedar

import (
	"context"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// ThreeLayerResolve implements risk-rated-autonomy-01PMRA01's layering:
//
//	1. Cedar forbid  -> Deny.    Hard. Never overridable by a rating.
//	2. Cedar permit  -> Allow.   Hard. Never re-litigated by a rating.
//	3. Cedar n/a     -> Confirm. Pending a real risk rating (WP03-WP06),
//	   this is a stub: every unmatched action asks. That stub is
//	   deliberate and is WP02's whole safety property — it converts
//	   Engine.Evaluate's NotApplicable, which enforce() has always mapped
//	   to nil ("default-allow"), into a decision that a human must
//	   answer, one layer up from where that hole has lived until now.
//
// The returned Decision's Outcome is always one of Allow / Deny /
// Confirm — never NotApplicable. Callers that already branch on Allow /
// Deny / Confirm (rather than folding "not Deny" into "Allow") can drop
// this straight in. g == nil is treated as AllowAll (matching every
// other Gate-typed helper in this package), which — because AllowAll
// always answers NotApplicable — resolves to layer 3, i.e. Confirm: with
// no engine wired, the safe direction is "ask", not "allow everything".
//
// This function is a pure re-interpretation of a SINGLE Evaluate call; it
// does not itself call a rater and never will — a blocking LLM call has
// no business inside the Cedar-evaluation package. WP05 wires a
// RiskRater by WRAPPING this function's Confirm result at its one
// caller (core/rpc/views/agentgraph/chat/kernel_tool_adapter.go's
// resolveConfirmEach / resolveLayer3Rating): a Confirm outcome from here
// is consulted against the rater ONLY there, so this function's own
// behaviour — Deny/Allow/Confirm from a single Evaluate call — is
// unchanged by WP05 landing. Nothing upstream reaches an unmatched
// action's disposition any other way: resolveConfirmEach is still the
// one call site for BOTH this function and the rater.
//
// threshold is risk-rated-autonomy-01PMRA01 FR-003's autonomy dial
// (autonomy.ResolvedKnobs.RiskThreshold, 0-100). WP02/WP03 have no rater
// to compare a score against yet, so threshold cannot change the
// OUTCOME — every layer-3 case is still Confirm. What it already governs
// for real, per FR-008 ("the rater is never on the hot path when it
// cannot change the answer"): threshold<=0 (strict tier) means the
// eventual rater must never even be called, which this function records
// in the returned Decision.Reason now so the audit trail (FR-007) and
// WP05's implementation both have one place that already encodes the
// bypass rule, rather than WP05 inventing it fresh.
func ThreeLayerResolve(
	ctx context.Context,
	g Gate,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	contextAttrs map[cedarlib.String]cedarlib.Value,
	threshold int,
) Decision {
	if g == nil {
		g = AllowAll{}
	}
	d := g.Evaluate(ctx, principal, action, resource, contextAttrs)
	switch d.Outcome {
	case Deny:
		// Layer 1: an explicit forbid. Hard stop, unchanged from today.
		if d.Reason == "" {
			d.Reason = "layer 1: forbid policy matched"
		}
		return d
	case Allow:
		// Layer 2: an explicit permit. Hard allow, unchanged from today.
		if d.Reason == "" {
			d.Reason = "layer 2: permit policy matched"
		}
		return d
	default:
		// Layer 3: Cedar had no opinion (NotApplicable), or — if a
		// future Gate implementation ever returns Confirm itself, which
		// none does today — that too lands here rather than falling
		// through to an implicit allow.
		d.Outcome = Confirm
		if threshold <= 0 {
			// FR-008 + FR-003 (strict tier, threshold 0): the rater must
			// be skipped, not called and ignored — a call whose result
			// cannot change the answer is waste, and every layer-3 case
			// already always asks at threshold 0. WP05's rater call
			// site must check this exact condition before spending a
			// model call.
			d.Reason = "layer 3: threshold=0 — every unmatched action asks and the rater is bypassed (FR-008)"
		} else {
			// This function itself still always returns Confirm here —
			// see ThreeLayerResolve's doc comment above: WP05's rater is
			// consulted by the CALLER against this exact Confirm result,
			// not inside this function. A caller with no rater wired (or
			// one that hits an error, per RiskRater's fail-closed
			// contract) leaves every unmatched action at Confirm, which
			// is strictly safer than main's pre-mission NotApplicable ->
			// allow.
			d.Reason = "layer 3: no Cedar policy matched; pending the caller's rating (WP05) — asking"
		}
		return d
	}
}
