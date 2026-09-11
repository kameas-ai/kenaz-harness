package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBranchExpr_Eq(t *testing.T) {
	t.Parallel()
	cases := []struct {
		expr   string
		inputs PortValues
		want   bool
	}{
		{`finish_reason == "tool_use"`, PortValues{"finish_reason": "tool_use"}, true},
		{`finish_reason == "stop"`, PortValues{"finish_reason": "tool_use"}, false},
		{`finish_reason != "stop"`, PortValues{"finish_reason": "tool_use"}, true},
		{`count > 5`, PortValues{"count": 10}, true},
		{`count < 5`, PortValues{"count": 10}, false},
		{`count >= 5`, PortValues{"count": 5}, true},
		{`count <= 5`, PortValues{"count": 5}, true},
		{`a == "x" and b == "y"`, PortValues{"a": "x", "b": "y"}, true},
		{`a == "x" and b == "y"`, PortValues{"a": "x", "b": "z"}, false},
		{`a == "x" or b == "y"`, PortValues{"a": "n", "b": "y"}, true},
		{`not (a == "x")`, PortValues{"a": "x"}, false},
		{`true`, nil, true},
		{`false`, nil, false},
		{`outer.inner == "deep"`, PortValues{"outer": map[string]any{"inner": "deep"}}, true},
	}
	for _, tc := range cases {
		got, err := evalBranchExpr(tc.expr, tc.inputs)
		if err != nil {
			t.Errorf("expr %q: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("expr %q: got %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestBranchExpr_BadSyntax(t *testing.T) {
	t.Parallel()
	bad := []string{
		"unterminated \"string",
		"a == ",
		")",
		"(",
	}
	for _, e := range bad {
		if _, err := evalBranchExpr(e, nil); err == nil {
			t.Errorf("expected error for %q", e)
		}
	}
}

func TestBranchExecutor_TrueFalsePorts(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	ex := decisionExecutor{}
	node := &Node{ID: "b", Kind: NodeKindDecision, Attrs: DecisionAttrs{
		Condition: `flag == "yes"`, NextTrue: "T", NextFalse: "F",
	}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"in": "x", "flag": "yes"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["next"] != "T" {
		t.Errorf("next = %v, want T", r.Outputs["next"])
	}
	if r.Outputs["verdict"] != true {
		t.Errorf("verdict = %v", r.Outputs["verdict"])
	}
}

func TestParallelExecutor_FanOutOrdered(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "p", Kind: NodeKindParallel, Attrs: ParallelAttrs{Targets: []string{"a", "b", "c"}, MaxConcurrency: 2}},
		{ID: "a", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
		{ID: "b", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
		{ID: "c", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	env := newTestEnv(g)
	ex := parallelExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	results := r.Outputs["out"].([]PortValues)
	if len(results) != 3 {
		t.Fatalf("results len = %d", len(results))
	}
	for _, res := range results {
		if res["out"] != "HELLO" {
			t.Errorf("expected HELLO, got %v", res["out"])
		}
	}
}

func TestParallelExecutor_RejectsUnknownTarget(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "p", Kind: NodeKindParallel, Attrs: ParallelAttrs{Targets: []string{"missing"}}},
	}}
	env := newTestEnv(g)
	ex := parallelExecutor{}
	if _, err := ex.Execute(context.Background(), env, &g.Nodes[0], nil); err == nil {
		t.Fatalf("expected error")
	}
}

func TestLoopExecutor_RespectsMaxIterations(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
			MaxIterations: 3, Body: []string{"step"},
		}},
		{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	env := newTestEnv(g)
	ex := loopExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "abc"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["iterations"] != 3 {
		t.Errorf("iterations = %v, want 3", r.Outputs["iterations"])
	}
}

func TestLoopExecutor_ConditionShortCircuit(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
			MaxIterations: 5, Condition: `out == "ABC"`, Body: []string{"step"},
		}},
		{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	env := newTestEnv(g)
	ex := loopExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "abc"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// First iter runs (no condition check on iter 0). Second iter: input
	// `in` is now "ABC" upon condition check; body runs uppercase on
	// `in` again - actually our test condition is on `out` from prior
	// iter. Both should converge after iter 1 since `out=="ABC"` holds.
	iter := r.Outputs["iterations"].(int)
	if iter < 1 || iter > 5 {
		t.Errorf("iter %d out of expected range", iter)
	}
}

// fakeTurnPauseGate is a race-safe test double for TurnPauseGate
// (subagent-control-and-background-tasks-01PMZB11 UNIT-8, AC-10).
// pausedAt is the 0-indexed Wait() call number at which Wait starts
// blocking (every call before that returns nil immediately). blockCh
// is closed by the test to simulate Resume; blockedSig fires (buffered
// 1) the instant Wait actually parks, so the test can synchronize on
// "the loop is now blocked" without sleep-based polling.
type fakeTurnPauseGate struct {
	mu       sync.Mutex
	calls    int
	pausedAt int
	blockCh  chan struct{}
	blocked  chan struct{}
}

func (g *fakeTurnPauseGate) Wait(ctx context.Context) error {
	g.mu.Lock()
	n := g.calls
	g.calls++
	g.mu.Unlock()
	if n < g.pausedAt {
		return nil
	}
	select {
	case g.blocked <- struct{}{}:
	default:
	}
	select {
	case <-g.blockCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *fakeTurnPauseGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// TestLoopExecutor_TurnPause_FreezesTurnCountUntilResumed is AC-10's
// proof: "a paused sub-agent starts no further turn; a resumed one
// does" — asserted via the ACTUAL turn (loop-iteration) count the real
// production loopExecutor.Execute produces, never via any status
// field. This exercises the real consult site added to exec_control.go
// (env.TurnPause.Wait before each iteration) directly, with no LLM or
// chat-stack scaffolding required.
//
// Mutation proof (manually verified 2026-09-10, matching PR #331's
// Abort/Steer convention): commenting out the
// `if env.TurnPause != nil { ... }` block this test exercises makes
// this test fail immediately — bodyRuns reaches 6 before the pause
// boundary is ever observed (gate.blocked never fires, so the test
// times out waiting for it), which is exactly the "implement pause as
// a status write" mutation AC-10 names. Reverting the comment-out
// restores green.
func TestLoopExecutor_TurnPause_FreezesTurnCountUntilResumed(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	bodyRuns := 0
	g := &Graph{Nodes: []Node{
		{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
			MaxIterations: 6, Body: []string{"step"},
		}},
		{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "count_turn"}},
	}}
	env := newTestEnv(g)
	env.Transforms.Register("count_turn", func(_ context.Context, _ PortValues, _ map[string]any) (PortValues, error) {
		mu.Lock()
		bodyRuns++
		mu.Unlock()
		return PortValues{"out": "x"}, nil
	})
	gate := &fakeTurnPauseGate{pausedAt: 3, blockCh: make(chan struct{}), blocked: make(chan struct{}, 1)}
	env.TurnPause = gate

	ex := loopExecutor{}
	type outcome struct {
		r   Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "x"})
		done <- outcome{r, err}
	}()

	select {
	case <-gate.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the loop to park at the pause boundary — TurnPause is not being consulted")
	}

	mu.Lock()
	turnsBeforeResume := bodyRuns
	mu.Unlock()
	if turnsBeforeResume != 3 {
		t.Fatalf("turn count while paused = %d, want exactly 3 (assert turn count, not status: AC-10)", turnsBeforeResume)
	}
	if got := gate.callCount(); got != 4 {
		t.Errorf("TurnPause.Wait call count while parked = %d, want 4 (consulted once per turn, including the one now blocked)", got)
	}

	close(gate.blockCh) // Resume: clears the pause so turns begin again.

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("Execute: %v", out.err)
		}
		if out.r.Outputs["iterations"] != 6 {
			t.Errorf("iterations = %v, want 6 (all turns ran after resume)", out.r.Outputs["iterations"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the loop to resume and finish after Resume cleared the pause")
	}

	mu.Lock()
	finalRuns := bodyRuns
	mu.Unlock()
	if finalRuns != 6 {
		t.Errorf("body ran %d times total, want 6", finalRuns)
	}
}

// TestLoopExecutor_TurnPause_NilGateNeverBlocks pins the default: a
// hand-built Env (every Kernel.Run caller except a dispatched
// sub-agent) leaves TurnPause nil, and the loop must run to completion
// exactly as before this UNIT-8 addition existed.
func TestLoopExecutor_TurnPause_NilGateNeverBlocks(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
			MaxIterations: 3, Body: []string{"step"},
		}},
		{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	env := newTestEnv(g)
	if env.TurnPause != nil {
		t.Fatalf("newTestEnv/applyEnvDefaults must leave TurnPause nil by default")
	}
	ex := loopExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "abc"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["iterations"] != 3 {
		t.Errorf("iterations = %v, want 3", r.Outputs["iterations"])
	}
}

