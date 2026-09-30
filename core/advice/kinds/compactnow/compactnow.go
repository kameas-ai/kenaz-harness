// Package compactnow registers the "compact_now" AdviceKind
// (laya-advisors-01LAYA001 WP05, tasks.md's DESIGN-LOCKED REVISION):
// "will compacting now preserve more task-relevant context than
// compacting at the forced threshold?"
//
// # Real signal sources
//
// Every field this kind's Features struct carries is named after a real,
// already-emitted quantity rather than invented from scratch:
// TokensInSpan and HistoricalCompressionRatio mirror
// core/context/audit.SessionCompactedPayload's own TokensInSpan /
// CompressionRatio fields (the compaction engine's success-path audit
// payload, core/agentgraph/compaction), and ModelContextLimit mirrors
// core/llm.ModelInfo's ContextWindow field (the provider capability
// catalog's context-length figure). ContextFillFraction,
// TurnsSinceLastCompaction and ToolResultTokenFraction are the ratios
// design §4's harness-kinds table names for this kind
// ("context_fill_fraction, tokens_in_span, turns_since_last_compaction,
// tool_result_token_fraction, model_context_limit,
// historical_compression_ratio (all present on SessionCompactedPayload)")
// — derived by a future WP07 caller from the SAME token-accounting the
// compaction engine (core/agentgraph/compaction) already performs, not a
// second accounting implementation.
//
// # Day-1 backend: fill-threshold rule
//
// Per design §4's table ("Day-1 backend: Fill-threshold rule... GBDT —
// terminal") this kind's own R1 heuristic is deliberately the simplest
// of the trio: context fill percentage crossing a threshold. See
// Heuristic's doc comment for the exact formula.
//
// # Consumer action
//
// A "yes" recommendation is meant to offer/execute compaction via the
// existing compactor (core/agentgraph/compaction). Nothing in this
// package calls that machinery — per tasks.md item 6, WP04-06 register
// kinds and a working backend without wiring a live call site or
// rendering a chip; that is WP07's job. Snapshot below is populated by a
// future WP07 caller from real session/token-accounting state; this
// WP's tests populate it directly.
package compactnow

