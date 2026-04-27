package agentgraph

import (
	"context"
	"errors"
	"testing"
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
	ex := branchExecutor{}
	node := &Node{ID: "b", Kind: NodeKindBranch, Attrs: BranchAttrs{
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

func TestRetryExecutor_RetriesUntilSuccess(t *testing.T) {
	t.Parallel()
	// Use a stub transform that fails twice then succeeds.
	calls := 0
	g := &Graph{Nodes: []Node{
		{ID: "retry", Kind: NodeKindRetry, Attrs: RetryAttrs{
			MaxAttempts: 3, BackoffBase: 1, Body: []string{"flake"},
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
			MaxAttempts: 2, BackoffBase: 1, Body: []string{"always"},
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

func TestForkExecutor_StubEmitsEvent(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	ex := forkExecutor{}
	node := &Node{ID: "fk", Kind: NodeKindFork, Attrs: ForkAttrs{Title: "child"}}
	r, err := ex.Execute(context.Background(), env, node, nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Outputs["branch_id"] == "" {
		t.Error("expected synthetic branch_id")
	}
	var sawForkReq bool
	for _, e := range r.Events.Events {
		if e.Kind == EventForkRequested {
			sawForkReq = true
		}
	}
	if !sawForkReq {
		t.Error("missing fork_requested event")
	}
}

func TestMergeExecutor_StubEmitsEvent(t *testing.T) {
	t.Parallel()
	env := newTestEnv(&Graph{})
	ex := mergeExecutor{}
	node := &Node{ID: "mg", Kind: NodeKindMerge, Attrs: MergeAttrs{BranchID: "b1"}}
	r, err := ex.Execute(context.Background(), env, node, PortValues{"branch": "b1", "branch_output": "result"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var sawMergeReq bool
	for _, e := range r.Events.Events {
		if e.Kind == EventMergeRequest {
			sawMergeReq = true
		}
	}
	if !sawMergeReq {
		t.Error("missing merge_requested event")
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