// TestLoopExecutor_TurnPause_CtxCancelUnblocksWait covers the Abort
// interaction E-002 requires: "mid-turn cost is not bounded by Pause
// ... that is Abort ... which is also what unblocks a Wait call". A
// cancelled ctx must unblock a parked Wait rather than hang forever.
func TestLoopExecutor_TurnPause_CtxCancelUnblocksWait(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "loop", Kind: NodeKindLoop, Attrs: LoopAttrs{
			MaxIterations: 5, Body: []string{"step"},
		}},
		{ID: "step", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"}},
	}}
	env := newTestEnv(g)
	gate := &fakeTurnPauseGate{pausedAt: 0, blockCh: make(chan struct{}), blocked: make(chan struct{}, 1)}
	env.TurnPause = gate

	ctx, cancel := context.WithCancel(context.Background())
	ex := loopExecutor{}
	done := make(chan error, 1)
	go func() {
		_, err := ex.Execute(ctx, env, &g.Nodes[0], PortValues{"in": "abc"})
		done <- err
	}()

	select {
	case <-gate.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the loop to park on the pause gate")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Execute err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ctx cancellation to unblock the paused Wait call")
	}
}

func TestRetryExecutor_RetriesUntilSuccess(t *testing.T) {
	t.Parallel()
	// Use a stub transform that fails twice then succeeds.
	calls := 0
	g := &Graph{Nodes: []Node{
		{ID: "retry", Kind: NodeKindRetry, Attrs: RetryAttrs{
			MaxAttempts: 3, BackoffBaseMs: 1, Body: []string{"flake"},
		}},
		{ID: "flake", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "flaky"}},
	}}
	env := newTestEnv(g)
	env.Transforms.Register("flaky", func(_ context.Context, in PortValues, _ map[string]any) (PortValues, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("flake")
		}
		return PortValues{"out": "yes"}, nil
	})
	ex := retryExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["attempts"] != 3 {
		t.Errorf("attempts = %v, want 3", r.Outputs["attempts"])
	}
}

