package agentgraph

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
	// `step` is a body node: the KERNEL never fires it (buildEdges
	// hides body nodes from the edge walk), so its lifecycle events
	// come from the loop's own Execute rather than from the kernel's
	// top-level fire path. Since WP12 that lifecycle is a full
	// node_start/node_complete pair carrying `iteration` + `container`
	// — the sentence that used to sit here said `step` had no
	// node_start at all, which was true before appendBodyFireStart and
	// is the exact blind spot the materializer needed closed.
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

// TestKernel_LadderStateSurvivesRebuildState pins the fix for a gap in
// RebuildState: before, EventLadderRung / EventEscalateTriggered were
// never replayed, so a resumed run silently reset every
// EscalationLadder node back to rung 0 (retry) and cleared the
// run-global escalation-used flag — a paused-and-resumed run could
// burn the shared escalation slot a second time, or re-pose a
// question the human had already answered before the pause.
//
// This drives a real escalationLadderExecutor through retry ->
// escalate -> replan -> ask, committing each Execute call's
// EventBatch to a real EventLog exactly as the kernel would, then
// rebuilds a fresh Env from that log and asserts both the per-node
// rung/retry_attempts and the shared global-escalation flag survive.
// It also exercises the ask/exhausted disambiguation: once the
// question is answered, RebuildState must reconstruct the exhausted
// rung, not the still-pending ask rung.
func TestKernel_LadderStateSurvivesRebuildState(t *testing.T) {
	t.Parallel()
	const runID = "ladder-rebuild-run"
	const nodeID = "ladder1"

	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))

	llm := &stubLLM{}
	env := &Env{RunID: runID, State: NewRunState(), LLM: llm}
	applyEnvDefaults(env)

	node := &Node{
		ID:   nodeID,
		Kind: NodeKindEscalationLadder,
		Attrs: EscalationLadderAttrs{
			TargetModel:  "strong-model",
			PlannerModel: "planner-model",
			UpstreamNode: "draft",
			MaxRetries:   1,
		},
	}
	ex := escalationLadderExecutor{}
	ctx := context.Background()

	commit := func(res Result) {
		t.Helper()
		if res.Events.Len() == 0 {
			return
		}
		if _, err := log.Append(res.Events); err != nil {
			t.Fatalf("log.Append: %v", err)
		}
	}

	// Fire 1: rung 0 (retry), retryAttempts 1 <= MaxRetries(1) -> requests
	// a backtrack and stays on the retry rung.
	res1, err := ex.Execute(ctx, env, node, PortValues{})
	if err != nil {
		t.Fatalf("Execute #1: %v", err)
	}
	if res1.Backtrack == nil {
		t.Fatalf("Execute #1: expected a Backtrack request")
	}
	commit(res1)

	// Fire 2: retryAttempts becomes 2 > MaxRetries(1) -> advances straight
	// into escalate, claims the global slot, and calls Generate.
	res2, err := ex.Execute(ctx, env, node, PortValues{})
	if err != nil {
		t.Fatalf("Execute #2: %v", err)
	}
	if res2.Outputs["rung"] != "escalate" {
		t.Fatalf("Execute #2: rung = %v, want escalate", res2.Outputs["rung"])
	}
	commit(res2)
	if fired, _ := env.State.Outputs(ladderGlobalEscalatedKey)["fired"].(bool); !fired {
		t.Fatalf("expected global escalation flag set after Execute #2")
	}

	// Fire 3: rung replan -> calls Generate, advances to ask.
	res3, err := ex.Execute(ctx, env, node, PortValues{})
	if err != nil {
		t.Fatalf("Execute #3: %v", err)
	}
	if res3.Outputs["rung"] != "replan" {
		t.Fatalf("Execute #3: rung = %v, want replan", res3.Outputs["rung"])
	}
	commit(res3)

	// Fire 4: rung ask, no answer yet -> pauses on the AskBus.
	res4, err := ex.Execute(ctx, env, node, PortValues{})
	if err != nil {
		t.Fatalf("Execute #4: %v", err)
	}
	if !res4.Pause {
		t.Fatalf("Execute #4: expected Pause=true")
	}
	commit(res4)

	// --- Round trip #1: mid-pause, still unanswered. ---
	env2 := &Env{RunID: runID}
	applyEnvDefaults(env2)
	if err := k.RebuildState(env2); err != nil {
		t.Fatalf("RebuildState (mid-pause): %v", err)
	}
	st2 := env2.State.Outputs(ladderStateKey(nodeID))
	if rung, _ := st2["rung"].(int); rung != ladderRungAsk {
		t.Errorf("rebuilt (mid-pause) rung = %v, want ladderRungAsk(%d)", rung, ladderRungAsk)
	}
	if fired, _ := env2.State.Outputs(ladderGlobalEscalatedKey)["fired"].(bool); !fired {
		t.Errorf("rebuilt (mid-pause) global escalation flag = false, want true")
	}

	// Answer the question and fire once more: rung ask -> exhausted.
	env.Ask.(*memAskBus).Answer(runID, nodeID, "proceed with plan B")
	res5, err := ex.Execute(ctx, env, node, PortValues{})
	if err != nil {
		t.Fatalf("Execute #5: %v", err)
	}
	if res5.Outputs["rung"] != "ask" {
		t.Fatalf("Execute #5: rung = %v, want ask", res5.Outputs["rung"])
	}
	commit(res5)

	// --- Round trip #2: post-answer, ladder exhausted. ---
	env3 := &Env{RunID: runID}
	applyEnvDefaults(env3)
	if err := k.RebuildState(env3); err != nil {
		t.Fatalf("RebuildState (post-answer): %v", err)
	}
	st3 := env3.State.Outputs(ladderStateKey(nodeID))
	if rung, _ := st3["rung"].(int); rung != ladderRungExhausted {
		t.Errorf("rebuilt (post-answer) rung = %v, want ladderRungExhausted(%d) — the answered ask must not replay as still-pending", rung, ladderRungExhausted)
	}
	if fired, _ := env3.State.Outputs(ladderGlobalEscalatedKey)["fired"].(bool); !fired {
		t.Errorf("rebuilt (post-answer) global escalation flag = false, want true")
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

// fanOutSiblingExecutor models a backtrack target ("draft") with two
// children: a reviewer that requests a rewind to draft on its first
// fire, and a sibling ("sib") that becomes ready via the very same
// fan-out and is deliberately held in flight (blocked on release)
// until the rewind's in-degree recompute has already run and attempted
// to re-promote it. This reproduces the PR #260 review's fan-out
// double-dispatch finding: an ordinary DAG shape (any node with 2+
// outgoing edges) where, without in-flight tracking, draft's second
// completion would fire "sib" a second, concurrent time while sib's
// first execution is still running.
type fanOutSiblingExecutor struct {
	kind     NodeKind
	target   string
	reviewer string
	sib      string

	mu        sync.Mutex
	calls     map[string]int
	active    int
	maxActive int
	release   chan struct{}

	sibFirstOnce     sync.Once
	sibFirst         chan struct{}
	reviewSecondOnce sync.Once
	reviewSecond     chan struct{}
}

func newFanOutSiblingExecutor(kind NodeKind, target, reviewer, sib string) *fanOutSiblingExecutor {
	return &fanOutSiblingExecutor{
		kind: kind, target: target, reviewer: reviewer, sib: sib,
		calls:        map[string]int{},
		release:      make(chan struct{}),
		sibFirst:     make(chan struct{}),
		reviewSecond: make(chan struct{}),
	}
}

func (e *fanOutSiblingExecutor) Kind() NodeKind { return e.kind }

func (e *fanOutSiblingExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	e.mu.Lock()
	e.calls[node.ID]++
	n := e.calls[node.ID]
	e.mu.Unlock()

	if node.ID == e.sib {
		e.mu.Lock()
		e.active++
		if e.active > e.maxActive {
			e.maxActive = e.active
		}
		e.mu.Unlock()
		if n == 1 {
			// Signal the test that sib's first fire has begun, then
			// block — held "in flight" (not yet returned from
			// Execute) across the moment the reviewer's backtrack
			// fires and recomputes in-degrees for the whole refire
			// set, including sib.
			e.sibFirstOnce.Do(func() { close(e.sibFirst) })
			<-e.release
		}
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	}

	if node.ID == e.reviewer && n == 2 {
		// review's second fire can only happen after draft's second
		// completion has run its promote-downstream section — which,
		// in the same critical section, already evaluated (and, if
		// the fix holds, deferred) sib. Safe checkpoint for the test
		// to inspect sib's call count while sib is still blocked.
		e.reviewSecondOnce.Do(func() { close(e.reviewSecond) })
	}

	r := NewResult()
	r.Outputs["value"] = node.ID
	if node.ID == e.reviewer && n == 1 {
		r.Backtrack = &BacktrackRequest{TargetNode: e.target, Reason: "rejected", RejectedApproach: "v1"}
	}
	return r, nil
}

func (e *fanOutSiblingExecutor) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[id]
}

