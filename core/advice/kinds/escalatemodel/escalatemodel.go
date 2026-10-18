// Package escalatemodel registers the "escalate_model" AdviceKind
// (laya-advisors-01LAYA001 WP06, tasks.md's DESIGN-LOCKED REVISION):
// "is the current model visibly struggling with this task?"
//
// # Real signal sources
//
// DoomLoopRepeatCount and DoomLoopThreshold mirror
// core/agentgraph.DefaultDoomLoopThreshold and the per-call repeat count
// core/agentgraph's tool-dispatch doom-loop guard (doomloop.go) already
// tracks per run — the same "N failed/malformed moves in window" signal
// design §4's table names as this kind's Day-1 backend
// ("Failure-streak rule (N failed/malformed moves in window)"). This
// package reuses agentgraph.DefaultDoomLoopThreshold directly as its own
// failure-streak threshold (see Heuristic's doc comment) rather than
// inventing a second "how many repeats is trouble" magic number.
// ConsecutiveToolFailures, RetriesInWindow, ErrorKindCounts and
// BudgetRemainingFraction are the remaining fields design §4's table
// names for this kind; a future WP07 caller derives them from the chat
// runner's own move/turn journal (model-moves-transcript-01PMCH01) and
// tool-dispatch error tallies.
//
// # Consumer action
//
// A "yes" recommendation is meant to offer switching the session to a
// stronger profile via the existing model-moves machinery
// (model-moves-transcript-01PMCH01). Nothing in this package calls that
// machinery — per tasks.md item 6, WP04-06 register kinds and a working
// backend without wiring a live call site or rendering a chip; that is
// WP07's job. Snapshot below is populated by a future WP07 caller from
// real chat-runner state; this WP's tests populate it directly.
package escalatemodel