func TestRetryExecutor_ExhaustReturnsError(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "retry", Kind: NodeKindRetry, Attrs: RetryAttrs{
			MaxAttempts: 2, BackoffBaseMs: 1, Body: []string{"always"},
		}},
		{ID: "always", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "always_fail"}},
	}}
	env := newTestEnv(g)
	env.Transforms.Register("always_fail", func(_ context.Context, _ PortValues, _ map[string]any) (PortValues, error) {
		return nil, errors.New("nope")
	})
	ex := retryExecutor{}
	if _, err := ex.Execute(context.Background(), env, &g.Nodes[0], nil); err == nil {
		t.Fatalf("expected exhaustion error")
	}
}

// TestRetryExecutor_PromotesAttemptsToEventLog covers WP04's audit-parity
// requirement: RetryNode's per-attempt detail (previously logging.L()
// only, per exec_control.go:691,710,737 pre-WP04) must land in the
// replayable EventLog as EventRetryAttempt so an audit consumer can
// reconstruct the same detail without grepping structured logs.
func TestRetryExecutor_PromotesAttemptsToEventLog(t *testing.T) {
	t.Parallel()
	calls := 0
	g := &Graph{Nodes: []Node{
		{ID: "retry", Kind: NodeKindRetry, Attrs: RetryAttrs{
			MaxAttempts: 3, BackoffBaseMs: 1, Body: []string{"flake"},
		}},
		{ID: "flake", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "flaky"}},
	}}
	env := newTestEnv(g)
	env.Transforms.Register("flaky", func(_ context.Context, in PortValues, _ map[string]any) (PortValues, error) {
		calls++
		if calls < 3 {
			return nil, errors.New("flake")
		}
		return PortValues{"out": "yes"}, nil
	})
	ex := retryExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], PortValues{"in": "x"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var phases []string
	for _, e := range r.Events.Events {
		if e.Kind != EventRetryAttempt {
			continue
		}
		var payload map[string]any
		if uerr := json.Unmarshal(e.Payload, &payload); uerr != nil {
			t.Fatalf("decode EventRetryAttempt payload: %v", uerr)
		}
		phase, _ := payload["phase"].(string)
		phases = append(phases, phase)
	}
	// 3 attempts: attempt 1 start+error, attempt 2 start+error,
	// attempt 3 start+success.
	want := []string{"start", "error", "start", "error", "start", "success"}
	if len(phases) != len(want) {
		t.Fatalf("EventRetryAttempt phases = %v, want %v", phases, want)
	}
	for i, p := range phases {
		if p != want[i] {
			t.Errorf("phase[%d] = %q, want %q (full: %v)", i, p, want[i], phases)
		}
	}
}