func (e *fanOutSiblingExecutor) maxConcurrent() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxActive
}

// TestKernel_BacktrackFanOutSiblingNotDoubleDispatched is the required
// regression test for backtrack double-dispatch (PR #260 review, FIX
// 1, mode 1): a fan-out sibling of the backtrack target must never be
// dispatched a second, concurrent time while its first execution is
// still in flight.
func TestKernel_BacktrackFanOutSiblingNotDoubleDispatched(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_fanout")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-fanout-graph", Entrypoints: []string{"draft"},
		Nodes: []Node{
			{ID: "draft", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "sib", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "sib", Port: "in"}},
		},
	}
	ex := newFanOutSiblingExecutor(kind, "draft", "review", "sib")
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-fanout-run", Graph: g}
	applyEnvDefaults(env)

	runErr := make(chan error, 1)
	go func() { runErr <- k.Run(context.Background(), env) }()

	select {
	case <-ex.sibFirst:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sib's first fire")
	}
	select {
	case <-ex.reviewSecond:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for review's second fire — draft never refired")
	}

	// draft has already refired and run its promote-downstream section
	// for both review (fired again above) and sib in the very same
	// critical section, while sib's first fire is still blocked on
	// release. Assert the kernel has not launched a second dispatch of
	// sib here — the exact double-dispatch race window the review
	// flagged.
	if got := ex.callCount("sib"); got != 1 {
		t.Fatalf("sib was dispatched %d times before its first execution finished — double-dispatch", got)
	}

	close(ex.release)

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish after releasing sib")
	}

	if got := ex.callCount("sib"); got != 2 {
		t.Errorf("sib calls = %d, want 2 (one before the rewind, one fresh fire after)", got)
	}
	if got := ex.maxConcurrent(); got > 1 {
		t.Errorf("sib had %d concurrent executions, want at most 1 (double-dispatch)", got)
	}
	if got := ex.callCount("draft"); got != 2 {
		t.Errorf("draft calls = %d, want 2", got)
	}
	if got := ex.callCount("review"); got != 2 {
		t.Errorf("review calls = %d, want 2", got)
	}
	if !env.State.Completed("draft") || !env.State.Completed("review") || !env.State.Completed("sib") {
		t.Errorf("expected all nodes to have fired: draft=%v review=%v sib=%v",
			env.State.Completed("draft"), env.State.Completed("review"), env.State.Completed("sib"))
	}
}

