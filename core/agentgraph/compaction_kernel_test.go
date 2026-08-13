package agentgraph_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// recordingCompactor implements agentgraph.Compactor and records every
// invocation site the kernel walks through. Tests assert that pre_call
// and post_tool fire from the right executors with the right scope.
type recordingCompactor struct {
	calls []agentgraph.CompactionInput
	// transform, when non-nil, replaces the messages in the response.
	transform func([]agentgraph.Message) []agentgraph.Message
}

func (c *recordingCompactor) Compact(_ context.Context, in agentgraph.CompactionInput) (agentgraph.CompactionOutput, error) {
	c.calls = append(c.calls, in)
	out := in.Messages
	if c.transform != nil {
		out = c.transform(in.Messages)
	}
	return agentgraph.CompactionOutput{Messages: out}, nil
}

func TestKernel_FiresPreCallCompactionOnLLMNode(t *testing.T) {
	compactor := &recordingCompactor{
		transform: func(msgs []agentgraph.Message) []agentgraph.Message {
			// Drop the oldest. Asserts the executor honors the
			// compactor's returned messages.
			if len(msgs) == 0 {
				return msgs
			}
			return msgs[1:]
		},
	}
	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g1",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 100,
				},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:     "r1",
		SessionID: "s1",
		Graph:     graph,
		LLM:       llm,
	}
	// Seed inputs onto the entrypoint so the executor has messages to
	// pass through compaction.
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 1 {
		t.Fatalf("expected 1 compaction call, got %d", len(compactor.calls))
	}
	c := compactor.calls[0]
	if c.Site != agentgraph.CompactionSitePreCall {
		t.Fatalf("site = %q", c.Site)
	}
	if c.NodeID != "llm1" {
		t.Fatalf("node id = %q", c.NodeID)
	}
	if c.RunID != "r1" || c.SessionID != "s1" {
		t.Fatalf("scope mis-stamped: run=%q session=%q", c.RunID, c.SessionID)
	}
}

