package agentgraph_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// turn-context-runway-01PMAG03 WP04 — integration.
//
// The two claims this mission has to make good on, exercised end-to-end
// through a real kernel over a loop-shaped graph:
//
//  1. A long turn that used to grow until it overflowed and died now
//     compacts mid-run and completes.
//  2. A short turn is unchanged — no automatic compaction call, and
//     byte-identical prompts to the pre-mission suppression path.

// growingTranscript models a turn accumulating context: every read
// returns the live conversation, and each loop iteration appends a
// chunk of tool output before the next model call. Compaction replaces
// the transcript in place, the way the real pipeline does.
//
// Race-safe per CLAUDE.md: the kernel reads it from executor goroutines
// while the test body inspects it.
type growingTranscript struct {
	mu       sync.Mutex
	msgs     []agentgraph.Message
	perTurn  int
	appends  int
	compacts int
}

func newGrowingTranscript(seed, perTurn int) *growingTranscript {
	g := &growingTranscript{perTurn: perTurn}
	for i := 0; i < seed; i++ {
		g.msgs = append(g.msgs, agentgraph.Message{
			Role:    "user",
			Content: fmt.Sprintf("seed turn %d %s", i, strings.Repeat("word ", 20)),
		})
	}
	return g
}

// History satisfies the reader seam AND grows the transcript, so each
// successive model call in the loop sees more context than the last.
func (g *growingTranscript) History(_ context.Context, _ string, _ int) ([]agentgraph.Message, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := 0; i < g.perTurn; i++ {
		g.msgs = append(g.msgs, agentgraph.Message{
			Role:    "tool",
			Name:    "bash",
			Content: fmt.Sprintf("tool output chunk %d %s", g.appends, strings.Repeat("output ", 40)),
		})
		g.appends++
	}
	out := make([]agentgraph.Message, len(g.msgs))
	copy(out, g.msgs)
	return out, nil
}

// compact trims the transcript to its tail, standing in for whatever
// strategy the real pipeline resolves to.
func (g *growingTranscript) compact(keep int) []agentgraph.Message {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.compacts++
	if len(g.msgs) > keep {
		g.msgs = append([]agentgraph.Message(nil), g.msgs[len(g.msgs)-keep:]...)
	}
	out := make([]agentgraph.Message, len(g.msgs))
	copy(out, g.msgs)
	return out
}

func (g *growingTranscript) compactCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.compacts
}

// windowedLLM fails the way a provider does when the request exceeds the
// model's context window. This is the wall a long turn hits today.
type windowedLLM struct {
	mu       sync.Mutex
	window   int
	calls    int
	overflow bool
	seen     []agentgraph.LLMRequest
}

func (f *windowedLLM) Generate(_ context.Context, req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.seen = append(f.seen, req)
	size := 0
	for _, m := range req.Messages {
		size += len(m.Content)
	}
	if size > f.window {
		f.overflow = true
		return agentgraph.LLMResponse{}, fmt.Errorf(
			"context overflow: request is %d bytes, model window is %d", size, f.window)
	}
	return agentgraph.LLMResponse{Content: "ok"}, nil
}

func (f *windowedLLM) snapshot() (calls int, overflowed bool, seen []agentgraph.LLMRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agentgraph.LLMRequest, len(f.seen))
	copy(out, f.seen)
	return f.calls, f.overflow, out
}

// trimmingCompactor is the Compactor seam backed by the transcript.
type trimmingCompactor struct {
	transcript *growingTranscript
	keep       int

	mu    sync.Mutex
	calls []agentgraph.CompactionInput
}

func (c *trimmingCompactor) Compact(_ context.Context, in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
	c.mu.Lock()
	c.calls = append(c.calls, in)
	c.mu.Unlock()
	return agentgraph.CompactionOutput{Messages: c.transcript.compact(c.keep)}, nil
}

