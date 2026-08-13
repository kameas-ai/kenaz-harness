package agentgraph_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// compaction_target_test.go closes compaction-convergence-01PMDL05
// WP02, folded into agentgraph-total-convergence-01PMGX01 WP08.
//
// THE BUG. A node's `max_tokens` is its *output* cap — how many tokens
// the model may generate. A compaction target is a *context* budget —
// how large the conversation handed to the model may be. They are
// unrelated quantities that happen to be measured in the same unit,
// which is exactly the kind of pair that gets conflated. It was:
// exec_compute.go set the pre-call compaction's TargetTokens from the
// node's MaxTokens, so a perfectly ordinary output cap of 512 or 4096
// made every real conversation read as wildly over its context budget
// the moment a Compactor was actually wired. It was invisible because
// no Compactor ever was.
//
// THE PIN. Both compaction sites a graph can reach are checked here
// against a node whose MaxTokens is small and whose real context window
// is large. The recorded CompactionInput must never carry MaxTokens as
// its TargetTokens, and must carry the context window it should be
// sizing against.

// targetRecorder is a Compactor that records the inputs it is handed
// and returns them unchanged.
//
// Race-safe by construction: the kernel may reach a compaction site
// from a tool_dispatch goroutine, and CI runs -race.
type targetRecorder struct {
	mu   sync.Mutex
	seen []agentgraph.CompactionInput
}

func (r *targetRecorder) Compact(_ context.Context, in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
	r.mu.Lock()
	r.seen = append(r.seen, in)
	r.mu.Unlock()
	return agentgraph.CompactionOutput{Messages: in.Messages, Skipped: true}, nil
}

func (r *targetRecorder) snapshot() []agentgraph.CompactionInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]agentgraph.CompactionInput, len(r.seen))
	copy(out, r.seen)
	return out
}

// fixedWindows is a ContextWindowSource with one answer, so the test
// can tell "the window" apart from "the output cap" by value.
type fixedWindows int

func (w fixedWindows) ContextWindow(_, _ string) int { return int(w) }

const (
	// Deliberately different orders of magnitude so a conflation
	// cannot pass by coincidence.
	testNodeOutputCap  = 256
	testContextWindow  = 200_000
	testHistoryLen     = 40
	testHistoryPadding = "filler conversation turn with enough text to matter"
)

