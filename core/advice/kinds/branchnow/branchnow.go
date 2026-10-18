// Package branchnow registers the "branch_now" AdviceKind
// (laya-advisors-01LAYA001 WP04, tasks.md's DESIGN-LOCKED REVISION):
// "is the current exchange a separable thread from the session's main
// line?"
//
// # Adapt, not rebuild (tasks.md WP04: "one detection implementation
// must remain")
//
// The shipped heuristic branch advisor — core/branchadvisor, a compiled
// regex table with a kill-switch (HARNESS_BRANCH_ADVISOR) and its own
// audit loop (branch_advisor.{suggested,accepted,dismissed,reintegrated})
// — is ADAPTED here, not reimplemented. Extract calls
// branchadvisor.ComputeSignals (the exact scan loop branchadvisor.Detect
// itself now calls, per that package's own 2026-09-29 refactor) to
// derive HeuristicSignalCount/HeuristicNoiseCount, and Heuristic below
// reuses branchadvisor.Signals.Confidence()'s IDENTICAL formula
// (signal_count / (signal_count + noise_count)) to derive its own
// confidence. There is exactly one regex table and exactly one
// confidence formula in the tree; this package is a second CALLER of
// both, never a second implementation.
//
// # Why this is a feature extractor now, not a second live detector
//
// research/kenaz-ml-integration-design.md §4's harness-kinds table
// literally says: "the shipped core/branchadvisor detector demoted to
// *feature extractor*." branch_now's Features vector carries
// HeuristicSignalCount/HeuristicNoiseCount as two of six fields
// (turns_since_session_start, turns_since_last_branch,
// prior_branch_count, edit_resend_precursor, heuristic_signal_count,
// heuristic_noise_count per spec.md §2's table) precisely so a FUTURE
// GBDT (design §5.4's R2/R3 graduation) can combine the regex signal
// with structural session features — the rung-R1 HeuristicAdvisor
// backend below only consumes the two heuristic_* fields (see
// Heuristic's own doc comment for why), but the other four ride along in
// every captured label row from day one (WP08's label-capture bridge),
// which is the whole point of capturing them now rather than retrofitting
// later.
//
// # Consumer action
//
// A "yes" recommendation is meant to offer/execute an event-log branch
// at the divergence point — the existing machinery from
// event-log-01KQ1A3M. Nothing in this package calls that machinery: per
// tasks.md item 6, WP04-06 register kinds and a working backend but wire
// NOTHING into a live chat turn or render a chip — that is WP07's job.
// SessionSnapshot below is this kind's own "session-state snapshot" type
// (FeatureExtractor's contract: "input is whatever session-state
// snapshot the KIND's OWN package defines and passes in") — a future
// WP07 caller populates it from real event-log/session state and drives
// Recommend; this WP's tests populate it directly.
package branchnow

import (
	"fmt"
	"math"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/branchadvisor"
)

// KindID is branch_now's stable registry id (spec.md §2's table).
const KindID = "branch_now"

// PromptVersion identifies this kind's prompt-text/feature-contract
// revision. Bump on a semantic change to Extract or RenderPrompt, never
// on a typo fix (kind.go's PromptVersion doc comment).
const PromptVersion = "v1"

// ModelID is the honest, non-model model id HeuristicAdvisor stamps onto
// every branch_now Recommendation (spec: "Model='heuristic/<name>-v1'").
// Named for what it actually does — reuses core/branchadvisor's compiled
// regex table — not for an aspirational future backend.
const ModelID = "heuristic/branch-regex-v1"

// decisionRatioFloor is the minimum signal/(signal+noise) ratio Heuristic
// requires before answering "yes, branch here" (Decision=true). Set
// above 0.5 so a message with as much noise-signal (a "actually",
// "instead", "nevermind" style qualifier) as branch-signal does not
// fire — mirrors the intuition branchadvisor.DetectWithDefaults' default
// 0.85 minConfidence encodes for the legacy chip, though deliberately
// looser here: minConfidence-gated UI display is a SEPARATE concern
// (spec §2d's flat ≥75 shown-gate is WP07's job) from this kind's own
// binary Decision, which must fire whenever the evidence honestly favors
// "yes" so a below-threshold recommendation can still be captured as a
// label (spec §2d).
const decisionRatioFloor = 0.5