func (c *trimmingCompactor) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// longTurnGraph is a loop whose body is a model node — the shape of the
// chat graph's assistant turn, reduced to the part this mission governs.
func longTurnGraph(iterations int) *agentgraph.Graph {
	return &agentgraph.Graph{
		ID:          "g-long-turn",
		SpecVersion: "1",
		Entrypoints: []string{"loop"},
		Nodes: []agentgraph.Node{
			{ID: "loop", Kind: agentgraph.NodeKindLoop, Attrs: agentgraph.LoopAttrs{
				MaxIterations: iterations, Body: []string{"llm1"},
			}},
			{ID: "llm1", Kind: agentgraph.NodeKindModel, Attrs: agentgraph.ModelAttrs{
				Provider: "test", Model: "m", MaxTokens: 512,
			}},
		},
	}
}

// TestTurnRunway_LongTurnOverflowsUnderSuppression is the control arm:
// the pre-mission configuration. With the automatic sites welded shut
// for the whole turn, a run that accumulates context has no way to
// shed it and dies at the context window.
//
// This test is the "before" this mission exists to fix. If it ever
// starts passing, the fixture stopped modelling the problem.
func TestTurnRunway_LongTurnOverflowsWithNoMidRunCompaction(t *testing.T) {
	transcript := newGrowingTranscript(20, 3)
	compactor := &trimmingCompactor{transcript: transcript, keep: 12}
	llm := &windowedLLM{window: 8_000}

	// The "before" arm used to be expressed with the Env's hard
	// suppression boolean, which agentgraph-total-convergence-01PMGX01
	// WP08 deleted (spec §6 I4 forbids the symbol). A kernel built
	// without a compactor models the same condition and models it more
	// directly: this is a run with no way to shed context, which is
	// what the pre-mission chat turn was after its single pre-send
	// pass. The compactor is constructed but deliberately not wired, so
	// the zero-calls assertion below still has something to count.
	k := agentgraph.NewKernel()
	env := &agentgraph.Env{
		RunID:     "r-long-no-compactor",
		SessionID: "s-long-no-compactor",
		Graph:     longTurnGraph(15),
		LLM:       llm,
		History:   agentgraph.HistoryReaderFunc(transcript.History),
		State:     agentgraph.NewRunState(),
	}
	err := k.Run(context.Background(), env)
	if err == nil {
		t.Fatalf("the uncompactable long turn completed; the fixture no longer models the pre-mission wall")
	}
	_, overflowed, _ := llm.snapshot()
	if !overflowed {
		t.Fatalf("run failed for a reason other than context overflow: %v", err)
	}
	if got := compactor.callCount(); got != 0 {
		t.Fatalf("an unwired compactor was somehow called %d times", got)
	}
}

// TestTurnRunway_LongTurnCompactsMidRunAndCompletes is the acceptance
// case (FR-001): the identical fixture, with a watermark armed instead
// of the suppression boolean, compacts mid-run and runs to completion.
func TestTurnRunway_LongTurnCompactsMidRunAndCompletes(t *testing.T) {
	transcript := newGrowingTranscript(20, 3)
	compactor := &trimmingCompactor{transcript: transcript, keep: 12}
	llm := &windowedLLM{window: 8_000}

	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:     "r-long-watermark",
		SessionID: "s-long-watermark",
		Graph:     longTurnGraph(15),
		LLM:       llm,
		History:   agentgraph.HistoryReaderFunc(transcript.History),
		State:     agentgraph.NewRunState(),

		AutoCompaction: agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
			MarginTokens:   400,
			MarginFraction: -1, // absolute-only, so the fixture is deterministic
		}),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("the long turn still died: %v — a turn that accumulates context must be able to compact itself", err)
	}
	calls, overflowed, _ := llm.snapshot()
	if overflowed {
		t.Fatalf("the run overflowed even though it completed")
	}
	if calls != 15 {
		t.Fatalf("model calls = %d, want 15 (the full loop)", calls)
	}
	if got := compactor.callCount(); got == 0 {
		t.Fatalf("no compaction fired mid-run; the turn only survived by accident")
	}
	// The watermark must not turn into "compact on every call" — that
	// is what Rearm exists to prevent.
	if got := compactor.callCount(); got >= calls {
		t.Fatalf("compaction fired %d times across %d model calls; the watermark is not re-baselining", got, calls)
	}
	t.Logf("long turn completed: %d model calls, %d mid-run compactions", calls, compactor.callCount())
}

