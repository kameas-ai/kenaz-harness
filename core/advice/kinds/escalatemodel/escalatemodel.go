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
// revision.
const PromptVersion = "v1"

// ModelID is the honest, non-model model id HeuristicAdvisor stamps onto
// every escalate_model Recommendation.
const ModelID = "heuristic/escalate-failure-streak-v1"

// Snapshot is escalate_model's own session-state input type. A future
// WP07 caller populates this from the chat runner's live turn/move
// journal and tool-dispatch error tallies immediately before a Recommend
// call; this package's tests populate it directly.
type Snapshot struct {
	// ConsecutiveToolFailures is how many tool calls in a row have failed
	// or been malformed in the current turn window.
	ConsecutiveToolFailures int
	// RetriesInWindow is how many retry/correction attempts have fired in
	// the trailing window (design §4's "retry/correction loop counts").
	RetriesInWindow int
	// DoomLoopRepeatCount is the current run's doom-loop guard repeat
	// count for the most-recently-touched (tool, args) key — the same
	// value core/agentgraph's tool-dispatch executor tracks via its
	// bounded toolCallHistory (doomloop.go). Zero when no repeat has been
	// observed yet.
	DoomLoopRepeatCount int
	// DoomLoopThreshold is the configured doom-loop trip threshold for
	// this run (Budget.DoomLoopThreshold). Zero means
	// agentgraph.DefaultDoomLoopThreshold applies — mirroring
	// core/agentgraph/exec_dispatch.go's own "zero means default" reader.
	DoomLoopThreshold int
	// CurrentRung names which model-resolution rung is currently serving
	// the session (design §4's table). Captured for the feature vector;
	// not used by the rung-R1 decision (see Heuristic's doc comment).
	CurrentRung string
	// ErrorKindCounts tallies observed error categories in the trailing
	// window (design §4's "error_kind one-hots" — represented here as
	// counts rather than a fixed one-hot vector so a new error category
	// needs no contract change; a future GBDT feature-engineering pass
	// can one-hot-encode this map's keys against a frozen vocabulary).
	ErrorKindCounts map[string]int
	// BudgetRemainingFraction is the fraction (0-1) of the run's
	// tool-call/token budget still remaining.
	BudgetRemainingFraction float64
}

// Features is escalate_model's model/label-facing feature vector —
// design §4's harness-kinds table. Field order is declaration order (see
// branchnow.Features' identical note on advice.FeaturesHash). Go's
// encoding/json sorts map keys, so ErrorKindCounts hashes deterministically
// despite being a map rather than a struct.
type Features struct {
	ConsecutiveToolFailures int            `json:"consecutive_tool_failures"`
	RetriesInWindow         int            `json:"retries_in_window"`
	DoomLoopRepeatCount     int            `json:"doom_loop_repeat_count"`
	DoomLoopThreshold       int            `json:"doom_loop_threshold"`
	CurrentRung             string         `json:"current_rung"`
	ErrorKindCounts         map[string]int `json:"error_kind_counts,omitempty"`
	BudgetRemainingFraction float64        `json:"budget_remaining_fraction"`
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
	var errCounts map[string]int
	if len(snap.ErrorKindCounts) > 0 {
		errCounts = make(map[string]int, len(snap.ErrorKindCounts))
		for k, v := range snap.ErrorKindCounts {
			errCounts[k] = v
		}
	}
	return Features{
		ConsecutiveToolFailures: snap.ConsecutiveToolFailures,
		RetriesInWindow:         snap.RetriesInWindow,
		DoomLoopRepeatCount:     snap.DoomLoopRepeatCount,
		DoomLoopThreshold:       threshold,
		CurrentRung:             snap.CurrentRung,
		ErrorKindCounts:         errCounts,
		BudgetRemainingFraction: snap.BudgetRemainingFraction,
	}, nil
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
		"consecutive_tool_failures=%d retries_in_window=%d doom_loop_repeat_count=%d "+
			"doom_loop_threshold=%d current_rung=%q error_kind_counts=%v budget_remaining_fraction=%.3f",
		f.ConsecutiveToolFailures, f.RetriesInWindow, f.DoomLoopRepeatCount,
		f.DoomLoopThreshold, f.CurrentRung, f.ErrorKindCounts, f.BudgetRemainingFraction,
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
// future GBDT per design §4/§5.4): current_rung, error_kind_counts,
// budget_remaining_fraction.
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