func TestKernel_FiresPostToolCompactionOnToolNode(t *testing.T) {
	compactor := &recordingCompactor{
		transform: func(msgs []agentgraph.Message) []agentgraph.Message {
			// Replace tool result with a one-line summary.
			if len(msgs) == 0 {
				return msgs
			}
			return []agentgraph.Message{{
				Role: "tool", Name: msgs[0].Name, Content: "[truncated]",
			}}
		},
	}
	tools := &fakeTools{result: agentgraph.ToolResult{
		Content: strings.Repeat("x", 32*1024), // big result
	}}
	graph := &agentgraph.Graph{
		ID:          "g2",
		SpecVersion: "1",
		Entrypoints: []string{"t1"},
		Nodes: []agentgraph.Node{
			{
				ID:    "t1",
				Kind:  agentgraph.NodeKindSubagentDispatch,
				Attrs: agentgraph.SubagentDispatchAttrs{Profile: "explore", Prompt: "go"},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:     "r2",
		SessionID: "s2",
		Graph:     graph,
		Tools:     tools,
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 1 {
		t.Fatalf("expected 1 compaction call, got %d", len(compactor.calls))
	}
	c := compactor.calls[0]
	if c.Site != agentgraph.CompactionSitePostTool {
		t.Fatalf("site = %q", c.Site)
	}
	if c.NodeID != "t1" {
		t.Fatalf("node id = %q", c.NodeID)
	}
	if len(c.Messages) != 1 || c.Messages[0].Role != "tool" {
		t.Fatalf("expected single tool-role input message, got %+v", c.Messages)
	}
}

// The two tests below are the compaction-convergence-01PMDL05
// single-fire proofs, REWRITTEN by
// agentgraph-total-convergence-01PMGX01 WP08.
//
// WHAT THEY PINNED, AND STILL PIN. 01PMDL05 found a real double-fire: a
// chat turn was compacted by a pre-kernel pass and then compacted again
// by the kernel's first automatic pre_call site, against the transcript
// the first pass had just produced — two real compaction calls, twice
// the aggressiveness the user asked for, for no benefit. The guarantee
// these tests exist to defend is "an automatic site does not fire
// against a transcript that was just compacted." That guarantee is
// unchanged and these tests still enforce it.
//
// WHAT CHANGED: THE MECHANISM. The original proofs set
// a boolean on the Env that welded both automatic sites shut for the
// whole run. WP08 deleted that field (spec §6 I4 grep-forbids the
// symbol), because welding the sites shut is also what made a chat turn
// uncompactable after its first pass: it compacted once and then grew
// monotonically through up to 25 model turns. The guarantee now comes
// from Env.AutoCompaction, the growth watermark
// (turn-context-runway-01PMAG03 WP02): the first observation latches
// the run's baseline and is refused by construction, and later sites
// are admitted only once the transcript has genuinely grown past it.
//
// So the assertion is the same — zero compaction calls on a run whose
// context has not grown — and it is now obtained from a policy that
// permits the later firing the boolean forbade. The contrast partner is
// still TestKernel_FiresPreCallCompactionOnLLMNode, which leaves
// AutoCompaction nil (no watermark, no gate: the correct reading for a
// graph-authored run with nothing compacting in front of it) and
// asserts exactly one call.

// TestKernel_ArmedWatermark_RefusesFirstPreCallSite is the pre_call
// single-fire proof. Same graph and compactor as
// TestKernel_FiresPreCallCompactionOnLLMNode, which asserts one call
// with no watermark armed; arming one must yield zero, because the
// first pre_call visit is the observation that establishes the
// baseline and therefore cannot have grown past it.
func TestKernel_ArmedWatermark_RefusesFirstPreCallSite(t *testing.T) {
	compactor := &recordingCompactor{}
	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g-watermark-precall",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{
				ID:   "llm1",
				Kind: agentgraph.NodeKindModel,
				Attrs: agentgraph.ModelAttrs{
					Provider: "test", Model: "m", MaxTokens: 100,
				},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:     "r-watermark-precall",
		SessionID: "s-watermark-precall",
		Graph:     graph,
		LLM:       llm,
		// The zero policy selects the package defaults, which is what
		// the chat surface arms on every run.
		AutoCompaction: agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{}),
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 0 {
		t.Fatalf("expected 0 compaction calls on the first pre_call site of a watermarked run, got %d: %+v",
			len(compactor.calls), compactor.calls)
	}
	// The baseline must actually have been latched — a watermark that
	// refused because it never observed anything would pass the
	// assertion above for the wrong reason, and would then admit the
	// next site unconditionally.
	if _, latched := env.AutoCompaction.Baseline(); !latched {
		t.Fatalf("pre_call site did not latch the watermark baseline")
	}
}

// TestKernel_ArmedWatermark_RefusesPostToolSiteBeforeCrossing is the
// post_tool counterpart. The post_tool site sees one tool result rather
// than the live transcript, so it cannot evaluate a baseline of its own
// and rides the pre_call site's verdict instead. On a run that has not
// crossed — including this one, which never reaches a pre_call site at
// all — that verdict is "no".
func TestKernel_ArmedWatermark_RefusesPostToolSiteBeforeCrossing(t *testing.T) {
	compactor := &recordingCompactor{}
	tools := &fakeTools{result: agentgraph.ToolResult{
		Content: strings.Repeat("x", 32*1024), // big result — would trigger post_tool if ungated
	}}
	graph := &agentgraph.Graph{
		ID:          "g-watermark-posttool",
		SpecVersion: "1",
		Entrypoints: []string{"t1"},
		Nodes: []agentgraph.Node{
			{
				ID:    "t1",
				Kind:  agentgraph.NodeKindSubagentDispatch,
				Attrs: agentgraph.SubagentDispatchAttrs{Profile: "explore", Prompt: "go"},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:          "r-watermark-posttool",
		SessionID:      "s-watermark-posttool",
		Graph:          graph,
		Tools:          tools,
		AutoCompaction: agentgraph.NewCompactionWatermark(agentgraph.CompactionWatermarkPolicy{}),
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 0 {
		t.Fatalf("expected 0 compaction calls at post_tool on an uncrossed watermarked run, got %d: %+v",
			len(compactor.calls), compactor.calls)
	}
}

func TestKernel_NilCompactorIsNoOp(t *testing.T) {
	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g3",
		SpecVersion: "1",
		Entrypoints: []string{"llm1"},
		Nodes: []agentgraph.Node{
			{ID: "llm1", Kind: agentgraph.NodeKindModel, Attrs: agentgraph.ModelAttrs{Provider: "p", Model: "m"}},
		},
	}
	k := agentgraph.NewKernel()
	env := &agentgraph.Env{Graph: graph, RunID: "r", LLM: llm}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// no compactor => no panic, no error; just verify run completed.
}

// ---- minimal fakes (subset of tests already in the package) ----

type fakeLLM struct {
	resp string
	seen agentgraph.LLMRequest
}

func (f *fakeLLM) Generate(_ context.Context, req agentgraph.LLMRequest) (agentgraph.LLMResponse, error) {
	f.seen = req
	return agentgraph.LLMResponse{Content: f.resp}, nil
}

type fakeTools struct {
	result agentgraph.ToolResult
}

func (f *fakeTools) Has(name string) bool { return true }
func (f *fakeTools) Call(_ context.Context, _ agentgraph.ToolCall) (agentgraph.ToolResult, error) {
	return f.result, nil
}
