package chat

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// overflowingLLM returns a context-overflow error for its first
// `overflowFor` generations and succeeds thereafter. It stands in for a
// session that is genuinely too big until compaction shrinks it.
type overflowingLLM struct {
	mu          sync.Mutex
	calls       int
	overflowFor int
}

func (f *overflowingLLM) Generate(_ context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.overflowFor {
		return coreag.LLMResponse{}, &corellm.ErrInvalidRequest{
			Status:  400,
			Message: "This model's maximum context length is 8192 tokens",
		}
	}
	return coreag.LLMResponse{Content: "ok"}, nil
}

func (f *overflowingLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// overflowFixture builds the minimum viable runner + Env for exercising
// recoverFromOverflow: a real kernel over a one-model-node graph whose
// LLM overflows a configurable number of times.
func overflowFixture(t *testing.T, budget, overflowFor int) (*ChatRunner, *chatSub, *coreag.Env, *overflowingLLM, *fakeCompactionEngine, *recordingBroker) {
	t.Helper()
	llm := &overflowingLLM{overflowFor: overflowFor}
	engine := &fakeCompactionEngine{}
	broker := &recordingBroker{}

	runner := &ChatRunner{cfg: Config{
		Kernel:                coreag.NewKernel(),
		Broker:                broker,
		Compaction:            &CompactionDeps{Engine: engine},
		MaxOverflowRecoveries: func() int { return budget },
	}}
	sub := &chatSub{id: "sub-overflow", sessionID: "sess-overflow", profileID: "prof"}
	env := &coreag.Env{
		RunID:     "run-overflow",
		SessionID: "sess-overflow",
		LLM:       llm,
		Graph: &coreag.Graph{
			ID:          "g-overflow",
			SpecVersion: "1",
			Entrypoints: []string{"llm1"},
			Nodes: []coreag.Node{
				{ID: "llm1", Kind: coreag.NodeKindModel, Attrs: coreag.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 100,
				}},
			},
		},
	}
	return runner, sub, env, llm, engine, broker
}

func overflowError() error {
	return &corellm.ErrInvalidRequest{
		Status:  400,
		Message: "This model's maximum context length is 8192 tokens",
	}
}

func countTopic(broker *recordingBroker, topic string) int {
	n := 0
	for _, e := range broker.snapshot() {
		if e.topic == topic {
			n++
		}
	}
	return n
}

// TestRecoverFromOverflow_BudgetAllowsSecondRecovery is the WP03
// headline (turn-context-runway-01PMAG03 / FR-003): two recoveries
// succeed where the hardcoded one-shot allowed exactly one. The first
// redrive overflows again; with a budget of 2 the turn gets a second
// rescue and completes.
func TestRecoverFromOverflow_BudgetAllowsSecondRecovery(t *testing.T) {
	t.Parallel()
	// overflowFor=1: the redrive after the first recovery still
	// overflows; the redrive after the second succeeds.
	runner, sub, env, llm, engine, broker := overflowFixture(t, 2, 1)

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, overflowError())

	if reason != "completed" || !clean {
		t.Fatalf("reason = %q clean = %v, want completed/true (message %q)", reason, clean, message)
	}
	if got := sub.overflowRecoveries.Load(); got != 2 {
		t.Fatalf("overflowRecoveries = %d, want 2", got)
	}
	if got := engine.compactCount(); got != 2 {
		t.Fatalf("Compact calls = %d, want 2 (one per recovery)", got)
	}
	if got := llm.callCount(); got != 2 {
		t.Fatalf("redrives = %d, want 2", got)
	}
	if got := countTopic(broker, "chat:overflow-recovery"); got != 2 {
		t.Fatalf("chat:overflow-recovery emissions = %d, want 2 (one per recovery)", got)
	}
}