// TestTurnRunway_RearmPreventsCompactingEveryCall pins the re-baseline
// that keeps mid-run compaction affordable.
//
// The failure mode it guards is specific and easy to miss: compaction
// does not always shrink a transcript back under its original baseline
// — a strategy that halves a transcript which has since quadrupled
// leaves it well above where the run started. Without Rearm the
// watermark keeps comparing against that stale, now-much-smaller
// baseline, so EVERY subsequent site clears the threshold and the turn
// pays for a real LLM compaction call on every single model call.
//
// The fixture is sized so that the post-compaction transcript stays
// above the original baseline plus margin, which is exactly the
// condition the naive version gets wrong. The earlier long-turn test
// cannot catch this: its compactor trims hard enough to fall back under
// the threshold on its own, so both behaviours look alike there.
func TestTurnRunway_RearmPreventsCompactingEveryCall(t *testing.T) {
	const iterations = 15
	transcript := newGrowingTranscript(10, 3)
	// keep=30 is deliberately gentle: far above the 10-message baseline
	// the run latches on its first call.
	compactor := &trimmingCompactor{transcript: transcript, keep: 30}
	llm := &windowedLLM{window: 10_000_000} // isolate re-baselining from overflow

	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:     "r-rearm",
		SessionID: "s-rearm",
		Graph:     longTurnGraph(iterations),
		LLM:       llm,
		History:   agentgraph.HistoryReaderFunc(transcript.History),
		State:     agentgraph.NewRunState(),
		AutoCompaction: agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
			MarginTokens:   1000,
			MarginFraction: -1,
		}),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls, _, _ := llm.snapshot()
	if calls != iterations {
		t.Fatalf("model calls = %d, want %d", calls, iterations)
	}
	fires := compactor.callCount()
	if fires == 0 {
		t.Fatalf("no compaction fired; the fixture no longer exercises the watermark")
	}
	// With Rearm the run re-baselines after each compaction and has to
	// re-accumulate the margin, so it fires a handful of times. Without
	// it, every call after the first crossing fires.
	if fires > iterations/2 {
		t.Fatalf("compaction fired %d times across %d model calls — the watermark is not re-baselining after it fires, so a long turn pays for a compaction LLM call on nearly every model call",
			fires, calls)
	}
}