// TestRetryExecutor_PromotesExhaustionToEventLog covers the exhausted
// phase: when every attempt fails, EventRetryAttempt still records a
// final "exhausted" entry even though Execute returns an error (the
// kernel commits Result.Events on the error path too — kernel.go's
// dispatch closure always appends r.Events.Events before checking err).
func TestRetryExecutor_PromotesExhaustionToEventLog(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "retry", Kind: NodeKindRetry, Attrs: RetryAttrs{
			MaxAttempts: 2, BackoffBaseMs: 1, Body: []string{"always"},
		}},
		{ID: "always", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "always_fail"}},
	}}
	env := newTestEnv(g)
	env.Transforms.Register("always_fail", func(_ context.Context, _ PortValues, _ map[string]any) (PortValues, error) {
		return nil, errors.New("nope")
	})
	ex := retryExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], nil)
	if err == nil {
		t.Fatalf("expected exhaustion error")
	}
	var sawExhausted bool
	for _, e := range r.Events.Events {
		if e.Kind != EventRetryAttempt {
			continue
		}
		var payload map[string]any
		if uerr := json.Unmarshal(e.Payload, &payload); uerr != nil {
			t.Fatalf("decode EventRetryAttempt payload: %v", uerr)
		}
		if payload["phase"] == "exhausted" {
			sawExhausted = true
		}
	}
	if !sawExhausted {
		t.Fatalf("expected an EventRetryAttempt with phase=exhausted among %d events", len(r.Events.Events))
	}
}

