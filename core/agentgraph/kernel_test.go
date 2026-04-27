package agentgraph

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// trivial 3-node DAG: a -> b -> c, all transforms.
func threeNodeChain() *Graph {
	return &Graph{
		SpecVersion: SpecVersion,
		ID:          "chain",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
			{ID: "b", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
			{ID: "c", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "a", Port: "out"}, To: EndpointRef{Node: "b", Port: "in"}},
			{From: EndpointRef{Node: "b", Port: "out"}, To: EndpointRef{Node: "c", Port: "in"}},
		},
	}
}

func TestKernel_RunsLinearDAG(t *testing.T) {
	t.Parallel()
	g := threeNodeChain()
	k := NewKernel()
	env := &Env{RunID: "run-1", Graph: g, SessionID: "s"}
	applyEnvDefaults(env)
	env.State.SetOutputs("a", PortValues{"out": "hi"}) // pretend the entrypoint already has a hint
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// All three nodes should be marked completed.
	if !env.State.Completed("a") || !env.State.Completed("b") || !env.State.Completed("c") {
		t.Errorf("expected all completed: a=%v b=%v c=%v",
			env.State.Completed("a"), env.State.Completed("b"), env.State.Completed("c"))
	}
}

func TestKernel_PausesOnAsk(t *testing.T) {
	t.Parallel()
	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "ask-graph",
		Entrypoints: []string{"q"},
		Nodes: []Node{
			{ID: "q", Kind: NodeKindAsk, Attrs: AskAttrs{Question: "wait"}},
		},
	}
	bus := NewMemAskBus()
	k := NewKernel()
	env := &Env{RunID: "rr", Graph: g, Ask: bus}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); !errors.Is(err, ErrPaused) {
		t.Fatalf("expected ErrPaused, got %v", err)
	}
	if q, ok := bus.PendingQuestion("rr", "q"); !ok || q != "wait" {
		t.Errorf("pending question wrong: %q (%v)", q, ok)
	}
}

func TestKernel_ResumesAfterAnswer(t *testing.T) {
	t.Parallel()
	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "ask-graph",
		Entrypoints: []string{"q"},
		Nodes: []Node{
			{ID: "q", Kind: NodeKindAsk, Attrs: AskAttrs{Question: "wait"}},
		},
	}
	bus := NewMemAskBus()
	k := NewKernel()
	env := &Env{RunID: "rr2", Graph: g, Ask: bus}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); !errors.Is(err, ErrPaused) {
		t.Fatalf("first run: %v", err)
	}
	bus.Answer("rr2", "q", "yes")
	if err := k.Resume(context.Background(), env); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !env.State.Completed("q") {
		t.Errorf("after resume, q not completed")
	}
}

func TestKernel_BudgetCapHaltsRun(t *testing.T) {
	t.Parallel()
	// 1000-token responses; 2-node chain; cap at 100 tokens fires
	// before the 2nd node dispatches.
	llm := &stubLLM{responses: []LLMResponse{
		{Content: "first", TokensUsed: 1000},
		{Content: "second", TokensUsed: 1000},
	}}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "g", Entrypoints: []string{"l1"},
		Nodes: []Node{
			{ID: "l1", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x"}},
			{ID: "l2", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "l1", Port: "response"}, To: EndpointRef{Node: "l2", Port: "messages"}},
		},
	}
	k := NewKernel()
	env := &Env{
		RunID: "rcap", Graph: g, LLM: llm,
		Budget: Budget{MaxTokensPerRun: 100},
	}
	applyEnvDefaults(env)
	err := k.Run(context.Background(), env)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
}

func TestKernel_RecordsRunStartAndComplete(t *testing.T) {
	t.Parallel()
	g := threeNodeChain()
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "rs", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var sawStart, sawComplete bool
	_ = log.Replay("rs", func(e Event) error {
		if e.Kind == EventRunStart {
			sawStart = true
		}
		if e.Kind == EventRunComplete {
			sawComplete = true
		}
		return nil
	})
	if !sawStart || !sawComplete {
		t.Errorf("missing lifecycle events; start=%v complete=%v", sawStart, sawComplete)
	}
}

