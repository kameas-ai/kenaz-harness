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

// TestKernel_TaskStateSurvivesRebuildState exercises the WP03
// persistence contract end to end: SetTaskGoal / AddTaskCompletedStep
// / AddTaskForbidden write durable events, and a fresh Env rebuilt via
// RebuildState (as Kernel.Resume does) sees the same TaskState content
// without ever re-firing a node.
func TestKernel_TaskStateSurvivesRebuildState(t *testing.T) {
	t.Parallel()
	g := threeNodeChain()
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "ts-rb", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if err := k.SetTaskGoal(env, "ship the report"); err != nil {
		t.Fatalf("SetTaskGoal: %v", err)
	}
	if err := k.AddTaskCompletedStep(env, "outline drafted"); err != nil {
		t.Fatalf("AddTaskCompletedStep: %v", err)
	}
	if err := k.AddTaskForbidden(env, "delete_file", "bash"); err != nil {
		t.Fatalf("AddTaskForbidden: %v", err)
	}

	env2 := &Env{RunID: "ts-rb", Graph: g}
	applyEnvDefaults(env2)
	if err := k.RebuildState(env2); err != nil {
		t.Fatalf("RebuildState: %v", err)
	}

	if got := env2.TaskState.Goal(); got != "ship the report" {
		t.Errorf("rebuilt Goal() = %q, want %q", got, "ship the report")
	}
	steps, elided := env2.TaskState.CompletedSteps()
	if elided != 0 || len(steps) != 1 || steps[0] != "outline drafted" {
		t.Errorf("rebuilt CompletedSteps() = %v, %d, want [\"outline drafted\"], 0", steps, elided)
	}
	forbidden := env2.TaskState.ForbiddenActions()
	if len(forbidden) != 2 || forbidden[0] != "bash" || forbidden[1] != "delete_file" {
		t.Errorf("rebuilt ForbiddenActions() = %v, want [bash delete_file]", forbidden)
	}
}

// TestKernel_FailureAnnotationsSurviveRebuildState pins the WP01 gap
// this WP03 change fixes: before, EventBacktrackFired was never
// replayed by RebuildState, so a resumed run silently lost every
// backtrack's rejection memory. It also doubles as the "FailedAttempts
// survives resume" half of the WP03 contract (FailedAttempt is a type
// alias for FailureAnnotation — see task_state.go).
func TestKernel_FailureAnnotationsSurviveRebuildState(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_resume")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-resume-graph", Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "sink", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "review", Port: "value"}, To: EndpointRef{Node: "sink", Port: "in"}},
		},
	}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	log := NewMemoryEventLog()
	k := NewKernel(WithExecutor(ex), WithEventLog(log))
	env := &Env{RunID: "bt-resume-run", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	original := env.State.FailureAnnotations()
	if len(original) != 1 {
		t.Fatalf("original failure annotations = %d, want 1", len(original))
	}

	env2 := &Env{RunID: "bt-resume-run", Graph: g}
	applyEnvDefaults(env2)
	if err := k.RebuildState(env2); err != nil {
		t.Fatalf("RebuildState: %v", err)
	}
	rebuilt := env2.State.FailureAnnotations()
	if len(rebuilt) != 1 {
		t.Fatalf("rebuilt failure annotations = %d, want 1", len(rebuilt))
	}
	if rebuilt[0].Node != original[0].Node || rebuilt[0].Reason != original[0].Reason ||
		rebuilt[0].RejectedApproach != original[0].RejectedApproach || rebuilt[0].Iteration != original[0].Iteration {
		t.Errorf("rebuilt annotation = %+v, want %+v (Timestamp excluded — see RebuildState doc)", rebuilt[0], original[0])
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
	// Should have produced 2 hook writes: pre-LLM (WP06) + post-LLM.
	if mem.writeCount() != 2 {
		t.Errorf("memory hook writes = %d (want 2 = pre+post-llm)", mem.writeCount())
	}
}

// fakeBacktrackExecutor is a minimal Executor used to exercise the
// kernel backtrack primitive (autonomy-recovery-runtime-01PMDL03
// WP01): it counts fires per node ID and, on the reviewer node's
// first fire only, returns a BacktrackRequest targeting an upstream
// node before behaving normally on every subsequent fire.
type fakeBacktrackExecutor struct {
	kind NodeKind

	mu       sync.Mutex
	calls    map[string]int
	reviewer string
	target   string
}

func (e *fakeBacktrackExecutor) Kind() NodeKind { return e.kind }

func (e *fakeBacktrackExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	e.mu.Lock()
	e.calls[node.ID]++
	n := e.calls[node.ID]
	e.mu.Unlock()

	r := NewResult()
	r.Outputs["value"] = node.ID
	if node.ID == e.reviewer && n == 1 {
		r.Backtrack = &BacktrackRequest{
			TargetNode:       e.target,
			Reason:           "rejected",
			RejectedApproach: "v1",
		}
	}
	return r, nil
}

func (e *fakeBacktrackExecutor) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[id]
}

