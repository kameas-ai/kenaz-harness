// Package advice defines the advisor seam mission laya-advisors-01LAYA001
// generalizes from risk-rated-autonomy-01PMRA01's RiskRater
// (core/policy/risk). Where a RiskRater answers "how risky is this tool
// call" to gate a dispatch, an Advisor answers a cheap, low-stakes,
// BINARY judgment call the harness makes many times a session — "is this
// exchange a separable thread", "will compacting now help", "is the
// model struggling" — and NEVER blocks anything: every failure mode
// degrades to "no advice" (see ErrNoAdvice).
//
// # What ships in this WP (WP01-03: the seam; WP04-06: the trio)
//
// WP01-03 shipped the seam and the laya model-resolution ladder with
// ZERO advice kinds registered in production — mirroring how
// core/policy/risk's WP04 shipped the RiskRater contract and a
// race-safe fake before WP05 wired a real implementation. WP04-06 (this
// package's kinds/ subpackages: branchnow, compactnow, escalatemodel)
// register the v1 trio per tasks.md's DESIGN-LOCKED REVISION — but NOT
// against an LLM or laya. Owner ruling 2026-09-29 (spec §2e-0, "no
// LLMs — advisors use laya and other ML models only") and the kenaz-ml
// integration design's five-rung graduation ladder (research/
// kenaz-ml-integration-design.md §5.4: "R1 Heuristic-live — the rule
// serves the kind") together mean the trio's day-1 backend is
// HeuristicAdvisor (heuristic.go): pure Go arithmetic over each kind's
// Features, zero model calls. Every Recommendation it produces carries
// Model="heuristic/<name>-v1", Rung=RungHeuristic, Unbenchmarked=true —
// honest about being a rule, never dressed up as a laya or LLM call.
//
// # Contract summary
//
//   - AdviceKind (kind.go) is the registry entry: a stable id, a typed
//     feature extractor, a prompt_version, a render function, and a
//     safety class (reversible / suggest_only) enforced in Go via
//     RequireCanAutoAct — a compile-visible switch, never a UI
//     convention (spec §3, the #78 lesson).
//   - Advisor (advisor.go) mirrors risk.RiskRater's shape: Recommend
//     takes already-extracted Features and returns a Recommendation or
//     ErrNoAdvice. Unlike the rater, NOTHING waits on an Advisor call —
//     every caller must be prepared for Recommend to be slow, absent, or
//     wrong, and treat all three identically (no advice shown).
//   - HeuristicAdvisor (heuristic.go) is the production Advisor for the
//     v1 trio: a per-kind HeuristicFunc registered via RegisterHeuristic,
//     a per-kind runtime KindGate (SetKindGate) implementing AC-02's
//     "independently disableable" requirement as a real branch rather
//     than a registry-level toggle, and the SAME session-scoped LRU
//     cache + two skip paths (SessionContext.Moot; a dismissed entry,
//     AC-03) LLMAdvisor established.
//   - LLMAdvisor (llmadvisor_dormant_test.go) is now a DORMANT TEST
//     DOUBLE — see that file's own doc comment for the full disposition.
//     It has zero production call sites; core/rpc/api.go's chatAdvisor is
//     a *HeuristicAdvisor today. Kept solely for its AC-02-style
//     fault-injection coverage (timeout, malformed response,
//     out-of-range confidence) until a future SidecarAdvisor's fake
//     covers the same matrix (kenaz-ml integration design §9 Phase 0/1).
//   - ResolveAdvisorModel (model_resolve.go) is the laya ladder: explicit
//     setting -> local laya -> fleet laya (compiled, unreachable behind
//     fleetRungEnabled=false pending OQ-2) -> none. No "any available
//     model" rung exists here — deliberately, so the advisor never
//     silently repurposes the user's big chat model for a judgment call
//     the way the pre-01PMRA01 risk rater used to (see
//     model_resolve_test.go's profiles[0]-regression case). This ladder
//     is NOT consulted by HeuristicAdvisor (rung R1 predates any model
//     resolution) — it remains live for a future laya/LLM backend
//     (Phase B/C) and for the boot-time resolve-and-log call core/rpc/
//     api.go's newLLMStack still makes unconditionally.
package advice