// Snapshot is branch_now's own session-state input type — the concrete
// shape Extract type-asserts `input any` to, per FeatureExtractor's
// contract that core/advice stays ignorant of it (AC-08). A future WP07
// caller populates this from real session/event-log state immediately
// before a live Recommend call; nothing in this package or WP04
// constructs one outside tests.
type Snapshot struct {
	// LastUserMessage is the most recent user turn's raw text — the ONLY
	// field ComputeSignals reads. Never itself copied into Features
	// (spec §2: "never raw transcript by default" — only the derived
	// HeuristicSignalCount/HeuristicNoiseCount and Labels-less counts
	// leave this function).
	LastUserMessage string
	// TurnsSinceSessionStart is how many turns have elapsed since the
	// session began.
	TurnsSinceSessionStart int
	// TurnsSinceLastBranch is how many turns have elapsed since the last
	// branch was created from this session (event-log-01KQ1A3M's
	// branch.created audit stream is the real source once WP07 wires a
	// caller). A session with no prior branch reports its full turn
	// count here.
	TurnsSinceLastBranch int
	// PriorBranchCount is how many branches already exist off this
	// session.
	PriorBranchCount int
	// EditResendPrecursor reports whether the user edited-and-resent
	// their previous message immediately before this one — one of
	// design §4's "Unprompted positives" precursor signals for a
	// separable thread.
	EditResendPrecursor bool
	// ToolCallDensityWindow is tool calls per turn over a recent
	// trailing window (design §4's harness-kinds table).
	ToolCallDensityWindow float64
}

// Features is branch_now's model/label-facing feature vector — spec.md
// §2's table plus design §4's heuristic_signal_count/heuristic_noise_count
// substitution for "the shipped core/branchadvisor detector demoted to
// *feature extractor*." Field order is declaration order, which
// advice.FeaturesHash's canonical-JSON hashing depends on for a stable
// cache key across materially-identical inputs.
type Features struct {
	TurnsSinceSessionStart int     `json:"turns_since_session_start"`
	TurnsSinceLastBranch   int     `json:"turns_since_last_branch"`
	PriorBranchCount       int     `json:"prior_branch_count"`
	EditResendPrecursor    bool    `json:"edit_resend_precursor"`
	HeuristicSignalCount   int     `json:"heuristic_signal_count"`
	HeuristicNoiseCount    int     `json:"heuristic_noise_count"`
	LastUserMsgLen         int     `json:"last_user_msg_len"`
	ToolCallDensityWindow  float64 `json:"tool_call_density_window"`
}

// Extract implements advice.FeatureExtractor. Type-asserts input to
// Snapshot (never panics on a mismatch — a wrong type degrades to an
// error, exactly like every other Advisor failure mode) and derives the
// two heuristic_* fields via branchadvisor.ComputeSignals — the single
// shared implementation, per this package's doc comment.
func Extract(input any) (advice.Features, error) {
	snap, ok := input.(Snapshot)
	if !ok {
		return nil, fmt.Errorf("branchnow: want Snapshot input, got %T", input)
	}
	sig := branchadvisor.ComputeSignals(snap.LastUserMessage)
	return Features{
		TurnsSinceSessionStart: snap.TurnsSinceSessionStart,
		TurnsSinceLastBranch:   snap.TurnsSinceLastBranch,
		PriorBranchCount:       snap.PriorBranchCount,
		EditResendPrecursor:    snap.EditResendPrecursor,
		HeuristicSignalCount:   sig.SignalCount,
		HeuristicNoiseCount:    sig.NoiseCount,
		LastUserMsgLen:         len(snap.LastUserMessage),
		ToolCallDensityWindow:  snap.ToolCallDensityWindow,
	}, nil
}