// TestRecoverFromOverflow_ExhaustionReportsSessionFull pins the honest
// terminal copy: when the budget runs out the user is told the session
// is full, not handed a raw provider 400 that reads as a generic
// backend failure.
func TestRecoverFromOverflow_ExhaustionReportsSessionFull(t *testing.T) {
	t.Parallel()
	// Never recovers: every redrive overflows.
	runner, sub, env, _, engine, broker := overflowFixture(t, 2, 99)

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, overflowError())

	if clean {
		t.Fatalf("clean = true after exhausting the recovery budget")
	}
	if reason != "backend-error" {
		t.Fatalf("reason = %q, want backend-error", reason)
	}
	if message != compaction.ErrSessionFull.Error() {
		t.Fatalf("message = %q, want the honest session-full copy %q",
			message, compaction.ErrSessionFull.Error())
	}
	if got := sub.overflowRecoveries.Load(); got != 2 {
		t.Fatalf("overflowRecoveries = %d, want 2 (the budget, spent exactly)", got)
	}
	if got := engine.compactCount(); got != 2 {
		t.Fatalf("Compact calls = %d, want 2", got)
	}
	if got := countTopic(broker, "chat:overflow-recovery"); got != 2 {
		t.Fatalf("chat:overflow-recovery emissions = %d, want 2", got)
	}
}

// TestRecoverFromOverflow_DefaultBudgetIsTodaysOneShot is the
// behaviour-preservation check: the default budget reproduces the
// pre-WP03 sub.overflowRetried one-shot exactly — one rescue, then the
// turn ends.
func TestRecoverFromOverflow_DefaultBudgetIsTodaysOneShot(t *testing.T) {
	t.Parallel()
	if DefaultMaxOverflowRecoveriesPerTurn != 1 {
		t.Fatalf("DefaultMaxOverflowRecoveriesPerTurn = %d, want 1 — the default must preserve today's behaviour",
			DefaultMaxOverflowRecoveriesPerTurn)
	}
	// Nil resolver ⇒ the package default.
	runner, sub, env, _, engine, _ := overflowFixture(t, 0, 99)
	runner.cfg.MaxOverflowRecoveries = nil

	reason, _, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, overflowError())

	if clean || reason != "backend-error" {
		t.Fatalf("reason = %q clean = %v, want backend-error/false", reason, clean)
	}
	if got := sub.overflowRecoveries.Load(); got != 1 {
		t.Fatalf("overflowRecoveries = %d, want 1 (the pre-mission one-shot)", got)
	}
	if got := engine.compactCount(); got != 1 {
		t.Fatalf("Compact calls = %d, want 1", got)
	}
}

// TestRecoverFromOverflow_ZeroBudgetSurfacesTheRealError pins the
// disabled case: with no rescues allowed we must report the actual
// overflow, not the session-full copy — the session may be perfectly
// compactable, we just were not permitted to try.
func TestRecoverFromOverflow_ZeroBudgetSurfacesTheRealError(t *testing.T) {
	t.Parallel()
	runner, sub, env, _, engine, broker := overflowFixture(t, 0, 99)
	original := overflowError()

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, original)

	if clean || reason != "backend-error" {
		t.Fatalf("reason = %q clean = %v, want backend-error/false", reason, clean)
	}
	if message != original.Error() {
		t.Fatalf("message = %q, want the raw overflow %q", message, original.Error())
	}
	if got := sub.overflowRecoveries.Load(); got != 0 {
		t.Fatalf("overflowRecoveries = %d, want 0", got)
	}
	if got := engine.compactCount(); got != 0 {
		t.Fatalf("Compact calls = %d, want 0", got)
	}
	if got := countTopic(broker, "chat:overflow-recovery"); got != 0 {
		t.Fatalf("emitted %d recovery events with a zero budget", got)
	}
}

// TestRecoverFromOverflow_CompactionUnavailableSurfacesTheRealError:
// when compaction cannot run at all, the session is not necessarily
// full — our recovery path is just missing. Report the real problem.
func TestRecoverFromOverflow_CompactionUnavailableSurfacesTheRealError(t *testing.T) {
	t.Parallel()
	runner, sub, env, _, _, _ := overflowFixture(t, 3, 99)
	runner.cfg.Compaction = nil // engine not wired
	original := overflowError()

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, original)

	if clean || reason != "backend-error" {
		t.Fatalf("reason = %q clean = %v, want backend-error/false", reason, clean)
	}
	if message != original.Error() {
		t.Fatalf("message = %q, want the raw overflow %q — an unwired compactor is not a full session",
			message, original.Error())
	}
	if got := sub.overflowRecoveries.Load(); got != 0 {
		t.Fatalf("overflowRecoveries = %d, want 0 (nothing was spent)", got)
	}
}