// TestTurnRunway_ShortTurnIsByteIdenticalToPreMission is FR-004,
// asserted as a direct A/B rather than as a proxy: the same short-turn
// fixture is run twice — once on a kernel that cannot compact at all,
// once with the mission's watermark armed — and every LLMRequest the
// two runs produce must be byte-identical, with zero automatic
// compaction calls on both sides.
//
// This is the guarantee that makes the watermark a safe replacement: a
// turn that never approaches the limit pays nothing and sees exactly
// the prompts it saw before.
//
// The "before" arm was originally expressed with the Env's hard
// suppression boolean; agentgraph-total-convergence-01PMGX01 WP08
// deleted that field, so the arm is now "no compactor wired". The
// comparison is unchanged in substance — both arms are runs in which no
// automatic compaction can occur — and is arguably stronger, since the
// baseline is now the genuine no-compaction path rather than a flag
// that suppressed one.
func TestTurnRunway_ShortTurnIsByteIdenticalToPreMission(t *testing.T) {
	// Deterministic short-turn fixture: a fixed transcript, no growth,
	// and a window large enough that nothing is ever at risk.
	build := func(runID string, wireCompactor bool, mutate func(*agentgraph.Env)) (*windowedLLM, *trimmingCompactor) {
		transcript := newGrowingTranscript(6, 0)
		compactor := &trimmingCompactor{transcript: transcript, keep: 4}
		llm := &windowedLLM{window: 10_000_000}
		var k *agentgraph.Kernel
		if wireCompactor {
			k = agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
		} else {
			k = agentgraph.NewKernel()
		}
		env := &agentgraph.Env{
			RunID:     runID,
			SessionID: "s-short",
			Graph:     longTurnGraph(3),
			LLM:       llm,
			History:   agentgraph.HistoryReaderFunc(transcript.History),
			State:     agentgraph.NewRunState(),
		}
		mutate(env)
		if err := k.Run(context.Background(), env); err != nil {
			t.Fatalf("%s: Run: %v", runID, err)
		}
		return llm, compactor
	}

	preLLM, preCompactor := build("r-short-premission", false, func(*agentgraph.Env) {})
	postLLM, postCompactor := build("r-short-watermark", true, func(env *agentgraph.Env) {
		env.AutoCompaction = agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{})
	})

	if got := preCompactor.callCount(); got != 0 {
		t.Fatalf("pre-mission arm made %d automatic compaction calls, want 0", got)
	}
	if got := postCompactor.callCount(); got != 0 {
		t.Fatalf("watermark arm made %d automatic compaction calls on a short turn, want 0 — FR-004 says a short turn pays nothing",
			got)
	}

	_, _, preSeen := preLLM.snapshot()
	_, _, postSeen := postLLM.snapshot()
	if len(preSeen) != len(postSeen) {
		t.Fatalf("model call count changed: pre-mission %d, watermark %d", len(preSeen), len(postSeen))
	}
	for i := range preSeen {
		if got, want := len(postSeen[i].Messages), len(preSeen[i].Messages); got != want {
			t.Fatalf("request %d: message count %d, want %d", i, got, want)
		}
		for j := range preSeen[i].Messages {
			if !reflect.DeepEqual(postSeen[i].Messages[j], preSeen[i].Messages[j]) {
				t.Fatalf("request %d message %d differs from pre-mission:\n got %+v\nwant %+v",
					i, j, postSeen[i].Messages[j], preSeen[i].Messages[j])
			}
		}
		if postSeen[i].SystemPrompt != preSeen[i].SystemPrompt {
			t.Fatalf("request %d system prompt differs from pre-mission", i)
		}
	}
}

// TestTurnRunway_ToolOutputCapCutsTheAccumulationRate is the WP01 half
// of the integration story: the cheapest lever in the mission is not
// compacting the context, it is never letting the bytes in. A single
// oversized tool result is bounded before it becomes a Message, so the
// turn's growth rate — and therefore how often WP02 has to fire — drops
// by orders of magnitude.
func TestTurnRunway_ToolOutputCapCutsTheAccumulationRate(t *testing.T) {
	payload := strings.Repeat("z", 4<<20) // 4MB from one tool call

	uncapped := agentgraph.ToolResultCap{MaxBytes: -1}
	capped := agentgraph.ToolResultCap{}

	out, elided := uncapped.Apply(payload, "h")
	if elided != 0 || len(out) != len(payload) {
		t.Fatalf("uncapped policy altered the payload")
	}

	out, elided = capped.Apply(payload, "handle-1")
	if elided == 0 {
		t.Fatalf("default cap did not bound a 4MB result")
	}
	if len(out) > agentgraph.DefaultToolResultMaxBytes+1024 {
		t.Fatalf("bounded result is %d bytes, want <= cap(%d) + marker",
			len(out), agentgraph.DefaultToolResultMaxBytes)
	}
	if ratio := float64(len(payload)) / float64(len(out)); ratio < 50 {
		t.Fatalf("cap only shrank the result %.1fx; the accumulation rate is barely changed", ratio)
	}
	if !strings.Contains(out, "handle-1") {
		t.Fatalf("the model is not told where the elided bytes went")
	}
}
