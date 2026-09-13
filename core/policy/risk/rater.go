// Package risk defines the RiskRater contract risk-rated-autonomy-01PMRA01
// layers on top of Cedar's forbid/permit decisions. When Cedar has no
// opinion about a tool dispatch (Outcome NotApplicable), layer 3
// (cedar.ThreeLayerResolve, core/policy/cedar/risk_layer.go) needs a
// score to compare against the tier's threshold; a RiskRater produces
// that score.
//
// WP04 defines the contract, a race-safe fake, and the band anchors.
// WP05 wires a real LLM implementation behind the same interface. Until
// then nothing calls RiskRater in production — ThreeLayerResolve's
// layer-3 branch is still the WP02 stub that always answers Confirm
// (see risk_layer.go's doc comment).
package risk

import "context"

// Rating is a RiskRater's structured judgment about one tool dispatch.
type Rating struct {
	// Score is 0-100 (spec FR-002's anchored bands, see Band/BandFor).
	// A RiskRater implementation MUST reject an out-of-range score with
	// an error rather than clamping it (tasks.md WP04) — clamping would
	// silently hide either a rater bug or a successful prompt-injection
	// attempt (spec's security section) behind a plausible-looking
	// number. See ValidateScore.
	Score int
	// Rationale is the rater's short, human-readable justification.
	// Treat as untrusted-but-informational: it is model output, and on
	// a session that has read untrusted content it can be
	// attacker-influenced same as the rating itself.
	Rationale string
	// Model is the model id that produced this rating (e.g.
	// "claude-haiku-4-5"), recorded for FR-007 reproducibility.
	Model string
	// PromptVersion identifies which rater system-prompt fixture
	// produced this rating (FR-002, FR-007). Ratings produced under
	// different prompt versions must never be compared or cached
	// together — WP05's cache key includes this field for exactly that
	// reason.
	PromptVersion string
}

// SessionContext is the narrow, additive session-scoped context a
// RiskRater may use to inform a rating. Kept minimal so WP05's LLM
// implementation can extend consumption without breaking the contract
// or FakeRater.
type SessionContext struct {
	// SessionID identifies the session. Used for cache-scoping (WP05:
	// ratings are never cached across sessions, so a prompt-injection
	// payload cannot poison a later, cleaner session's rating) and
	// audit correlation. Not a secret; safe to log.
	SessionID string
}

// RiskRater is the layer-3 seam WP05 wires a real LLM call behind. Rate
// takes the (tool name, normalized args, session context) that were the
// subject of a Cedar NotApplicable decision and returns a Rating.
//
// # Security: the rated input is attacker-influenced
//
// normalizedArgs is model-controlled, and on any session that has read
// untrusted content (a fetched web page, an email, a repo file, an MCP
// tool result) it is attacker-influenced. A production implementation
// MUST pass tool name and args to the rating model as clearly
// delimited, escaped DATA — never as instructions — per the mission
// spec's security section. WP06 carries the concrete mitigation (a
// family floor the rater cannot lower) and its injection-fixture tests;
// this package only defines the contract new implementations must
// honour.
//
// # Failure contract
//
// Every failure mode — timeout, unparseable response, out-of-range
// score, the rater's own model call refused or rate-limited — MUST
// surface as a non-nil error, never as a best-guess Rating. Once WP05
// wires a RiskRater into cedar.ThreeLayerResolve, any Rate error there
// maps to Confirm (ask a human), never to Allow (spec FR-004: "fail
// closed to the prompt, never fail-open").
type RiskRater interface {
	Rate(ctx context.Context, tool string, normalizedArgs string, sessCtx SessionContext) (Rating, error)
}

// compile-time witness that *FakeRater satisfies RiskRater.
var _ RiskRater = (*FakeRater)(nil)