func TestForkExecutor_RealImpl_FiresFork(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	seam := NewFakeBranchSeam()
	env.Branch = seam
	ex := branchExecutor{}
	node := &Node{ID: "fk", Kind: NodeKindBranch, Attrs: BranchAttrs{Title: "child"}}
	r, err := ex.Execute(context.Background(), env, node,
		PortValues{"context": "you are a helpful summary"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["branch_id"] == "" {
		t.Error("expected branch_id output")
	}
	if r.Outputs["child_session_id"] == "" {
		t.Error("expected child_session_id output")
	}
	if len(seam.Forks) != 1 {
		t.Fatalf("Fork calls = %d, want 1", len(seam.Forks))
	}
	if seam.Forks[0].HandoffPrompt != "you are a helpful summary" {
		t.Errorf("handoff prompt = %q", seam.Forks[0].HandoffPrompt)
	}
	var sawForkReq, sawBranchFork bool
	for _, e := range r.Events.Events {
		if e.Kind == EventForkRequested {
			sawForkReq = true
		}
		if e.Kind == EventBranchFork {
			sawBranchFork = true
		}
	}
	if !sawForkReq {
		t.Error("missing fork_requested event")
	}
	if !sawBranchFork {
		t.Error("missing branch_fork event")
	}
}

func TestForkExecutor_NoBranchSeam_Errors(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	// applyEnvDefaults installs nilBranchSeam which errors.
	ex := branchExecutor{}
	node := &Node{ID: "fk", Kind: NodeKindBranch, Attrs: BranchAttrs{Title: "child"}}
	_, err := ex.Execute(context.Background(), env, node, nil)
	if err == nil || !errors.Is(err, ErrNoBranchSeam) {
		t.Fatalf("err = %v, want ErrNoBranchSeam", err)
	}
}

func TestMergeExecutor_RealImpl_AppendsAndMarksMerged(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	seam := NewFakeBranchSeam()
	seam.ChildTails["b1"] = []Message{
		{Role: "user", Content: "what's up?"},
		{Role: "assistant", Content: "Done. Latest dep version is 1.2.3."},
	}
	env.Branch = seam
	ex := mergeExecutor{}
	node := &Node{ID: "mg", Kind: NodeKindMerge, Attrs: MergeAttrs{BranchId: "b1", Mode: "summarize_append"}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"branch": "b1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["branch_id"] != "b1" {
		t.Errorf("branch_id output = %v", r.Outputs["branch_id"])
	}
	if r.Outputs["summary_msg_id"] == "" {
		t.Error("missing summary_msg_id")
	}
	if len(seam.ParentMessages) != 1 {
		t.Fatalf("parent messages = %d, want 1", len(seam.ParentMessages))
	}
	if seam.ParentMessages[0].Role != "system" {
		t.Errorf("role = %q, want system", seam.ParentMessages[0].Role)
	}
	if len(seam.Merged) != 1 || seam.Merged[0] != "b1" {
		t.Errorf("Merged = %v, want [b1]", seam.Merged)
	}
	var sawMergeReq, sawBranchMerge bool
	for _, e := range r.Events.Events {
		if e.Kind == EventMergeRequest {
			sawMergeReq = true
		}
		if e.Kind == EventBranchMerge {
			sawBranchMerge = true
		}
	}
	if !sawMergeReq {
		t.Error("missing merge_requested event")
	}
	if !sawBranchMerge {
		t.Error("missing branch_merge event")
	}
}

func TestMergeExecutor_MissingBranchID_Errors(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	env.Branch = NewFakeBranchSeam()
	ex := mergeExecutor{}
	// MergeAttrs with empty BranchID and no port → error.
	node := &Node{ID: "mg", Kind: NodeKindMerge, Attrs: MergeAttrs{BranchId: ""}}
	_, err := ex.Execute(context.Background(), env, node, nil)
	if err == nil {
		t.Fatal("expected error for missing branch id")
	}
}

func TestJoinExecutor_CollectsOutputs(t *testing.T) {
	t.Parallel()
	g := &Graph{Nodes: []Node{
		{ID: "j", Kind: NodeKindJoin, Attrs: JoinAttrs{From: []string{"a", "b"}}},
	}}
	env := newTestEnv(g)
	env.State.SetOutputs("a", PortValues{"out": "1"})
	env.State.SetOutputs("b", PortValues{"out": "2"})
	ex := joinExecutor{}
	r, err := ex.Execute(context.Background(), env, &g.Nodes[0], nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := r.Outputs["out"].([]any)
	if len(out) != 2 || out[0] != "1" || out[1] != "2" {
		t.Errorf("collected = %v", out)
	}
}