// RenderPrompt implements advice.PromptRenderer. AdviceKind requires a
// non-nil renderer (kind.go's validate()) so the registry entry is ready
// for a Phase-B/C laya or LLM backend (design §5.4's R2 "shadow-ML" /
// R3), but rung-R1's HeuristicAdvisor (heuristic.go) never calls it —
// Heuristic below computes Decision/Confidence directly from Features.
// This renders a plain, delimited-data description (never raw user
// text — spec's non-goal "prompt-injection-hardening beyond the rater's
// existing pattern" reuses the same discipline: features are data, never
// instructions).
func RenderPrompt(features advice.Features) (system, user string) {
	f, ok := features.(Features)
	if !ok {
		return "", ""
	}
	system = "You judge whether the current exchange is a separable thread from the session's main line. " +
		"Respond with a strict JSON object: {\"decision\": bool, \"confidence\": 0-100}."
	user = fmt.Sprintf(
		"turns_since_session_start=%d turns_since_last_branch=%d prior_branch_count=%d "+
			"edit_resend_precursor=%t heuristic_signal_count=%d heuristic_noise_count=%d "+
			"last_user_msg_len=%d tool_call_density_window=%.3f",
		f.TurnsSinceSessionStart, f.TurnsSinceLastBranch, f.PriorBranchCount,
		f.EditResendPrecursor, f.HeuristicSignalCount, f.HeuristicNoiseCount,
		f.LastUserMsgLen, f.ToolCallDensityWindow,
	)
	return system, user
}

// Heuristic implements advice.HeuristicFunc — branch_now's rung-R1
// backend (design §4: "Day-1 backend: Shipped regex branch advisor").
//
// # Confidence mapping (documented per spec's "principled mapping"
// requirement)
//
// confidence = round(100 * signal_count / (signal_count + noise_count)),
// via branchadvisor.Signals.Confidence() — the IDENTICAL formula
// branchadvisor.Detect itself uses to decide whether to fire, reapplied
// here rather than re-derived so the two callers can never silently
// diverge. 0 when neither table matched anything (no evidence either
// way). Decision is true only when there is at least one positive
// signal AND the ratio clears decisionRatioFloor (0.5) — positive
// evidence must outweigh negative evidence, not merely exist.
//
// Deliberately NOT used in the decision (captured as Features instead,
// for a future GBDT per design §4/§5.4): turns_since_session_start,
// turns_since_last_branch, prior_branch_count, edit_resend_precursor,
// last_user_msg_len, tool_call_density_window. The day-1 rule is exactly
// the shipped regex detector's own logic — nothing more — matching
// design §4's "Day-1 backend" column precisely; widening the rule to
// weigh the other fields is exactly the kind of "hybrid" work design §4
// reserves for a later graduated model, not this heuristic.
//
// Honors branchadvisor's own kill-switch (HARNESS_BRANCH_ADVISOR):
// when disengaged, Heuristic answers "no branch, zero confidence" — an
// honest negative answer, not a Recommend-level failure, so the caller's
// cache and label-capture paths behave identically to any other
// low-evidence call rather than degrading to ErrNoAdvice for a reason
// this function alone knows about.
func Heuristic(features advice.Features) (decision bool, confidence int, modelID string, err error) {
	f, ok := features.(Features)
	if !ok {
		return false, 0, "", fmt.Errorf("branchnow: want Features, got %T", features)
	}
	if !branchadvisor.Enabled() {
		return false, 0, ModelID, nil
	}
	sig := branchadvisor.Signals{SignalCount: f.HeuristicSignalCount, NoiseCount: f.HeuristicNoiseCount}
	ratio := sig.Confidence()
	confidence = int(math.Round(ratio * 100))
	decision = f.HeuristicSignalCount > 0 && ratio > decisionRatioFloor
	return decision, confidence, ModelID, nil
}

func init() {
	// ID is the literal "branch_now" (not the KindID identifier) because
	// scripts/ci/cmd/checkadvicekinds statically greps this composite
	// literal for `ID: "<value>"` — a non-literal ID field parses as
	// "<unresolved>" and the gate cannot verify completeness or the
	// suggest-only/auto-act cross-check for this kind. TestRegistration
	// (branchnow_test.go) pins that this literal and KindID never drift.
	advice.MustRegister(advice.AdviceKind{
		ID:            "branch_now",
		PromptVersion: PromptVersion,
		SafetyClass:   advice.SafetyReversible,
		Extract:       Extract,
		RenderPrompt:  RenderPrompt,
	})
}