// TestRecoverFromOverflow_NonOverflowRedriveErrorIsReportedVerbatim:
// a redrive that fails for an unrelated reason must not be retried or
// relabelled — the user sees the actual failure.
func TestRecoverFromOverflow_NonOverflowRedriveErrorIsReportedVerbatim(t *testing.T) {
	t.Parallel()
	runner, sub, env, _, _, _ := overflowFixture(t, 3, 0)
	// overflowFor=0 ⇒ the redrive succeeds, so force a different
	// failure: an entrypoint that does not exist.
	env.Graph.Entrypoints = []string{"nope"}

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, overflowError())

	if clean || reason != "backend-error" {
		t.Fatalf("reason = %q clean = %v, want backend-error/false", reason, clean)
	}
	if message == compaction.ErrSessionFull.Error() {
		t.Fatalf("a non-overflow redrive failure was relabelled as session-full")
	}
	if got := sub.overflowRecoveries.Load(); got != 1 {
		t.Fatalf("overflowRecoveries = %d, want 1 (stopped after the first non-overflow failure)", got)
	}
}

// TestRecoverFromOverflow_RearmsTheWatermark pins the interaction
// between WP03's recovery and WP02's watermark.
//
// The recovery compacts the persisted history, so the redrive starts
// from a new, smaller transcript. If the watermark kept the
// pre-overflow baseline it would be measuring against a transcript that
// no longer exists — and a large one, since we only reached this path
// by overflowing — so every mid-run site would be refused. Mid-run
// compaction would become least likely in exactly the run that just
// proved it needs it.
func TestRecoverFromOverflow_RearmsTheWatermark(t *testing.T) {
	t.Parallel()
	runner, sub, env, _, _, _ := overflowFixture(t, 2, 0) // redrive succeeds

	watermark := coreag.NewCompactionWatermark(coreag.CompactionWatermarkPolicy{})
	// Latch a big pre-overflow baseline, the state a run is in when it
	// hits the context window.
	watermark.Admit(400_000)
	if base, ok := watermark.Baseline(); !ok || base != 400_000 {
		t.Fatalf("fixture did not latch: baseline = (%d, %v)", base, ok)
	}
	env.AutoCompaction = watermark

	reason, message, clean := runner.recoverFromOverflow(
		slog.Default(), sub, env, overflowError())
	if reason != "completed" || !clean {
		t.Fatalf("reason = %q clean = %v, want completed/true (%q)", reason, clean, message)
	}

	if _, ok := watermark.Baseline(); ok {
		t.Fatalf("watermark still holds the pre-overflow baseline after a successful recovery; the redrive will be measured against a transcript that no longer exists")
	}
	// And it re-baselines from the post-compaction transcript rather
	// than firing immediately.
	if watermark.Admit(5_000) {
		t.Fatalf("re-latching observation after recovery fired")
	}
	if got, _ := watermark.Baseline(); got != 5_000 {
		t.Fatalf("post-recovery baseline = %d, want 5000", got)
	}
}

// TestMaxOverflowRecoveries_NegativeFallsBackToDefault: a negative dial
// must not read as "unlimited" — an unbounded rescue loop against a
// session that genuinely cannot fit would burn compaction calls forever.
func TestMaxOverflowRecoveries_NegativeFallsBackToDefault(t *testing.T) {
	t.Parallel()
	r := &ChatRunner{cfg: Config{MaxOverflowRecoveries: func() int { return -5 }}}
	if got := r.maxOverflowRecoveries(); got != DefaultMaxOverflowRecoveriesPerTurn {
		t.Fatalf("maxOverflowRecoveries() = %d, want %d", got, DefaultMaxOverflowRecoveriesPerTurn)
	}
	var nilRunner *ChatRunner
	if got := nilRunner.maxOverflowRecoveries(); got != DefaultMaxOverflowRecoveriesPerTurn {
		t.Fatalf("nil runner maxOverflowRecoveries() = %d, want %d", got, DefaultMaxOverflowRecoveriesPerTurn)
	}
}