// droppedDependencyExecutor models a backtrack target ("draft") with a
// child ("z") that also depends on a sibling branch ("w") which is NOT
// reachable from draft and therefore falls outside the refire set. It
// reproduces the PR #260 review's dropped-dependency finding (FIX 1,
// mode 2): the in-degree recompute must not treat every edge from
// outside the refire set as already satisfied — only ones from nodes
// that have actually completed.
type droppedDependencyExecutor struct {
	kind      NodeKind
	target    string // draft
	reviewer  string // requests a rewind to target on its first fire
	sideSrc   string // w: independent branch, blocks until released
	dependent string // z: depends on both target and sideSrc

	mu                  sync.Mutex
	calls               map[string]int
	sideDone            bool
	firedDependentEarly bool
	release             chan struct{}

	sideStartedOnce  sync.Once
	sideStarted      chan struct{}
	reviewSecondOnce sync.Once
	reviewSecond     chan struct{}
}

func newDroppedDependencyExecutor(kind NodeKind, target, reviewer, sideSrc, dependent string) *droppedDependencyExecutor {
	return &droppedDependencyExecutor{
		kind: kind, target: target, reviewer: reviewer, sideSrc: sideSrc, dependent: dependent,
		calls:        map[string]int{},
		release:      make(chan struct{}),
		sideStarted:  make(chan struct{}),
		reviewSecond: make(chan struct{}),
	}
}

