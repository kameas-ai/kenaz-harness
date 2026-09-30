// Package advice defines the advisor seam mission laya-advisors-01LAYA001
// generalizes from risk-rated-autonomy-01PMRA01's RiskRater
// (core/policy/risk). Where a RiskRater answers "how risky is this tool
// call" to gate a dispatch, an Advisor answers a cheap, low-stakes,
// BINARY judgment call the harness makes many times a session — "is this
// exchange a separable thread", "will compacting now help", "is the
// model struggling" — and NEVER blocks anything: every failure mode
// degrades to "no advice" (see ErrNoAdvice).
//
// # What ships in this WP (WP01-03)
//
// The seam and the laya model-resolution ladder land here with ZERO
// advice kinds registered in production — mirroring how
// core/policy/risk's WP04 shipped the RiskRater contract and a
// race-safe fake before WP05 wired a real LLM implementation, and
// nothing called RiskRater in production until then. The three v1 kinds
// (branch_now, compact_now, escalate_model — spec.md §2's table) are
// WP04-06, dispatched as parallel worktree agents once this seam merges.
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
//   - LLMAdvisor (llmadvisor.go) was built as the production
//     implementation, but owner ruling 2026-09-29 (spec §2e-0) voids
//     LLM-backed advisors as a shipping path — advisors use laya and
//     other non-LLM models only. Its fate (delete vs. dormant reference
//     double) is decided by the in-flight kenaz-ml integration design;
//     it has zero production call sites today. As built it carries: an
//     800ms hard timeout (spec §2's deviation from the rater's 5s — advice
//     worthless late), a ctx derived from context.Background() (the
//     v0.78.2 ShutdownServedCore lesson risk/llmrater.go's Rate doc
//     comment explains in full), a session-scoped LRU cache keyed
//     (session, kind, featuresHash, promptVersion), and two independent
//     skip paths that make zero LLM calls: SessionContext.Moot (the
//     resolved autonomy tier makes the kind's action unreachable) and a
//     dismissed cache entry (AC-03: a dismissed recommendation is never
//     re-shown for materially identical features within the session).
//   - ResolveAdvisorModel (model_resolve.go) is the laya ladder: explicit
//     setting -> local laya -> fleet laya (compiled, unreachable behind
//     fleetRungEnabled=false pending OQ-2) -> none. No "any available
//     model" rung exists here — deliberately, so the advisor never
//     silently repurposes the user's big chat model for a judgment call
//     the way the pre-01PMRA01 risk rater used to (see
//     model_resolve_test.go's profiles[0]-regression case).
package advice