import (
	"fmt"
	"math"

	"github.com/kameas-ai/kenaz-harness/core/advice"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// KindID is escalate_model's stable registry id (spec.md §2's table).
const KindID = "escalate_model"

// PromptVersion identifies this kind's prompt-text/feature-contract
// revision. v2 (2026-09-30 interop ruling): the feature vector was
// rewritten to design §4's catalog — the kenaz-ml engine's escalate_model
// contract (advice/contracts.py ESCALATE_MODEL_FEATURES) — replacing
// WP06's improvised v1 shape (doom_loop_*, a string current_rung, a nested
// error_kind_counts map), which the engine refused whole-batch (409
// names_mismatch). Bumping the version keeps every v1 label row
// distinguishable in the corpus (the capture bridge stamps it on each
// row); v1 rows stay as they are.
const PromptVersion = "v2"

// ModelID is the honest, non-model model id HeuristicAdvisor stamps onto
// every escalate_model Recommendation.
const ModelID = "heuristic/escalate-failure-streak-v1"

// ErrorKinds is design §4's error_kind one-hot vocabulary, IN ORDER — the
// harness's closed ErrorCategory enum (core/fleet/usage_emitter.go),
// which kenaz-ml's advice/contracts.py ERROR_KINDS mirrors as a cross-repo
// constant. A change here requires a matching engine change and a
// VOCABULARY_VERSION bump there. TestErrorKinds_MatchFleetCategories pins
// it to core/fleet.
var ErrorKinds = [...]string{"auth", "transient", "cancelled", "budget", "unknown"}

// Snapshot is escalate_model's own session-state input type. A future
// WP07 caller populates this from the chat runner's live turn/move
// journal and tool-dispatch error tallies immediately before a Recommend
// call; this package's tests populate it directly. It carries both the
// catalog signals (exported as Features) and the heuristic's own
// doom-loop inputs (kept off the exported vector — see Features).
type Snapshot struct {
	// ConsecutiveToolFailures is how many tool calls in a row have failed
	// or been malformed in the current turn window.
	ConsecutiveToolFailures int
	// RetriesInWindow is how many retry/correction attempts have fired in
	// the trailing window (design §4's "retry/correction loop counts").
	RetriesInWindow int
	// TurnLatencyTrend is design §4's turn_latency_trend: the slope of
	// recent turn latencies (positive = turns getting slower).
	// DATED (2026-09-30, owner: the WP07 follow-up that wires the
	// ChatRunner accessor — see advice_hook.go's escalate_model justify):
	// no source is plumbed; callers leave it zero and set
	// FeaturesIncomplete.
	TurnLatencyTrend float64
	// CurrentRung is the ORDINAL position of the model currently serving
	// the session on its escalation ladder (design §4's current_rung; the
	// engine's contract is numeric): 0 = unknown/unset, higher = stronger
	// model. Captured for the feature vector; not used by the rung-R1
	// decision (see Heuristic's doc comment).
	CurrentRung int
	// ErrorKindCounts tallies observed error categories in the trailing
	// window, keyed by ErrorKinds names; any other key buckets into
	// "unknown" (mirroring core/fleet.ProjectErrorCategory). Extract
	// projects it onto the error_kind_* one-hot (see Features).
	ErrorKindCounts map[string]int
	// BudgetRemainingFraction is the fraction (0-1) of the run's
	// tool-call/token budget still remaining.
	BudgetRemainingFraction float64
	// DoomLoopRepeatCount is the current run's doom-loop guard repeat
	// count for the most-recently-touched (tool, args) key — the same
	// value core/agentgraph's tool-dispatch executor tracks via its
	// bounded toolCallHistory (doomloop.go). A heuristic input only.
	DoomLoopRepeatCount int
	// DoomLoopThreshold is the configured doom-loop trip threshold for
	// this run (Budget.DoomLoopThreshold). Zero means
	// agentgraph.DefaultDoomLoopThreshold applies — mirroring
	// core/agentgraph/exec_dispatch.go's own "zero means default" reader.
	// A heuristic input only.
	DoomLoopThreshold int
	// FeaturesIncomplete, when true, tells Extract that the caller built
	// this Snapshot from placeholder/zero-value data because no
	// ChatRunner-visible tool-failure/doom-loop accessor is wired yet
	// (see core/rpc/views/agentgraph/chat/advice_hook.go's fireAdvice —
	// its own comment names the exact blocker). See
	// advice.FeaturesCompleteness's doc comment and compactnow.Snapshot's
	// identical field for the full reasoning: the kind package is the
	// "justify site" that knows whether its own Snapshot is real, not the
	// label-capture bridge.
	FeaturesIncomplete bool
}

// Features is escalate_model's model/label-facing feature vector —
// EXACTLY design §4's catalog, in the engine contract's order
// (kenaz-ml advice/contracts.py ESCALATE_MODEL_FEATURES):
// consecutive_tool_failures, retries_in_window, turn_latency_trend,
// current_rung, error_kind_{auth,transient,cancelled,budget,unknown},
// budget_remaining_fraction. Every exported field is numeric (the engine
// declares all of them float64). TestFeatures_MatchTheEngineContract pins
// the JSON names and order.
//
// The error_kind_* fields are a ONE-HOT of the window's dominant error
// category (the highest count; ties go to the earlier ErrorKinds entry);
// all zero when no error was observed.
//
// DoomLoopRepeatCount / DoomLoopThreshold / FeaturesIncomplete are
// `json:"-"`: the heuristic's own inputs and the completeness flag, never
// on the wire and never in advice.FeaturesHash. Consequence, stated: two
// states differing ONLY in doom-loop proximity share an advice-cache key.
// That is inert today (the only production caller sends zero-valued
// placeholder Snapshots — advice_hook.go's dated justify) and must be
// revisited by whoever wires a real doom-loop source, either by adding it
// to the catalog (an engine contract + VOCABULARY_VERSION change) or by
// accepting the cache coarseness.
type Features struct {
	ConsecutiveToolFailures int     `json:"consecutive_tool_failures"`
	RetriesInWindow         int     `json:"retries_in_window"`
	TurnLatencyTrend        float64 `json:"turn_latency_trend"`
	CurrentRung             int     `json:"current_rung"`
	ErrorKindAuth           int     `json:"error_kind_auth"`
	ErrorKindTransient      int     `json:"error_kind_transient"`
	ErrorKindCancelled      int     `json:"error_kind_cancelled"`
	ErrorKindBudget         int     `json:"error_kind_budget"`
	ErrorKindUnknown        int     `json:"error_kind_unknown"`
	BudgetRemainingFraction float64 `json:"budget_remaining_fraction"`

	DoomLoopRepeatCount int  `json:"-"`
	DoomLoopThreshold   int  `json:"-"`
	FeaturesIncomplete  bool `json:"-"`
}

// FeaturesComplete implements advice.FeaturesCompleteness.
func (f Features) FeaturesComplete() bool { return !f.FeaturesIncomplete }

// dominantErrorKind projects counts onto ErrorKinds (unknown keys bucket
// into "unknown") and returns the index of the highest total, or -1 when
// no error was counted.
func dominantErrorKind(counts map[string]int) int {
	totals := make([]int, len(ErrorKinds))
	for k, v := range counts {
		idx := len(ErrorKinds) - 1 // "unknown"
		for i, name := range ErrorKinds {
			if k == name {
				idx = i
				break
			}
		}
		if v > 0 {
			totals[idx] += v
		}
	}
	best := -1
	for i, n := range totals {
		if n > 0 && (best < 0 || n > totals[best]) {
			best = i
		}
	}
	return best
}

// Extract implements advice.FeatureExtractor.
func Extract(input any) (advice.Features, error) {
	snap, ok := input.(Snapshot)
	if !ok {
		return nil, fmt.Errorf("escalatemodel: want Snapshot input, got %T", input)
	}
	threshold := snap.DoomLoopThreshold
	if threshold <= 0 {
		threshold = agentgraph.DefaultDoomLoopThreshold
	}
	f := Features{
		ConsecutiveToolFailures: snap.ConsecutiveToolFailures,
		RetriesInWindow:         snap.RetriesInWindow,
		TurnLatencyTrend:        snap.TurnLatencyTrend,
		CurrentRung:             snap.CurrentRung,
		BudgetRemainingFraction: snap.BudgetRemainingFraction,
		DoomLoopRepeatCount:     snap.DoomLoopRepeatCount,
		DoomLoopThreshold:       threshold,
		FeaturesIncomplete:      snap.FeaturesIncomplete,
	}
	switch dominantErrorKind(snap.ErrorKindCounts) {
	case 0:
		f.ErrorKindAuth = 1
	case 1:
		f.ErrorKindTransient = 1
	case 2:
		f.ErrorKindCancelled = 1
	case 3:
		f.ErrorKindBudget = 1
	case 4:
		f.ErrorKindUnknown = 1
	}
	return f, nil
}

// RenderPrompt implements advice.PromptRenderer. Required by
// AdviceKind.validate() (forward-compat scaffold for design §5.4's
// "Phase-C challenger at most" laya role — "only if shadow proves lift").
// Never called by rung-R1's HeuristicAdvisor.
func RenderPrompt(features advice.Features) (system, user string) {
	f, ok := features.(Features)
	if !ok {
		return "", ""
	}
	system = "You judge whether the current model is visibly struggling with this task. " +
		"Respond with a strict JSON object: {\"decision\": bool, \"confidence\": 0-100}."
	user = fmt.Sprintf(
		"consecutive_tool_failures=%d retries_in_window=%d turn_latency_trend=%.3f current_rung=%d "+
			"error_kind_auth=%d error_kind_transient=%d error_kind_cancelled=%d error_kind_budget=%d "+
			"error_kind_unknown=%d budget_remaining_fraction=%.3f",
		f.ConsecutiveToolFailures, f.RetriesInWindow, f.TurnLatencyTrend, f.CurrentRung,
		f.ErrorKindAuth, f.ErrorKindTransient, f.ErrorKindCancelled, f.ErrorKindBudget,
		f.ErrorKindUnknown, f.BudgetRemainingFraction,
	)
	return system, user
}

// Heuristic implements advice.HeuristicFunc — escalate_model's rung-R1
// backend (design §4: "Day-1 backend: Failure-streak rule (N failed/
// malformed moves in window)").
//
// # Confidence mapping (documented per spec's "principled mapping"
// requirement)
//
// streakRatio = min(1, consecutive_tool_failures / doom_loop_threshold);
// retryRatio = min(1, retries_in_window / doom_loop_threshold); proximity
// = min(1, doom_loop_repeat_count / doom_loop_threshold) — three
// independent "how close to trouble" ratios sharing the SAME
// threshold (agentgraph.DefaultDoomLoopThreshold when
// doom_loop_threshold is unset), reusing the doom-loop guard's own
// notion of "how many repeats is a problem" rather than a second,
// independently-tuned number. confidence = round(100 * max(streakRatio,
// retryRatio, proximity)) — the single worst-case signal drives
// confidence, since any ONE of the three maxing out (three failures in a
// row, three retries, or the doom-loop guard itself about to trip) is
// independently a legitimate "the model is struggling" signal; taking
// the max avoids a moderate reading on all three masking a severe
// reading on one. Decision is true when any ratio reaches 1.0 (the
// streak/retry/proximity count has reached the threshold).
//
// Deliberately NOT used in the decision (captured as Features for a
// future GBDT per design §4/§5.4): turn_latency_trend, current_rung, the
// error_kind_* one-hot, budget_remaining_fraction. The doom-loop inputs
// it does use ride on Features as json:"-" fields (see Features).
func Heuristic(features advice.Features) (decision bool, confidence int, modelID string, err error) {
	f, ok := features.(Features)
	if !ok {
		return false, 0, "", fmt.Errorf("escalatemodel: want Features, got %T", features)
	}
	threshold := f.DoomLoopThreshold
	if threshold <= 0 {
		threshold = agentgraph.DefaultDoomLoopThreshold
	}

	ratio := func(n int) float64 {
		r := float64(n) / float64(threshold)
		if r > 1 {
			r = 1
		}
		if r < 0 {
			r = 0
		}
		return r
	}

	streakRatio := ratio(f.ConsecutiveToolFailures)
	retryRatio := ratio(f.RetriesInWindow)
	proximity := ratio(f.DoomLoopRepeatCount)

	worst := math.Max(streakRatio, math.Max(retryRatio, proximity))
	confidence = int(math.Round(worst * 100))
	decision = worst >= 1.0
	return decision, confidence, ModelID, nil
}

func init() {
	// ID is the literal "escalate_model" (not the KindID identifier) —
	// see branchnow.go's init() comment for why: check-advice-kinds.sh's
	// scan requires a literal string right after `ID:`. TestRegistration
	// (escalatemodel_test.go) pins that this literal and KindID never
	// drift.
	advice.MustRegister(advice.AdviceKind{
		ID:            "escalate_model",
		PromptVersion: PromptVersion,
		SafetyClass:   advice.SafetySuggestOnly,
		Extract:       Extract,
		RenderPrompt:  RenderPrompt,
	})
}
