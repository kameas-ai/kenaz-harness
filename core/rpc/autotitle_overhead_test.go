package rpc

// autotitle_overhead_test.go — model-settings-reach-the-model-01PMZ101
// WP07, the auto-title half. core/sessions/autotitle/wiring/llm.go's
// LLMCaller.Overhead() had zero non-test callers (only
// llm_test.go:115,143,265 in that package called it) despite its own doc
// comment claiming "so the rpc layer can surface it in the same
// per-session cost panel" — recordOverhead ran on every successful
// auto-title call and the tally was accumulated, then discarded. The
// compaction half of this same class was already fixed (chat-turn-
// integrity-01PMZ606 WP12, see compaction_overhead_test.go) and is the
// template this file follows: drive a REAL LLMCaller.Call through a fake
// registry (there is no public setter for OverheadTotals — recordOverhead
// is private and only reachable via Call), then assert the RPC-layer
// projection reads it back.
//
// This mirrors CLAUDE.md's own proof bar: "the overhead reaches the same
// response the compaction totals reach" — both are asserted, in the same
// test, off the SAME CompactionOverheadInfo value CompactionOverhead()
// returns.

import (
	"context"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	autotitlewiring "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle/wiring"
)

// fakeAutoTitleRegistry is a minimal autotitlewiring.LLMRegistry (just
// Stream) whose canned Response carries non-zero Usage + Cost, so
// LLMCaller.Call's private recordOverhead has real data to fold into the
// running OverheadTotals. Modeled directly on fakeOverheadRegistry /
// fakeOverheadStream in compaction_overhead_test.go.
type fakeAutoTitleRegistry struct{}

func (fakeAutoTitleRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return &fakeAutoTitleStream{}, nil
}

type fakeAutoTitleStream struct{}

func (s *fakeAutoTitleStream) Events() <-chan corellm.StreamEvent {
	ch := make(chan corellm.StreamEvent)
	close(ch)
	return ch
}
func (s *fakeAutoTitleStream) Cancel() error { return nil }
func (s *fakeAutoTitleStream) Final() (corellm.Response, error) {
	return corellm.Response{
		Content:      []corellm.ContentBlock{{Type: "text", Text: "Auto-generated title"}},
		FinishReason: "stop",
		Usage:        corellm.Usage{InputTokens: 400, OutputTokens: 12},
		Cost:         corellm.Cost{Currency: "USD", Total: 0.0023},
	}, nil
}

// TestAPI_CompactionOverhead_ReadsAutoTitleTotals is WP07's core proof:
// with a real auto-title call landed, does CompactionOverhead() surface
// its AutoTitle* totals — reaching the SAME response the compaction
// totals reach, per the mission audit's stated proof bar.
func TestAPI_CompactionOverhead_ReadsAutoTitleTotals(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	caller := autotitlewiring.NewLLMCaller(fakeAutoTitleRegistry{},
		autotitlewiring.WithProfileID("test-profile", "test-model"))
	if caller == nil {
		t.Fatal("NewLLMCaller returned nil for a non-nil registry")
	}

	// Drive a REAL Call — the only way to get non-zero OverheadTotals;
	// recordOverhead is private and only invoked from inside Call itself.
	if _, _, _, err := caller.Call(ctx, "system prompt", "user prompt"); err != nil {
		t.Fatalf("Call: %v", err)
	}

	a := &API{autotitleLLM: caller}
	got, err := a.CompactionOverhead(ctx)
	if err != nil {
		t.Fatalf("CompactionOverhead: %v", err)
	}

	if got.AutoTitleCalls != 1 {
		t.Errorf("AutoTitleCalls = %d, want 1", got.AutoTitleCalls)
	}
	if got.AutoTitleTotal != 0.0023 {
		t.Errorf("AutoTitleTotal = %v, want 0.0023", got.AutoTitleTotal)
	}
	if got.AutoTitleCurrency != "USD" {
		t.Errorf("AutoTitleCurrency = %q, want USD", got.AutoTitleCurrency)
	}
	if got.AutoTitleInputTokens != 400 || got.AutoTitleOutputTokens != 12 {
		t.Errorf("AutoTitleInputTokens/OutputTokens = %d/%d, want 400/12",
			got.AutoTitleInputTokens, got.AutoTitleOutputTokens)
	}
	if got.AutoTitleIndeterminateCalls != 0 {
		t.Errorf("AutoTitleIndeterminateCalls = %d, want 0", got.AutoTitleIndeterminateCalls)
	}

	// The compaction half must stay exactly zero — proving the two halves
	// are populated independently (one being disabled/absent must not
	// suppress or pollute the other's totals).
	if got.Calls != 0 || got.Total != 0 || got.Currency != "" {
		t.Errorf("compaction fields leaked non-zero data from the auto-title fixture: %+v", got)
	}
}

// TestAPI_CompactionOverhead_AutoTitleZeroValueWhenNil pins the degrade
// contract for the auto-title half specifically: a.autotitleLLM == nil
// (no LLM registry wired at boot) must not error and must leave the
// AutoTitle* fields at their zero value.
func TestAPI_CompactionOverhead_AutoTitleZeroValueWhenNil(t *testing.T) {
	t.Parallel()
	a := &API{}
	got, err := a.CompactionOverhead(context.Background())
	if err != nil {
		t.Fatalf("CompactionOverhead: %v", err)
	}
	if got.AutoTitleCalls != 0 || got.AutoTitleTotal != 0 || got.AutoTitleCurrency != "" ||
		got.AutoTitleIndeterminateCalls != 0 || got.AutoTitleInputTokens != 0 || got.AutoTitleOutputTokens != 0 {
		t.Errorf("CompactionOverhead().AutoTitle* = %+v, want zero value", got)
	}
}