// TestCompactNode_TargetTokensIsNotTheOutputCap covers the `compact`
// node — the site chat_default.yaml uses.
func TestCompactNode_TargetTokensIsNotTheOutputCap(t *testing.T) {
	rec := &targetRecorder{}
	history := make([]agentgraph.Message, 0, testHistoryLen)
	for i := 0; i < testHistoryLen; i++ {
		history = append(history, agentgraph.Message{Role: "user", Content: testHistoryPadding})
	}

	graph := &agentgraph.Graph{
		ID:          "g-compact-target",
		SpecVersion: "1",
		Entrypoints: []string{"hist"},
		Nodes: []agentgraph.Node{
			{ID: "hist", Kind: agentgraph.NodeKindHistoryRead, Attrs: agentgraph.HistoryReadAttrs{N: 0}},
			{
				ID:   "compact1",
				Kind: agentgraph.NodeKindCompact,
				Attrs: agentgraph.CompactAttrs{
					Strategy: "drop_oldest",
					Provider: "test",
					Model:    "m",
					// The trap: an output cap on the compaction node.
					MaxTokens: testNodeOutputCap,
				},
			},
		},
		Edges: []agentgraph.Edge{
			{
				From: agentgraph.EndpointRef{Node: "hist", Port: "messages"},
				To:   agentgraph.EndpointRef{Node: "compact1", Port: "input"},
			},
		},
	}

	k := agentgraph.NewKernel(agentgraph.WithCompactor(rec))
	env := &agentgraph.Env{
		RunID:          "r-compact-target",
		SessionID:      "s-compact-target",
		Graph:          graph,
		ContextWindows: fixedWindows(testContextWindow),
		History: agentgraph.HistoryReaderFunc(func(context.Context, string, int) ([]agentgraph.Message, error) {
			return history, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := rec.snapshot()
	if len(seen) != 1 {
		t.Fatalf("expected exactly 1 compaction call from the compact node, got %d", len(seen))
	}
	in := seen[0]
	if in.TargetTokens == testNodeOutputCap {
		t.Fatalf("compact node passed the node's output cap (%d) as the compaction TargetTokens — that is the compaction-convergence-01PMDL05 WP02 conflation",
			testNodeOutputCap)
	}
	if in.TargetTokens != 0 {
		t.Fatalf("compact node TargetTokens = %d, want 0 (no target_token_budget attr set, so the pipeline derives the target from the context window)",
			in.TargetTokens)
	}
	if in.ContextWindow != testContextWindow {
		t.Fatalf("compact node ContextWindow = %d, want %d — without the window the pipeline has no denominator and can only skip",
			in.ContextWindow, testContextWindow)
	}
	if in.SessionID != "s-compact-target" {
		t.Fatalf("compact node SessionID = %q, want the run's session — a session-scoped strategy cannot address history without it", in.SessionID)
	}
}

// TestCompactNode_ExplicitBudgetIsHonoured is the other half: when the
// author DOES set target_token_budget, that number must arrive intact.
// A fix that hardcoded TargetTokens to zero would pass the test above
// and silently ignore every author's explicit budget.
func TestCompactNode_ExplicitBudgetIsHonoured(t *testing.T) {
	const wantBudget = 12_345
	rec := &targetRecorder{}

	graph := &agentgraph.Graph{
		ID:          "g-compact-budget",
		SpecVersion: "1",
		Entrypoints: []string{"hist"},
		Nodes: []agentgraph.Node{
			{ID: "hist", Kind: agentgraph.NodeKindHistoryRead, Attrs: agentgraph.HistoryReadAttrs{N: 0}},
			{
				ID:   "compact1",
				Kind: agentgraph.NodeKindCompact,
				Attrs: agentgraph.CompactAttrs{
					Strategy:          "drop_oldest",
					MaxTokens:         testNodeOutputCap,
					TargetTokenBudget: wantBudget,
				},
			},
		},
		Edges: []agentgraph.Edge{
			{
				From: agentgraph.EndpointRef{Node: "hist", Port: "messages"},
				To:   agentgraph.EndpointRef{Node: "compact1", Port: "input"},
			},
		},
	}

	k := agentgraph.NewKernel(agentgraph.WithCompactor(rec))
	env := &agentgraph.Env{
		RunID:     "r-compact-budget",
		SessionID: "s-compact-budget",
		Graph:     graph,
		History: agentgraph.HistoryReaderFunc(func(context.Context, string, int) ([]agentgraph.Message, error) {
			return []agentgraph.Message{{Role: "user", Content: "hi"}}, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	seen := rec.snapshot()
	if len(seen) != 1 {
		t.Fatalf("expected 1 compaction call, got %d", len(seen))
	}
	if seen[0].TargetTokens != wantBudget {
		t.Fatalf("TargetTokens = %d, want the author's target_token_budget %d", seen[0].TargetTokens, wantBudget)
	}
}

// TestPreCallSite_TargetTokensIsNotTheOutputCap covers the automatic
// pre_call site on a model node, the other place the conflation lived.
//
// The watermark is armed and pre-crossed so the site is actually
// reached: a first-observation refusal would make this pass for the
// wrong reason.
func TestPreCallSite_TargetTokensIsNotTheOutputCap(t *testing.T) {
	rec := &targetRecorder{}
	watermark := agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{
		MarginTokens: 1, MarginFraction: -1,
	})
	watermark.Admit(0) // latch low so the model node's observation crosses

	graph := &agentgraph.Graph{
		ID:          "g-precall-target",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: testNodeOutputCap,
				},
			},
		},
	}
	llm := &fakeLLM{resp: "ok"}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(rec))
	env := &agentgraph.Env{
		RunID:          "r-precall-target",
		SessionID:      "s-precall-target",
		Graph:          graph,
		LLM:            llm,
		AutoCompaction: watermark,
		ContextWindows: fixedWindows(testContextWindow),
		History: agentgraph.HistoryReaderFunc(func(context.Context, string, int) ([]agentgraph.Message, error) {
			return []agentgraph.Message{{Role: "user", Content: strings.Repeat("y ", 5000)}}, nil
		}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	seen := rec.snapshot()
	if len(seen) == 0 {
		t.Fatalf("pre_call site never fired; the watermark or the gate is refusing, so this test proves nothing")
	}
	for i, in := range seen {
		if in.Site != agentgraph.CompactionSitePreCall {
			continue
		}
		if in.TargetTokens == testNodeOutputCap {
			t.Fatalf("call %d: pre_call passed the node's output cap (%d) as TargetTokens — the conflation is back", i, testNodeOutputCap)
		}
		if in.ContextWindow != testContextWindow {
			t.Fatalf("call %d: pre_call ContextWindow = %d, want %d", i, in.ContextWindow, testContextWindow)
		}
	}
}