func (e *droppedDependencyExecutor) Kind() NodeKind { return e.kind }

func (e *droppedDependencyExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	e.mu.Lock()
	e.calls[node.ID]++
	n := e.calls[node.ID]
	e.mu.Unlock()

	if node.ID == e.sideSrc {
		e.sideStartedOnce.Do(func() { close(e.sideStarted) })
		<-e.release
		e.mu.Lock()
		e.sideDone = true
		e.mu.Unlock()
	}

	if node.ID == e.dependent {
		e.mu.Lock()
		if !e.sideDone {
			e.firedDependentEarly = true
		}
		e.mu.Unlock()
	}

	if node.ID == e.reviewer && n == 2 {
		e.reviewSecondOnce.Do(func() { close(e.reviewSecond) })
	}

	r := NewResult()
	r.Outputs["value"] = node.ID
	if node.ID == e.reviewer && n == 1 {
		r.Backtrack = &BacktrackRequest{TargetNode: e.target, Reason: "rejected", RejectedApproach: "v1"}
	}
	return r, nil
}

func (e *droppedDependencyExecutor) callCount(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls[id]
}

func (e *droppedDependencyExecutor) firedEarly() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.firedDependentEarly
}

// TestKernel_BacktrackDoesNotDropPendingOutsideDependency is the
// required regression test for the dropped-dependency mode of FIX 1: a
// refire-set member with an in-edge from outside the refire set that
// has not yet completed must not fire early with a missing input.
func TestKernel_BacktrackDoesNotDropPendingOutsideDependency(t *testing.T) {
	t.Parallel()
	kind := NodeKind("fake_backtrack_dropdep")
	g := &Graph{
		SpecVersion: SpecVersion, ID: "bt-dropdep-graph", Entrypoints: []string{"root"},
		Nodes: []Node{
			{ID: "root", Kind: kind},
			{ID: "draft", Kind: kind},
			{ID: "w", Kind: kind},
			{ID: "review", Kind: kind},
			{ID: "z", Kind: kind},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "root", Port: "value"}, To: EndpointRef{Node: "draft", Port: "in"}},
			{From: EndpointRef{Node: "root", Port: "value"}, To: EndpointRef{Node: "w", Port: "in"}},
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "review", Port: "in"}},
			{From: EndpointRef{Node: "draft", Port: "value"}, To: EndpointRef{Node: "z", Port: "in"}},
			{From: EndpointRef{Node: "w", Port: "value"}, To: EndpointRef{Node: "z", Port: "in2"}},
		},
	}
	ex := newDroppedDependencyExecutor(kind, "draft", "review", "w", "z")
	k := NewKernel(WithExecutor(ex))
	env := &Env{RunID: "bt-dropdep-run", Graph: g}
	applyEnvDefaults(env)

	runErr := make(chan error, 1)
	go func() { runErr <- k.Run(context.Background(), env) }()

	select {
	case <-ex.sideStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for w's fire")
	}
	select {
	case <-ex.reviewSecond:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for review's second fire — draft never refired")
	}

	// draft has already refired and run its promote-downstream section
	// for review and z in the same critical section, while w (z's
	// other, refire-set-external dependency) is still blocked. If the
	// in-degree recompute dropped w's still-pending edge — treating
	// every non-refire predecessor as already satisfied regardless of
	// completion — z would already have fired here with a missing
	// input port.
	if got := ex.callCount("z"); got != 0 {
		t.Fatalf("z fired %d times before w produced its output — dropped dependency", got)
	}

	close(ex.release)

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish after releasing w")
	}

	if ex.firedEarly() {
		t.Error("z fired before w completed")
	}
	if got := ex.callCount("z"); got != 1 {
		t.Errorf("z calls = %d, want 1", got)
	}
	if !env.State.Completed("root") || !env.State.Completed("draft") || !env.State.Completed("w") || !env.State.Completed("z") {
		t.Errorf("expected root/draft/w/z to have fired")
	}
}
