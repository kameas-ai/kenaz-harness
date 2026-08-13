package chat

import (
	"context"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	fr041 "github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
)

// session_compaction_reload_test.go pins review finding F3 of
// agentgraph-total-convergence-01PMGX01 WP08.
//
// THE GAP. The `compact` node's whole reason for existing downstream of
// history_read is that it hands the COMPACTED transcript to the rest of
// the graph. The strategy does that by re-reading the session after the
// engine has rewritten it (`reload()` in session_compaction.go). Nothing
// tested it: a reviewer mutated `reload()` to `passthrough()` and all
// 1659 tests in this package still passed.
//
// WHY THAT MUTATION IS DANGEROUS RATHER THAN COSMETIC. It is silent and
// it is worst exactly when it matters most. The engine still compacts —
// the DB rows are rewritten, the summary exists, the audit entry lands —
// so every count-based assertion in the golden matrix still holds. But
// the node emits the PRE-compaction slice, so the turn that triggered
// compaction runs on the uncompacted transcript: the one turn that was
// already at 80% of the window is the one that does not benefit. Nothing
// surfaces it. The next turn re-reads history and looks fine.
//
// The mutation is survivable in production for the same reason it is
// invisible in tests — the model node's own fallback re-reads
// env.History when its inbound messages are empty — which is precisely
// why the node's contract needs its own pin rather than relying on a
// downstream re-read to paper over it.

// reloadHistoryReader serves one transcript before compaction and a
// different, smaller one after, so the two are distinguishable by
// content. `compacted` flips when the engine reports success.
type reloadHistoryReader struct {
	mu        sync.Mutex
	compacted bool
	before    []coreag.Message
	after     []coreag.Message
	reads     int
}

func (r *reloadHistoryReader) History(_ context.Context, _ string, _ int) ([]coreag.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	if r.compacted {
		return append([]coreag.Message(nil), r.after...), nil
	}
	return append([]coreag.Message(nil), r.before...), nil
}

func (r *reloadHistoryReader) markCompacted() {
	r.mu.Lock()
	r.compacted = true
	r.mu.Unlock()
}

func (r *reloadHistoryReader) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

// rewritingEngine is a compaction.Engine that reports success and flips
// the history reader over to its post-compaction transcript, the way the
// real engine's transactional rewrite does.
type rewritingEngine struct {
	hist *reloadHistoryReader

	mu       sync.Mutex
	compacts int
	rolls    int
}

func (e *rewritingEngine) Compact(_ context.Context, _ string, _ fr041.ProviderProfileRef, _ float64) (string, error) {
	e.mu.Lock()
	e.compacts++
	e.mu.Unlock()
	e.hist.markCompacted()
	return "summary-1", nil
}

func (e *rewritingEngine) RollingSummarize(_ context.Context, _ string, _ fr041.ProviderProfileRef, _ int) (string, error) {
	e.mu.Lock()
	e.rolls++
	e.mu.Unlock()
	e.hist.markCompacted()
	return "rolling-1", nil
}

func (e *rewritingEngine) counts() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compacts, e.rolls
}