func TestKernel_ConcurrencyCap(t *testing.T) {
	t.Parallel()
	// 5 entrypoints fanning into a single sink; cap=2 should mean we
	// never see > 2 concurrent transforms.
	var maxConcurrent int
	var current int
	var mu sync.Mutex
	probeFn := func(_ context.Context, in PortValues, _ map[string]any) (PortValues, error) {
		mu.Lock()
		current++
		if current > maxConcurrent {
			maxConcurrent = current
		}
		mu.Unlock()
		// no real sleep — Go scheduler will interleave goroutines
		mu.Lock()
		current--
		mu.Unlock()
		return PortValues{"out": "x"}, nil
	}
	g := &Graph{
		SpecVersion: SpecVersion, ID: "g", Entrypoints: []string{"a", "b", "c", "d", "e"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "probe"}},
			{ID: "b", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "probe"}},
			{ID: "c", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "probe"}},
			{ID: "d", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "probe"}},
			{ID: "e", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "probe"}},
		},
	}
	k := NewKernel(WithMaxInFlight(2))
	env := &Env{RunID: "rcap", Graph: g}
	applyEnvDefaults(env)
	env.Transforms.Register("probe", probeFn)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if maxConcurrent > 2 {
		t.Errorf("concurrency cap violated: maxConcurrent = %d", maxConcurrent)
	}
}

func TestKernel_SkipsLoopBodiesAtTopLevel(t *testing.T) {
	t.Parallel()
	g := &Graph{
		SpecVersion: SpecVersion, ID: "g", Entrypoints: []string{"loop"},
		Nodes: []Node{
			{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
				MaxIterations: 2, Body: []string{"step"},
			}},
			{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
		},
	}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "rl", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// step should be a body node — no top-level fire records its
	// "node_start" with the kernel; it fires inside the loop's
	// Execute. Both `loop` and `step` produce node_complete events
	// because the loop forwards body-node events into its own batch.
	var loopComplete bool
	_ = log.Replay("rl", func(e Event) error {
		if e.Kind == EventNodeComplete && e.NodeID == "loop" {
			loopComplete = true
		}
		return nil
	})
	if !loopComplete {
		t.Error("loop did not complete")
	}
}

func TestKernel_RebuildStateFromEventLog(t *testing.T) {
	t.Parallel()
	g := threeNodeChain()
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "rb", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Now build a fresh env with empty state and rebuild from the log.
	env2 := &Env{RunID: "rb", Graph: g}
	applyEnvDefaults(env2)
	if err := k.RebuildState(env2); err != nil {
		t.Fatalf("RebuildState: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if !env2.State.Completed(id) {
			t.Errorf("rebuilt state missing completion for %s", id)
		}
	}
}

func TestKernel_RejectsMissingEntrypoint(t *testing.T) {
	t.Parallel()
	g := &Graph{SpecVersion: SpecVersion, ID: "g", Entrypoints: []string{"missing"}, Nodes: []Node{
		{ID: "x", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	k := NewKernel()
	env := &Env{RunID: "r", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err == nil {
		t.Fatalf("expected error for missing entrypoint")
	}
}

func TestKernel_RejectsNilGraph(t *testing.T) {
	t.Parallel()
	k := NewKernel()
	if err := k.Run(context.Background(), &Env{RunID: "r"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestKernel_LLMNodeRunsThroughKernel(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{responses: []LLMResponse{{Content: "answer", FinishReason: "stop", TokensUsed: 5}}}
	mem := newStubMemory()
	g := &Graph{
		SpecVersion: SpecVersion, ID: "g", Entrypoints: []string{"l"},
		Nodes: []Node{{ID: "l", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "x", MaxTokens: 50}}},
	}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "lr", Graph: g, LLM: llm, Memory: mem}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if env.Counters.LLMCallsMade != 1 {
		t.Errorf("LLMCallsMade = %d", env.Counters.LLMCallsMade)
	}
	// Should have produced 1 post-LLM hook write to memory.
	if mem.writeCount() != 1 {
		t.Errorf("memory hook writes = %d", mem.writeCount())
	}
}