func TestKernel_BacktrackRewindsAndRefires(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-graph", Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "sink", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "review", Port: "value"}, To: EndpointRef{Node: "sink", Port: "in"}},
		},
	}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-run", Graph: g}
	applyEnvDefaults(env)

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := ex.callCount("draft"); got != 2 {
		t.Errorf("draft calls = %d, want 2", got)
	}
	if got := ex.callCount("review"); got != 2 {
		t.Errorf("review calls = %d, want 2", got)
	}
	if got := ex.callCount("sink"); got != 1 {
		t.Errorf("sink calls = %d, want 1", got)
	}
	if !env.State.Completed("draft") || !env.State.Completed("review") || !env.State.Completed("sink") {
		t.Errorf("expected all completed: draft=%v review=%v sink=%v",
			env.State.Completed("draft"), env.State.Completed("review"), env.State.Completed("sink"))
	}

	anns := env.State.FailureAnnotations()
	if len(anns) != 1 {
		t.Fatalf("failure annotations = %d, want 1", len(anns))
	}
	if anns[0].Node != "draft" || anns[0].Reason != "rejected" || anns[0].RejectedApproach != "v1" || anns[0].Iteration != 1 {
		t.Errorf("unexpected annotation: %+v", anns[0])
	}
	if got := env.Counters.BacktracksSnapshot(); got != 1 {
		t.Errorf("BacktracksSnapshot = %d, want 1", got)
	}
}

// alwaysBacktrackExecutor models a reviewer that never approves —
// every fire of the reviewer node requests a rewind to its upstream
// target, exercising the MaxBacktracksPerRun cap (an unbounded rewind
// must halt the run with ErrBudgetExceeded rather than looping
// forever).
type alwaysBacktrackExecutor struct {
	kind NodeKind

	mu       sync.Mutex
	calls    map[string]int
	reviewer string
	target   string
}

func (e *alwaysBacktrackExecutor) Kind() NodeKind { return e.kind }

func (e *alwaysBacktrackExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	e.mu.Lock()
	e.calls[node.ID]++
	e.mu.Unlock()

	r := NewResult()
	r.Outputs["value"] = node.ID
	if node.ID == e.reviewer {
		r.Backtrack = &BacktrackRequest{TargetNode: e.target, Reason: "never good enough"}
	}
	return r, nil
}

func TestKernel_BacktrackBudgetCapHaltsInfiniteRewind(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_loop")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-loop-graph", Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
		},
	}
	ex := &alwaysBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "draft"}
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-loop-run", Graph: g, Budget: Budget{MaxBacktracksPerRun: 2}}
	applyEnvDefaults(env)

	err := k.Run(context.Background(), env)
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("err = %v, want ErrBudgetExceeded", err)
	}
	if got := env.Counters.BacktracksSnapshot(); got != 3 {
		t.Errorf("BacktracksSnapshot = %d, want 3 (2 honored + 1 that trips the cap)", got)
	}
}

func TestKernel_BacktrackRejectsUnknownTarget(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_badtarget")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-badtarget-graph", Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
		},
	}
	ex := &fakeBacktrackExecutor{kind: kind, calls: map[string]int{}, reviewer: "review", target: "does-not-exist"}
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-badtarget-run", Graph: g}
	applyEnvDefaults(env)

	err := k.Run(context.Background(), env)
	if err == nil {
		t.Fatal("expected an error for an unknown backtrack target")
	}
	if errors.Is(err, ErrBudgetExceeded) || errors.Is(err, ErrPaused) {
		t.Errorf("unexpected sentinel error: %v", err)
	}
}