// runCompactNodeForReload drives the real path — kernel, compact node,
// bound pipeline, session-rewrite strategy — and returns the messages
// the node put on its output port.
func runCompactNodeForReload(t *testing.T, tier compactionpolicy.CompactionAggressiveness) ([]coreag.Message, *rewritingEngine, *reloadHistoryReader) {
	t.Helper()

	hist := &reloadHistoryReader{
		before: []coreag.Message{
			{Role: "user", Content: "turn one, long and old"},
			{Role: "assistant", Content: "reply one, long and old"},
			fillerMessage("user", 85),
			{Role: "user", Content: "the next user turn"},
		},
		after: []coreag.Message{
			{Role: "system", Content: "SUMMARY OF EARLIER CONVERSATION"},
			{Role: "user", Content: "the next user turn"},
		},
	}
	eng := &rewritingEngine{hist: hist}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       hist,
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction: &CompactionDeps{
			Engine:         eng,
			Aggressiveness: func() compactionpolicy.CompactionAggressiveness { return tier },
			CompactionModel: func() (fr041.ProviderProfileRef, bool) {
				return fr041.ProviderProfileRef{}, false
			},
			RecentWindow: func() int { return 4 },
			MaxContextTokens: func(_ fr041.ProviderProfileRef) (int, bool) {
				return 100, true
			},
		},
		CompactionPipeline: newTierPipeline(string(tier)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	env := &coreag.Env{
		RunID:     "run-reload",
		SessionID: "session-1",
		Graph:     compactHeadGraph(),
		History:   historyAdapterFunc(hist.History),
		Compactor: runner.bindCompactor("profile-1", "model-1"),
	}
	if err := coreag.NewKernel().Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := env.State.Outputs("compact_history")
	if out == nil {
		t.Fatalf("compact_history produced no outputs")
	}
	v, ok := out.Get("result")
	if !ok {
		t.Fatalf("compact_history has no `result` port; the node's only output is missing")
	}
	msgs, ok := v.([]coreag.Message)
	if !ok {
		t.Fatalf("compact_history `result` = %T, want []coreag.Message", v)
	}
	return msgs, eng, hist
}

// TestCompactNode_ThresholdTier_EmitsReloadedHistory is the ModeThreshold
// half: after the engine rewrites the session, the node's result port
// must carry the POST-compaction transcript, not the slice it was handed.
func TestCompactNode_ThresholdTier_EmitsReloadedHistory(t *testing.T) {
	msgs, eng, hist := runCompactNodeForReload(t, compactionpolicy.AggressivenessBalanced)

	if compacts, _ := eng.counts(); compacts != 1 {
		t.Fatalf("engine Compact calls = %d, want 1 — the fixture is not tripping the trigger, so the reload assertion below is vacuous", compacts)
	}
	if hist.readCount() < 2 {
		t.Fatalf("history was read %d time(s); a reload after compaction requires a second read", hist.readCount())
	}
	assertReloaded(t, msgs)
}

// TestCompactNode_MaximalTier_EmitsReloadedHistory is the ModeRolling
// half. It has its own success path in the strategy (RollingSummarize
// rather than Compact), so it needs its own pin — a fix applied to one
// branch would otherwise leave the other silently passing through.
func TestCompactNode_MaximalTier_EmitsReloadedHistory(t *testing.T) {
	msgs, eng, hist := runCompactNodeForReload(t, compactionpolicy.AggressivenessMaximal)

	if _, rolls := eng.counts(); rolls != 1 {
		t.Fatalf("engine RollingSummarize calls = %d, want 1", rolls)
	}
	if hist.readCount() < 2 {
		t.Fatalf("history was read %d time(s); a reload after compaction requires a second read", hist.readCount())
	}
	assertReloaded(t, msgs)
}

// assertReloaded checks the node emitted the post-compaction transcript.
// It asserts on CONTENT, not just length: a passthrough happens to be a
// different length here, but a future fixture change could make the two
// slices the same size and silently un-arm the test.
func assertReloaded(t *testing.T, msgs []coreag.Message) {
	t.Helper()
	if len(msgs) == 0 {
		t.Fatalf("compact node emitted no messages")
	}
	if msgs[0].Content != "SUMMARY OF EARLIER CONVERSATION" {
		t.Fatalf("compact node emitted the PRE-compaction transcript (first message %q). The engine rewrote the session but the node passed its input through, so the turn that triggered compaction runs uncompacted — silently, because the next turn re-reads history and looks fine.",
			msgs[0].Content)
	}
	if len(msgs) != 2 {
		t.Fatalf("compact node emitted %d messages, want the 2 of the post-compaction transcript", len(msgs))
	}
}
