package chat

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// autonomy-knobs-live-01PMAG02 WP03.
//
// Two defects, one fix:
//
//  1. env.Budget was never assigned anywhere in core/. A repo-wide search
//     for a write to the field found only an unrelated credstore Budget,
//     the manifest-inheritance merge, and Graph<->wire serialization. So
//     env.Budget was the zero value on every chat run, every guard in
//     kernel.checkBudget is `if env.Budget.X > 0 && ...`, and all of them
//     short-circuited. chat_default.yaml's budget block was decorative:
//     production chat enforced no per-run caps at all.
//
//     The gap survived because it was partial. DoomLoopThreshold and
//     MaxBacktracksPerRun treat zero as "use the package default" rather
//     than "unlimited", so the behavioural guards kept working while the
//     volume guards silently did not.
//
//  2. autonomy.ResolvedKnobs.TokenCeilingPerTurn had no consumer.
//
// The knob may only LOWER the graph's declared ceiling. A graph budget is
// the author's safety cap; a Settings toggle that could raise it would
// let the UI defeat a limit the graph declared deliberately.

func TestApplyTokenCeilingKnob_GraphBudgetSurvivesWithNoKnob(t *testing.T) {
	t.Parallel()
	in := coreag.Budget{
		MaxTokensPerRun:    200000,
		MaxLLMCallsPerRun:  100,
		MaxToolCallsPerRun: 200,
	}
	got := applyTokenCeilingKnob(in, autonomy.ResolvedKnobs{})
	if got != in {
		t.Fatalf("no-knob budget = %+v, want the graph budget carried through unchanged %+v", got, in)
	}
	// The regression this pins: the caps must be non-zero, or
	// checkBudget skips every one of them.
	if got.MaxTokensPerRun == 0 || got.MaxLLMCallsPerRun == 0 || got.MaxToolCallsPerRun == 0 {
		t.Fatal("a zero cap means checkBudget short-circuits — production chat would run uncapped")
	}
}

func TestApplyTokenCeilingKnob_KnobLowersCeiling(t *testing.T) {
	t.Parallel()
	in := coreag.Budget{MaxTokensPerRun: 200000, MaxLLMCallsPerRun: 100}
	got := applyTokenCeilingKnob(in, autonomy.ResolvedKnobs{TokenCeilingPerTurn: 50000})
	if got.MaxTokensPerRun != 50000 {
		t.Errorf("MaxTokensPerRun = %d, want 50000 (knob lowers)", got.MaxTokensPerRun)
	}
	// The knob is token-scoped; it must not disturb the other caps.
	if got.MaxLLMCallsPerRun != 100 {
		t.Errorf("MaxLLMCallsPerRun = %d, want 100 untouched", got.MaxLLMCallsPerRun)
	}
}

func TestApplyTokenCeilingKnob_KnobCannotRaiseGraphCeiling(t *testing.T) {
	t.Parallel()
	in := coreag.Budget{MaxTokensPerRun: 200000}
	got := applyTokenCeilingKnob(in, autonomy.ResolvedKnobs{TokenCeilingPerTurn: 999999})
	if got.MaxTokensPerRun != 200000 {
		t.Fatalf("MaxTokensPerRun = %d, want the graph's 200000 to hold — a Settings toggle must not defeat a graph-declared safety cap", got.MaxTokensPerRun)
	}
}

// When the graph declares no token cap, the knob may establish one:
// zero on either side means "no opinion", not "unlimited wins".
func TestApplyTokenCeilingKnob_KnobEstablishesCeilingWhenGraphHasNone(t *testing.T) {
	t.Parallel()
	got := applyTokenCeilingKnob(coreag.Budget{}, autonomy.ResolvedKnobs{TokenCeilingPerTurn: 12345})
	if got.MaxTokensPerRun != 12345 {
		t.Fatalf("MaxTokensPerRun = %d, want 12345", got.MaxTokensPerRun)
	}
}

// autonomyKnobs must be nil-safe: the chat runner is constructed without
// an AutonomyKnobs provider on several test and boot paths.
func TestChatRunner_AutonomyKnobsNilSafe(t *testing.T) {
	t.Parallel()
	var r *ChatRunner
	if got := r.autonomyKnobs(context.Background(), "sess-1"); got.TokenCeilingPerTurn != 0 {
		t.Errorf("nil receiver: got %+v, want zero knobs", got)
	}
	r2 := &ChatRunner{}
	if got := r2.autonomyKnobs(context.Background(), "sess-1"); got.TokenCeilingPerTurn != 0 {
		t.Errorf("nil provider: got %+v, want zero knobs", got)
	}
}