import (
	"fmt"
	"math"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

// KindID is compact_now's stable registry id (spec.md §2's table).
const KindID = "compact_now"

// PromptVersion identifies this kind's prompt-text/feature-contract
// revision.
const PromptVersion = "v1"

// ModelID is the honest, non-model model id HeuristicAdvisor stamps onto
// every compact_now Recommendation.
const ModelID = "heuristic/compact-fill-threshold-v1"

// fillThreshold is the context_fill_fraction at or above which Heuristic
// answers "yes, compact now." 0.75 mirrors the compaction engine's own
// intuition that leaving a comfortable margin below the forced-compact
// ceiling preserves more task-relevant context than waiting for the
// engine to compact under pressure (spec's own question for this kind).
const fillThreshold = 0.75

// staleTurnsBonusFloor is how many turns since the last compaction start
// adding a small confidence bonus — long-uncompacted sessions carry more
// accumulated staleness risk than the raw fill fraction alone captures.
const staleTurnsBonusFloor = 20

// staleTurnsBonus is the flat confidence-point bonus applied once
// TurnsSinceLastCompaction reaches staleTurnsBonusFloor. Capped by
// Heuristic so the total never exceeds 100.
const staleTurnsBonus = 10

// Snapshot is compact_now's own session-state input type. A future WP07
// caller populates this from the compaction engine's live token
// accounting immediately before a Recommend call; this package's tests
// populate it directly.
type Snapshot struct {
	// ContextFillFraction is the fraction (0-1) of the model's context
	// window currently consumed.
	ContextFillFraction float64
	// TokensInSpan mirrors SessionCompactedPayload.TokensInSpan: the
	// token count of the span a compaction pass would summarize.
	TokensInSpan int
	// TurnsSinceLastCompaction counts turns elapsed since the last
	// successful compaction (or session start, if none yet).
	TurnsSinceLastCompaction int
	// ToolResultTokenFraction is the fraction (0-1) of TokensInSpan that
	// is stale tool-result content (design §4's "tool-result staleness
	// profile," spec.md §2's table) rather than conversational turns.
	ToolResultTokenFraction float64
	// ModelContextLimit mirrors core/llm.ModelInfo.ContextWindow — the
	// active model's max context length in tokens.
	ModelContextLimit int
	// HistoricalCompressionRatio mirrors
	// SessionCompactedPayload.CompressionRatio from this session's most
	// recent prior compaction (0 if none yet).
	HistoricalCompressionRatio float64
	// FeaturesIncomplete, when true, tells Extract that the caller built
	// this Snapshot from placeholder/zero-value data because no
	// ChatRunner-visible token-accounting accessor is wired yet (see
	// core/rpc/views/agentgraph/chat/advice_hook.go's fireAdvice, the one
	// production caller — its own comment names the exact blocker). This
	// is the "justify site" advice.FeaturesCompleteness's doc comment
	// refers to: the kind package is the one place that knows whether ITS
	// OWN Snapshot fields are real, so it is also the place that decides
	// how that maps onto FeaturesComplete() — never hardcoded by kind id
	// in the label-capture bridge.
	FeaturesIncomplete bool
}

// Features is compact_now's model/label-facing feature vector — design
// §4's harness-kinds table. Field order is declaration order (see
// branchnow.Features' identical note on advice.FeaturesHash).
// FeaturesIncomplete is deliberately `json:"-"`: it is a training-corpus
// bookkeeping signal, not part of the model-facing feature vector or the
// FeaturesHash cache key (a placeholder run and a future real run with
// identical resulting numbers are still "materially identical features"
// for caching purposes — completeness is a provenance fact the DB layer
// tracks in its own column, not a fact about the features themselves).
type Features struct {
	ContextFillFraction        float64 `json:"context_fill_fraction"`
	TokensInSpan               int     `json:"tokens_in_span"`
	TurnsSinceLastCompaction   int     `json:"turns_since_last_compaction"`
	ToolResultTokenFraction    float64 `json:"tool_result_token_fraction"`
	ModelContextLimit          int     `json:"model_context_limit"`
	HistoricalCompressionRatio float64 `json:"historical_compression_ratio"`
	FeaturesIncomplete         bool    `json:"-"`
}

// FeaturesComplete implements advice.FeaturesCompleteness.
func (f Features) FeaturesComplete() bool { return !f.FeaturesIncomplete }

// Extract implements advice.FeatureExtractor.
func Extract(input any) (advice.Features, error) {
	snap, ok := input.(Snapshot)
	if !ok {
		return nil, fmt.Errorf("compactnow: want Snapshot input, got %T", input)
	}
	return Features{
		ContextFillFraction:        snap.ContextFillFraction,
		TokensInSpan:               snap.TokensInSpan,
		TurnsSinceLastCompaction:   snap.TurnsSinceLastCompaction,
		ToolResultTokenFraction:    snap.ToolResultTokenFraction,
		ModelContextLimit:          snap.ModelContextLimit,
		HistoricalCompressionRatio: snap.HistoricalCompressionRatio,
		FeaturesIncomplete:         snap.FeaturesIncomplete,
	}, nil
}

// RenderPrompt implements advice.PromptRenderer. Required by
// AdviceKind.validate() (forward-compat scaffold for a Phase-B/C
// backend, per design §5.4 — compact_now's own table entry names its
// laya role "None: features are purely numeric; a 421M encoder here is
// latency and RSS waste," so a GBDT is the only graduation path this
// kind is ever expected to take; RenderPrompt exists purely to satisfy
// the registry contract). Never called by rung-R1's HeuristicAdvisor.
func RenderPrompt(features advice.Features) (system, user string) {
	f, ok := features.(Features)
	if !ok {
		return "", ""
	}
	system = "You judge whether compacting the session now preserves more task-relevant context than " +
		"waiting for the forced threshold. Respond with a strict JSON object: " +
		"{\"decision\": bool, \"confidence\": 0-100}."
	user = fmt.Sprintf(
		"context_fill_fraction=%.3f tokens_in_span=%d turns_since_last_compaction=%d "+
			"tool_result_token_fraction=%.3f model_context_limit=%d historical_compression_ratio=%.3f",
		f.ContextFillFraction, f.TokensInSpan, f.TurnsSinceLastCompaction,
		f.ToolResultTokenFraction, f.ModelContextLimit, f.HistoricalCompressionRatio,
	)
	return system, user
}

// Heuristic implements advice.HeuristicFunc — compact_now's rung-R1
// backend (design §4: "Day-1 backend: Fill-threshold rule").
//
// # Confidence mapping (documented per spec's "principled mapping"
// requirement)
//
// base = round(100 * clamp(context_fill_fraction, 0, 1)) — the fill
// fraction itself IS the confidence: a session at 90% fill is a stronger
// "compact now" signal than one at 76%, and the mapping is linear and
// legible rather than a second derived curve. A flat +10 point bonus is
// added when turns_since_last_compaction >= 20 (accumulated staleness
// the raw fill number alone under-weights), capped at 100. Decision is
// true exactly when context_fill_fraction >= fillThreshold (0.75) —
// independent of the bonus, so the bonus affects confidence (and
// therefore whether a future ≥75 render gate shows it) without changing
// the binary answer itself.
//
// Deliberately NOT used in the decision (captured as Features for a
// future GBDT per design §4/§5.4's "GBDT — terminal" row):
// tokens_in_span, tool_result_token_fraction, model_context_limit,
// historical_compression_ratio.
func Heuristic(features advice.Features) (decision bool, confidence int, modelID string, err error) {
	f, ok := features.(Features)
	if !ok {
		return false, 0, "", fmt.Errorf("compactnow: want Features, got %T", features)
	}
	fill := f.ContextFillFraction
	if fill < 0 {
		fill = 0
	}
	if fill > 1 {
		fill = 1
	}
	confidence = int(math.Round(fill * 100))
	if f.TurnsSinceLastCompaction >= staleTurnsBonusFloor {
		confidence += staleTurnsBonus
	}
	if confidence > 100 {
		confidence = 100
	}
	decision = f.ContextFillFraction >= fillThreshold
	return decision, confidence, ModelID, nil
}

func init() {
	// ID is the literal "compact_now" (not the KindID identifier) — see
	// branchnow.go's init() comment for why: check-advice-kinds.sh's
	// scan requires a literal string right after `ID:`. TestRegistration
	// (compactnow_test.go) pins that this literal and KindID never drift.
	advice.MustRegister(advice.AdviceKind{
		ID:            "compact_now",
		PromptVersion: PromptVersion,
		SafetyClass:   advice.SafetySuggestOnly,
		Extract:       Extract,
		RenderPrompt:  RenderPrompt,
	})
}
