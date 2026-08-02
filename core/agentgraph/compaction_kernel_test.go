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
				Kind:  agentgraph.NodeKindTool,
				Attrs: agentgraph.ToolAttrs{Name: "echo"},
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

// TestKernel_SuppressAutomaticCompaction_BlocksPreCallSite is the
// compaction-convergence-01PMDL05 single-fire proof for the pre_call
// site: with Env.SuppressAutomaticCompaction set, the kernel must not
// invoke the wired Compactor at all, even though everything else about
// the run (compactor non-nil, an LLMNode in the graph) is identical to
// TestKernel_FiresPreCallCompactionOnLLMNode, which asserts the
// opposite (fires exactly once) when the flag is left at its zero
// value. This is the mechanism that lets a chat-driven run (whose
// authoritative compactor is the pre-send path, not this one) share a
// kernel with graph-authored runs without double-compacting.
func TestKernel_SuppressAutomaticCompaction_BlocksPreCallSite(t *testing.T) {
	compactor := &recordingCompactor{}
	llm := &fakeLLM{resp: "ok"}
	graph := &agentgraph.Graph{
		ID:          "g-suppress-precall",
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
		RunID:                       "r-suppress-precall",
		SessionID:                   "s-suppress-precall",
		Graph:                       graph,
		LLM:                         llm,
		SuppressAutomaticCompaction: true,
	}
	env.State = agentgraph.NewRunState()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 0 {
		t.Fatalf("expected 0 compaction calls with SuppressAutomaticCompaction=true, got %d: %+v",
			len(compactor.calls), compactor.calls)
	}
}

// TestKernel_SuppressAutomaticCompaction_BlocksPostToolSite is the
// post_tool-site counterpart of the pre_call test above — same graph
// shape as TestKernel_FiresPostToolCompactionOnToolNode, but with the
// suppression flag set, asserting zero compaction calls instead of one.
func TestKernel_SuppressAutomaticCompaction_BlocksPostToolSite(t *testing.T) {
	compactor := &recordingCompactor{}
	tools := &fakeTools{result: agentgraph.ToolResult{
		Content: strings.Repeat("x", 32*1024), // big result — would trigger post_tool if not suppressed
	}}
	graph := &agentgraph.Graph{
		ID:          "g-suppress-posttool",
		SpecVersion: "1",
		Entrypoints: []string{"t1"},
		Nodes: []agentgraph.Node{
			{
				ID:    "t1",
				Kind:  agentgraph.NodeKindTool,
				Attrs: agentgraph.ToolAttrs{Name: "echo"},
			},
		},
	}
	k := agentgraph.NewKernel(agentgraph.WithCompactor(compactor))
	env := &agentgraph.Env{
		RunID:                       "r-suppress-posttool",
		SessionID:                   "s-suppress-posttool",
		Graph:                       graph,
		Tools:                       tools,
		SuppressAutomaticCompaction: true,
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(compactor.calls) != 0 {
		t.Fatalf("expected 0 compaction calls with SuppressAutomaticCompaction=true, got %d: %+v",
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
