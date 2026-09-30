package advice

import (
	"context"
	"errors"
)

// SessionContext is the narrow, additive session-scoped context an
// Advisor may use to inform a recommendation and to decide whether to
// skip a model call entirely. Kept minimal so a future implementation
// can extend consumption without breaking the contract or FakeAdvisor —
// mirrors risk.SessionContext's own doc comment exactly.
type SessionContext struct {
	// SessionID identifies the session. Part of the cache key (never
	// hashed into it — see cache.go's cacheKey doc comment for why a
	// real struct field, not a hash component, makes cross-session
	// isolation structural rather than probabilistic).
	SessionID string
	// Moot, when true, tells Recommend the answer cannot change
	// anything for this dispatch — e.g. the caller has already resolved
	// an autonomy tier under which this kind's action is unreachable.
	// Recommend MUST treat Moot as a hard skip: zero LLM calls, zero
	// cache reads or writes, an immediate ErrNoAdvice-wrapped return.
	// This is one of the two skip paths spec §2 requires ("skip when
	// the answer cannot change anything ... autonomy tier makes the
	// kind moot"); the other is a dismissed cache entry (AC-03).
	Moot bool
}

// Recommendation is an Advisor's structured judgment for one
// AdviceKind + Features pair.
type Recommendation struct {
	// Decision is the strict binary answer to the kind's question (e.g.
	// branch_now: "yes, branch here").
	Decision bool
	// Confidence is 0-100. An Advisor implementation MUST reject an
	// out-of-range confidence with an error rather than clamp it — see
	// ParseRecommendation's doc comment; clamping would silently hide a
	// model bug behind a plausible-looking number, the same reasoning
	// risk.Rating.Score's doc comment gives for risk scores.
	Confidence int
	// KindID identifies which AdviceKind produced this recommendation.
	KindID string
	// PromptVersion identifies which prompt-text revision produced this
	// recommendation (mirrors risk.Rating.PromptVersion).
	PromptVersion string
	// Model is the model id that produced this recommendation.
	Model string
	// Rung is which rung of ResolveAdvisorModel's ladder resolved Model
	// (mirrors risk.RaterModelRung's "log which rung resolved" contract,
	// stamped onto the recommendation itself so a UI or audit trail can
	// show it without re-deriving it). RungHeuristic is the one
	// exception: a HeuristicAdvisor backend (heuristic.go) stamps it
	// directly without ever calling ResolveAdvisorModel — see
	// RungHeuristic's own doc comment for why a rule-based rung sits
	// outside the model-resolution ladder entirely.
	Rung ModelRung
	// Unbenchmarked reports whether Model has no measured row in a
	// benchmark of record (OQ-3, not yet landed — always true today; the
	// rater review's follow-up asked that this field exist "from the
	// start" rather than being bolted on after the fact once a benchmark
	// exists).
	Unbenchmarked bool
	// CacheHit is true when this Recommendation was served from the
	// session-scoped cache rather than a fresh model call. Set by the
	// Advisor implementation on return, never by the cache itself —
	// mirrors risk.Rating.CacheHit's exact contract, including that a
	// cache hit is served on a COPY so the cache's own stored entry is
	// never mutated by a later reader.
	CacheHit bool
}

// ErrNoAdvice is the ONE failure signal Recommend ever returns (wrapped
// with more detail via fmt.Errorf's %w where useful for diagnostics).
// Spec §2: "Advice is optional by construction. Every failure mode — no
// model available, timeout, parse failure, consent absent — degrades to
// 'no advice'. An advisor outage must be invisible except in its own
// diagnostics. No turn ever blocks on an advisor." Unlike
// risk.RiskRater's failure contract (which distinguishes ErrUnreachable
// from a transient/malformed-response failure because ITS caller must
// choose between Confirm and the offline floor), an Advisor caller never
// needs to distinguish sub-causes: every error means "render nothing."
// errors.Is(err, ErrNoAdvice) is true for every error Recommend returns.
var ErrNoAdvice = errors.New("advice: no recommendation available")

// Advisor is the seam RiskRater generalizes into a multi-kind registry.
// Recommend takes a kind and its ALREADY-EXTRACTED features (the caller
// calls kind.Extract itself — this package never touches the concrete
// session-state type an extractor consumes, per AC-08) and returns a
// Recommendation or an error wrapping ErrNoAdvice. Dismiss records that
// the caller has shown and the user has dismissed a recommendation for
// (kind, features) in this session, so a later Recommend for materially
// identical features skips the model call entirely (AC-03).
type Advisor interface {
	Recommend(ctx context.Context, kind AdviceKind, features Features, sess SessionContext) (Recommendation, error)
	// Dismiss takes the full AdviceKind (not just its id) so the
	// dismissal key can include kind.PromptVersion directly, exactly as
	// Recommend's own cache key does — looking PromptVersion up again via
	// the package registry would silently miss for a kind constructed
	// in a test without ever calling Register (see cache.go's cacheKey:
	// promptVersion is part of the key, so a Dismiss that resolves it
	// differently than Recommend did creates a tombstone under the WRONG
	// key and the dismissal silently fails to suppress anything).
	Dismiss(sess SessionContext, kind AdviceKind, features Features)
}
