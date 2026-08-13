package agentgraph

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"
)

// kernel_liveness_test.go — behaviour tests for conditional execution
// (agentgraph-total-convergence-01PMGX01 WP02b/WP02c, designed in
// agentic-turn-routing-01PMAG01 §3.2–3.3).
//
// The companion file kernel_promotion_golden_test.go proves the change
// moved NOTHING for the graphs the repo ships. This file proves it
// moved the thing it was supposed to: a `decision` now fires exactly
// one successor, the other branch is skipped transitively, and
// reconvergence points below the branch still fire exactly once.

// scriptedRuntime is the race-safe fake backing the scripted executors
// below. The kernel dispatches from bare goroutines, so every field is
// mutex-guarded and all test-side reads go through a snapshot helper
// (CLAUDE.md "Race-safe test fakes").
type scriptedRuntime struct {
	mu sync.Mutex
	// fired is the ordered list of node IDs that actually executed.
	fired []string
	// fireCount is per-node, so a scripted behaviour can change across
	// a backtrack rewind.
	fireCount map[string]int
	// behaviour is keyed by node ID; nil entry means "emit `out`".
	behaviour map[string]func(fire int) (Result, error)
}

func newScriptedRuntime() *scriptedRuntime {
	return &scriptedRuntime{
		fireCount: map[string]int{},
		behaviour: map[string]func(int) (Result, error){},
	}
}

func (s *scriptedRuntime) run(nodeID string) (Result, error) {
	s.mu.Lock()
	s.fired = append(s.fired, nodeID)
	s.fireCount[nodeID]++
	fire := s.fireCount[nodeID]
	fn := s.behaviour[nodeID]
	s.mu.Unlock()
	if fn != nil {
		return fn(fire)
	}
	res := NewResult()
	res.Outputs["out"] = "value:" + nodeID
	return res, nil
}

func (s *scriptedRuntime) snapshotFired() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.fired))
	copy(out, s.fired)
	return out
}

func (s *scriptedRuntime) fires(nodeID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fireCount[nodeID]
}

// scriptedKindExecutor routes one node kind into the shared runtime.
type scriptedKindExecutor struct {
	kind NodeKind
	rt   *scriptedRuntime
}

func (s scriptedKindExecutor) Kind() NodeKind { return s.kind }

func (s scriptedKindExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	return s.rt.run(node.ID)
}

// livenessKernel builds a kernel where every kind except `decision` and
// `join` runs the scripted runtime. `decision` and `join` keep their
// production executors — they are the subject under test.
func livenessKernel(rt *scriptedRuntime, log EventLog) *Kernel {
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		if k == NodeKindDecision || k == NodeKindJoin {
			continue
		}
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	return NewKernel(opts...)
}

// skippedIDs reads the node_skipped events out of a run log.
func skippedIDs(t *testing.T, log EventLog, runID string) []string {
	t.Helper()
	var out []string
	if err := log.Replay(runID, func(e Event) error {
		if e.Kind == EventNodeSkipped && e.NodeID != "" {
			out = append(out, e.NodeID)
		}
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	sort.Strings(out)
	return out
}

// decisionGraph builds `seed -> dec -> {taken_true, taken_false}` where
// `dec` routes on the `flag` value seed emits.
func decisionGraph() *Graph {
	return &Graph{
		SpecVersion: SpecVersion,
		ID:          "decision-liveness",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				// `out` is the transform manifest's required output; `flag`
				// is the extra port this graph routes on.
				Outputs: []Port{
					{Name: "out", Type: PortType("any")},
					{Name: "flag", Type: PortType("text")},
				}},
			{ID: "dec", Kind: NodeKindDecision, Attrs: DecisionAttrs{
				Condition: `flag == "yes"`, NextTrue: "yes_branch", NextFalse: "no_branch",
			},
				// `in` is the manifest's canonical payload port; `flag` is
				// the extra port the condition expression reads.
				Inputs: []Port{
					{Name: "in", Type: PortType("any")},
					{Name: "flag", Type: PortType("text")},
				}},
			{ID: "yes_branch", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "yes"}},
			{ID: "no_branch", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "no"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "out"}, To: EndpointRef{Node: "dec", Port: "in"}},
			{From: EndpointRef{Node: "seed", Port: "flag"}, To: EndpointRef{Node: "dec", Port: "flag"}},
			{From: EndpointRef{Node: "dec", Port: "true"}, To: EndpointRef{Node: "yes_branch", Port: "in"}},
			{From: EndpointRef{Node: "dec", Port: "false"}, To: EndpointRef{Node: "no_branch", Port: "in"}},
		},
	}
}

// TestKernel_DecisionFiresExactlyOneSuccessor is the headline WP02b
// regression. Before the change the kernel decremented in-degree
// unconditionally, so BOTH successors of a decision fired and the
// not-taken one ran with a silently missing input port.
func TestKernel_DecisionFiresExactlyOneSuccessor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		flag      string
		wantFired string
		wantSkip  string
	}{
		{name: "true verdict", flag: "yes", wantFired: "yes_branch", wantSkip: "no_branch"},
		{name: "false verdict", flag: "no", wantFired: "no_branch", wantSkip: "yes_branch"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newScriptedRuntime()
			flag := tc.flag
			rt.behaviour["seed"] = func(int) (Result, error) {
				res := NewResult()
				res.Outputs["out"] = "seed"
				res.Outputs["flag"] = flag
				return res, nil
			}
			log := NewMemoryEventLog()
			k := livenessKernel(rt, log)
			g := decisionGraph()
			env := &Env{RunID: "run-decision-" + tc.name, Graph: g}
			applyEnvDefaults(env)
			if err := k.Run(context.Background(), env); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := rt.fires(tc.wantFired); got != 1 {
				t.Errorf("%s fired %d times, want 1", tc.wantFired, got)
			}
			if got := rt.fires(tc.wantSkip); got != 0 {
				t.Errorf("%s fired %d times, want 0 — the not-taken branch must not execute", tc.wantSkip, got)
			}
			if got, want := skippedIDs(t, log, env.RunID), []string{tc.wantSkip}; len(got) != 1 || got[0] != want[0] {
				t.Errorf("node_skipped events = %v, want %v", got, want)
			}
		})
	}
}

// TestKernel_SkipPropagatesTransitively pins that skip is not a
// one-hop property: everything reachable only through the not-taken
// branch is skipped too, and the run still completes cleanly.
func TestKernel_SkipPropagatesTransitively(t *testing.T) {
	t.Parallel()
	g := decisionGraph()
	// Extend the false branch into a chain: no_branch -> tail1 -> tail2.
	g.Nodes = append(g.Nodes,
		Node{ID: "tail1", Kind: NodeKindArtifact, Attrs: ArtifactAttrs{OutputTarget: "session"}},
		Node{ID: "tail2", Kind: NodeKindSessionWrite, Attrs: SessionWriteAttrs{Role: "assistant"}},
	)
	g.Edges = append(g.Edges,
		Edge{From: EndpointRef{Node: "no_branch", Port: "out"}, To: EndpointRef{Node: "tail1", Port: "in"}},
		Edge{From: EndpointRef{Node: "tail1", Port: "out"}, To: EndpointRef{Node: "tail2", Port: "text"}},
	)

	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "yes" // take the true branch; the whole false chain dies
		return res, nil
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-skip-chain", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"no_branch", "tail1", "tail2"} {
		if got := rt.fires(id); got != 0 {
			t.Errorf("%s fired %d times, want 0 (skip must propagate transitively)", id, got)
		}
	}
	if got := rt.fires("yes_branch"); got != 1 {
		t.Errorf("yes_branch fired %d times, want 1", got)
	}
	want := []string{"no_branch", "tail1", "tail2"}
	got := skippedIDs(t, log, env.RunID)
	if len(got) != len(want) {
		t.Fatalf("node_skipped = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node_skipped = %v, want %v", got, want)
		}
	}
}

// TestKernel_SkippedBranchDoesNotDeadlockJoin is the reconvergence
// case, and the reason in-degree still decrements unconditionally. The
// naive fix — skip the decrement for a not-taken edge — leaves any join
// below a branch waiting forever on an arrival that will never come.
//
// Skip-arrival semantics for `join` (01PMAG01 §3.2): a skipped inbound
// edge IS an arrival, it just carries no value. The join fires exactly
// once as long as at least one inbound edge was live.
// convergence:exercised join
//
// livenessKernel scripts every kind EXCEPT `decision` and `join`, which
// keep their production executors because they are the subject under
// test. So this run really does fire joinExecutor: the assertion below
// reads rejoin's recorded outputs precisely BECAUSE it is not in the
// scripted-fire table.
func TestKernel_SkippedBranchDoesNotDeadlockJoin(t *testing.T) {
	t.Parallel()
	g := decisionGraph()
	g.Nodes = append(g.Nodes, Node{
		ID: "rejoin", Kind: NodeKindJoin,
		Attrs: JoinAttrs{From: []string{"yes_branch", "no_branch"}},
	})
	g.Edges = append(g.Edges,
		Edge{From: EndpointRef{Node: "yes_branch", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "a"}},
		Edge{From: EndpointRef{Node: "no_branch", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "b"}},
	)

	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "yes"
		return res, nil
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-join-reconverge", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The join runs its production executor, so it is not in rt.fired —
	// assert on its recorded outputs instead.
	if !env.State.Completed("rejoin") {
		t.Fatal("rejoin never fired — a skipped branch deadlocked the reconvergence point")
	}
	collected, ok := env.State.Outputs("rejoin")["out"].([]any)
	if !ok {
		t.Fatalf("rejoin out = %T, want []any", env.State.Outputs("rejoin")["out"])
	}
	if len(collected) != 2 {
		t.Fatalf("rejoin collected %d entries, want 2 (one per JoinAttrs.From)", len(collected))
	}
	// The live branch contributes its value; the skipped one recorded
	// no outputs at all, so it contributes an empty bag rather than a
	// phantom value.
	if collected[0] != "value:yes_branch" {
		t.Errorf("rejoin[0] = %v, want the live branch's value", collected[0])
	}
	if pv, ok := collected[1].(PortValues); !ok || len(pv) != 0 {
		t.Errorf("rejoin[1] = %#v, want an empty bag for the skipped branch", collected[1])
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 1 || got[0] != "no_branch" {
		t.Errorf("node_skipped = %v, want [no_branch] — rejoin must NOT be skipped", got)
	}
}

// TestKernel_JoinSkippedWhenEveryBranchIsSkipped pins the transitive
// half of the join rule: a reconvergence point whose every inbound edge
// was skipped is itself skipped, and the skip keeps propagating past
// it.
func TestKernel_JoinSkippedWhenEveryBranchIsSkipped(t *testing.T) {
	t.Parallel()
	// seed -> dec -> {a (true), b (false)}; both a and b feed `rejoin`.
	// Only one of a/b ever runs, so to get an all-dead join we route
	// `rejoin` off the two DEAD legs of two decisions.
	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "join-all-dead",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				Outputs: []Port{{Name: "flag", Type: PortType("text")}}},
			{ID: "dec1", Kind: NodeKindDecision, Attrs: DecisionAttrs{
				Condition: `flag == "yes"`, NextTrue: "dead1", NextFalse: "live1"}},
			{ID: "dec2", Kind: NodeKindDecision, Attrs: DecisionAttrs{
				Condition: `flag == "yes"`, NextTrue: "dead2", NextFalse: "live2"}},
			{ID: "dead1", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "d1"}},
			{ID: "dead2", Kind: NodeKindArtifact, Attrs: ArtifactAttrs{OutputTarget: "session"}},
			{ID: "live1", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "l1"}},
			{ID: "live2", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "l2"}},
			{ID: "rejoin", Kind: NodeKindJoin, Attrs: JoinAttrs{From: []string{"dead1", "dead2"}}},
			{ID: "after", Kind: NodeKindSessionWrite, Attrs: SessionWriteAttrs{Role: "assistant"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "flag"}, To: EndpointRef{Node: "dec1", Port: "flag"}},
			{From: EndpointRef{Node: "seed", Port: "flag"}, To: EndpointRef{Node: "dec2", Port: "flag"}},
			{From: EndpointRef{Node: "dec1", Port: "true"}, To: EndpointRef{Node: "dead1", Port: "in"}},
			{From: EndpointRef{Node: "dec1", Port: "false"}, To: EndpointRef{Node: "live1", Port: "in"}},
			{From: EndpointRef{Node: "dec2", Port: "true"}, To: EndpointRef{Node: "dead2", Port: "in"}},
			{From: EndpointRef{Node: "dec2", Port: "false"}, To: EndpointRef{Node: "live2", Port: "in"}},
			{From: EndpointRef{Node: "dead1", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "a"}},
			{From: EndpointRef{Node: "dead2", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "b"}},
			{From: EndpointRef{Node: "rejoin", Port: "out"}, To: EndpointRef{Node: "after", Port: "text"}},
		},
	}

	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "no" // both decisions take `false`; both dead legs die
		return res, nil
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-join-all-dead", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"live1", "live2"} {
		if got := rt.fires(id); got != 1 {
			t.Errorf("%s fired %d times, want 1", id, got)
		}
	}
	if env.State.Completed("rejoin") {
		t.Error("rejoin executed, but every inbound edge was skipped")
	}
	if got := rt.fires("after"); got != 0 {
		t.Errorf("after fired %d times, want 0 — the skip must propagate past the join", got)
	}
	want := []string{"after", "dead1", "dead2", "rejoin"}
	got := skippedIDs(t, log, env.RunID)
	if len(got) != len(want) {
		t.Fatalf("node_skipped = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("node_skipped = %v, want %v", got, want)
		}
	}
}

// TestKernel_BacktrackClearsSkipAndRefiresTheOtherBranch covers the
// interaction 01PMAG01 §3.2 flags explicitly: the backtrack refire-set
// recompute must rebuild `liveIn` and clear the skip bits, or a rewound
// branch inherits stale liveness and never re-fires.
//
// The run takes the false branch, the false branch's tail requests a
// rewind to `seed`, and `seed`'s second fire flips the flag. The true
// branch — skipped on pass one — must now execute.
func TestKernel_BacktrackClearsSkipAndRefiresTheOtherBranch(t *testing.T) {
	t.Parallel()
	g := decisionGraph()
	// Give the false branch a tail that asks for one rewind.
	g.Nodes = append(g.Nodes, Node{
		ID: "rewinder", Kind: NodeKindArtifact, Attrs: ArtifactAttrs{OutputTarget: "session"},
	})
	g.Edges = append(g.Edges, Edge{
		From: EndpointRef{Node: "no_branch", Port: "out"}, To: EndpointRef{Node: "rewinder", Port: "in"},
	})

	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(fire int) (Result, error) {
		res := NewResult()
		if fire == 1 {
			res.Outputs["out"] = "seed"
			res.Outputs["flag"] = "no"
		} else {
			res.Outputs["out"] = "seed"
			res.Outputs["flag"] = "yes"
		}
		return res, nil
	}
	rt.behaviour["rewinder"] = func(fire int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "rewinder"
		if fire == 1 {
			res.Backtrack = &BacktrackRequest{
				TargetNode:       "seed",
				Reason:           "WP02b skip/backtrack interaction test",
				RejectedApproach: "the false branch",
			}
		}
		return res, nil
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-skip-backtrack", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rt.fires("seed"); got != 2 {
		t.Fatalf("seed fired %d times, want 2 (one rewind)", got)
	}
	if got := rt.fires("no_branch"); got != 1 {
		t.Errorf("no_branch fired %d times, want 1 (pass one only)", got)
	}
	if got := rt.fires("yes_branch"); got != 1 {
		t.Errorf("yes_branch fired %d times, want 1 — the rewind must clear its skip bit and let it run", got)
	}
}

// TestKernel_DecisionRoutesOnNextAttrsNotJustPorts is the WP02c pin:
// next_true / next_false are read by the kernel. The successors here
// hang off a NON-canonical source port, so port-based liveness alone
// cannot route them — only the attrs can.
func TestKernel_DecisionRoutesOnNextAttrsNotJustPorts(t *testing.T) {
	t.Parallel()
	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "decision-attr-routing",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				Outputs: []Port{{Name: "flag", Type: PortType("text")}}},
			{ID: "dec", Kind: NodeKindDecision, Attrs: DecisionAttrs{
				Condition: `flag == "yes"`, NextTrue: "yes_branch", NextFalse: "no_branch"},
				Outputs: []Port{
					{Name: "true", Type: PortType("any")},
					{Name: "false", Type: PortType("any")},
					{Name: "verdict", Type: PortType("bool")},
					{Name: "next", Type: PortType("text")},
				}},
			{ID: "yes_branch", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "yes"}},
			{ID: "no_branch", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{Label: "no"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "flag"}, To: EndpointRef{Node: "dec", Port: "flag"}},
			// Both successors hang off `verdict`, an audit port. Only
			// next_true / next_false distinguish them.
			{From: EndpointRef{Node: "dec", Port: "verdict"}, To: EndpointRef{Node: "yes_branch", Port: "in"}},
			{From: EndpointRef{Node: "dec", Port: "verdict"}, To: EndpointRef{Node: "no_branch", Port: "in"}},
		},
	}
	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "yes"
		return res, nil
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-decision-attrs", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rt.fires("yes_branch"); got != 1 {
		t.Errorf("yes_branch fired %d times, want 1 (next_true names it)", got)
	}
	if got := rt.fires("no_branch"); got != 0 {
		t.Errorf("no_branch fired %d times, want 0 (next_false names it and the verdict was true)", got)
	}
}

// delegatingEdgeRouter is a wrapper executor that produces NO routing
// output of its own but forwards liveness to the real implementation.
// It exists to make the executors' missing-verdict / missing-choice
// fallbacks reachable and therefore testable (review finding B4).
//
// It is not a contrivance: forwarding LiveOutEdges to the wrapped
// implementation is what any decorator over a routing kind — telemetry,
// policy, a recorded-replay harness — has to do to keep routing
// working, and such a decorator can absolutely be registered with an
// Execute that fails to write the routing port. Before this, both
// fallbacks were unreachable dead code guarded by tests that passed for
// an unrelated reason (the override did not implement EdgeRouter at
// all, so the kernel never called into the fallback).
type delegatingEdgeRouter struct {
	kind  NodeKind
	rt    *scriptedRuntime
	inner EdgeRouter
}

func (d delegatingEdgeRouter) Kind() NodeKind { return d.kind }

func (d delegatingEdgeRouter) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	return d.rt.run(node.ID)
}

func (d delegatingEdgeRouter) LiveOutEdges(nd *Node, outs PortValues) func(Edge) bool {
	return d.inner.LiveOutEdges(nd, outs)
}

// TestDecisionExecutor_MissingVerdictFallbackIsReachable exercises the
// conservative fallback INSIDE decisionExecutor.LiveOutEdges through a
// registered EdgeRouter that delegates to it — the reachable path
// (B4). A decision with no `verdict` recorded gives the kernel nothing
// to route on, so it must promote every branch rather than guess one
// and silently kill the other.
//
// Deleting the fallback makes this test fail (it would route the
// `false` branch off a zero-valued verdict), which is the definition of
// non-vacuous.
func TestDecisionExecutor_MissingVerdictFallbackIsReachable(t *testing.T) {
	t.Parallel()
	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "yes"
		return res, nil
	}
	// The wrapper's Execute writes the two routing PORTS but no
	// `verdict`, so LiveOutEdges is called and takes the fallback.
	rt.behaviour["dec"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["true"] = "t"
		res.Outputs["false"] = "f"
		return res, nil
	}
	log := NewMemoryEventLog()
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		if k == NodeKindDecision {
			opts = append(opts, WithExecutor(delegatingEdgeRouter{
				kind: k, rt: rt, inner: decisionExecutor{},
			}))
			continue
		}
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	k := NewKernel(opts...)
	g := decisionGraph()
	env := &Env{RunID: "run-decision-fallback", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"yes_branch", "no_branch"} {
		if got := rt.fires(id); got != 1 {
			t.Errorf("%s fired %d times, want 1 — a decision with no recorded verdict has no routing authority and must promote both", id, got)
		}
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 0 {
		t.Errorf("node_skipped = %v, want none", got)
	}
}

// TestRouterExecutor_MissingChoiceFallbackIsReachable is the router's
// half of B4. It matters MORE than the decision one: without the
// fallback a missing `next` makes every declared choice port dead, so
// the router would skip its entire downstream subgraph rather than
// merely picking a wrong branch.
func TestRouterExecutor_MissingChoiceFallbackIsReachable(t *testing.T) {
	t.Parallel()
	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		return res, nil
	}
	// No `next` written — the fallback's trigger.
	rt.behaviour["route"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "routed"
		return res, nil
	}
	log := NewMemoryEventLog()
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		switch k {
		case NodeKindRouter:
			opts = append(opts, WithExecutor(delegatingEdgeRouter{
				kind: k, rt: rt, inner: routerExecutor{},
			}))
		case NodeKindJoin:
			// keep the production join
		default:
			opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
		}
	}
	k := NewKernel(opts...)
	g := routerLivenessGraph()
	env := &Env{RunID: "run-router-fallback", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"do_research", "do_draft", "do_done"} {
		if got := rt.fires(id); got != 1 {
			t.Errorf("%s fired %d times, want 1 — a router with no recorded choice must promote every branch, not skip them all", id, got)
		}
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 0 {
		t.Errorf("node_skipped = %v, want none", got)
	}
}

// TestKernel_NonEdgeRouterExecutorPromotesEveryBranch pins the OTHER
// reachable contract, and states plainly what the two tests above used
// to claim by accident: WithExecutor installs its override through
// Replace, so the override IS the registered executor — and an override
// that does not implement EdgeRouter gets unconditional promotion.
//
// This is a deliberate semantic of the EdgeRouter seam and a change
// from the kind-switch it replaced (a plain override that wrote
// `verdict` used to route). Production is unaffected: the registered
// executor for `decision` is the real one. Any harness that wants
// routing preserved delegates, as delegatingEdgeRouter does.
func TestKernel_NonEdgeRouterExecutorPromotesEveryBranch(t *testing.T) {
	t.Parallel()
	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		res.Outputs["flag"] = "yes"
		return res, nil
	}
	// Override `decision` with a plain executor. It even writes a
	// `verdict` — and it is still ignored, because the override does
	// not implement EdgeRouter. That is the point being pinned.
	rt.behaviour["dec"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["verdict"] = true
		res.Outputs["true"] = "t"
		res.Outputs["false"] = "f"
		return res, nil
	}
	log := NewMemoryEventLog()
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	k := NewKernel(opts...)
	g := decisionGraph()
	env := &Env{RunID: "run-decision-non-edgerouter", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"yes_branch", "no_branch"} {
		if got := rt.fires(id); got != 1 {
			t.Errorf("%s fired %d times, want 1 — a non-EdgeRouter executor gets unconditional promotion", id, got)
		}
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 0 {
		t.Errorf("node_skipped = %v, want none", got)
	}
}

// TestValidate_DecisionRoutingAttrs covers the WP02c validator rule
// that keeps the kernel's two routing authorities (attr target, source
// port) from disagreeing.
func TestValidate_DecisionRoutingAttrs(t *testing.T) {
	t.Parallel()
	base := func(mutate func(*Graph)) Graph {
		g := *decisionGraph()
		g.Nodes = append([]Node(nil), g.Nodes...)
		g.Edges = append([]Edge(nil), g.Edges...)
		mutate(&g)
		return g
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*Graph)
		mustHas string
	}{
		{
			name: "unknown next_true target",
			mutate: func(g *Graph) {
				g.Nodes[1].Attrs = DecisionAttrs{Condition: `flag == "yes"`, NextTrue: "nope", NextFalse: "no_branch"}
				g.Edges = g.Edges[:2]
			},
			mustHas: "references unknown node",
		},
		{
			name: "both branches name the same node",
			mutate: func(g *Graph) {
				g.Nodes[1].Attrs = DecisionAttrs{Condition: `flag == "yes"`, NextTrue: "no_branch", NextFalse: "no_branch"}
				g.Edges = g.Edges[:2]
			},
			mustHas: "is not a decision",
		},
		{
			name: "port edge disagrees with the attr",
			mutate: func(g *Graph) {
				// next_true says yes_branch; the `true` port edge goes
				// to no_branch.
				g.Edges[2] = Edge{From: EndpointRef{Node: "dec", Port: "true"}, To: EndpointRef{Node: "no_branch", Port: "in"}}
			},
			mustHas: "must agree",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := base(tc.mutate)
			err := Validate(g)
			if err == nil {
				t.Fatalf("Validate: want an error containing %q, got nil", tc.mustHas)
			}
			ve, ok := err.(*ValidationError)
			if !ok || !ve.Has(tc.mustHas) {
				t.Fatalf("Validate: want an issue containing %q, got %v", tc.mustHas, err)
			}
		})
	}
}

// TestValidate_DecisionRoutingAcceptsBundledGraphs guards the new rule
// against false positives — both on a well-formed hand-built graph and
// on the shipped graphs that actually carry decision/branch nodes.
func TestValidate_DecisionRoutingAcceptsBundledGraphs(t *testing.T) {
	t.Parallel()
	if err := Validate(*decisionGraph()); err != nil {
		t.Fatalf("Validate on a well-formed decision graph: %v", err)
	}
	// NOTE: ../rpc/views/agentgraph/library/toolloop_default.yaml is
	// deliberately NOT in this list. It already fails Validate() on the
	// pre-WP02c tree — its `continue_check` node uses the legacy
	// `kind: branch` spelling, which resolves to the sub-graph-spawn
	// manifest (requiring a `title` attr and having no `in` port)
	// rather than the decision alias. That is a pre-existing defect in
	// a shipped library graph, verified against the unmodified
	// validator, and it is not this WP's to fix.
	for _, path := range []string{
		"testdata/default_toolloop.yaml",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		g, err := LoadYAML(data)
		if err != nil {
			t.Fatalf("LoadYAML %s: %v", path, err)
		}
		if err := Validate(g); err != nil {
			t.Errorf("Validate %s: %v", path, err)
		}
	}
}

// TestKernel_AbsentSourcePortStillPromotes pins the deliberate default
// on the other side of edgeLivenessFor: for every kind that is NOT a
// `decision`, an out-edge is live regardless of whether the source node
// actually produced a value on the port the edge names.
//
// This is the single riskiest property in the WP02b promotion rewrite.
// A "port present ⇒ live, port absent ⇒ skip" rule looks obviously
// right and is wrong: production chat_default sources
// assistant_write's inbound edge from agent_loop's `assistant_text`
// port, which the LoopNode's flattened outputs do not always carry —
// under a port-presence rule the harness would silently stop persisting
// assistant replies.
//
// The chat_default entry in kernel_promotion_golden_test.go used to
// pin this incidentally, as a side effect of tool_dispatch.yaml
// under-declaring its outputs. Phase 2 fixed that manifest
// (agentgraph-total-convergence-01PMGX01), so the property gets its own
// test rather than depending on a bug staying unfixed.
func TestKernel_AbsentSourcePortStillPromotes(t *testing.T) {
	t.Parallel()
	rt := newScriptedRuntime()
	// `seed` produces ONLY `out` — nothing on `ghost`, which is exactly
	// the port the downstream edge is sourced from.
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		return res, nil
	}
	g := &Graph{
		SpecVersion: SpecVersion,
		ID:          "absent-port-promotion",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				Outputs: []Port{
					{Name: "out", Type: PortType("any")},
					{Name: "ghost", Type: PortType("text")},
				}},
			{ID: "downstream", Kind: NodeKindTraceWrite,
				Attrs: TraceWriteAttrs{Severity: "info", Message: "downstream"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "ghost"}, To: EndpointRef{Node: "downstream", Port: "in"}},
		},
	}
	log := NewMemoryEventLog()
	k := livenessKernel(rt, log)
	env := &Env{RunID: "run-absent-port", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rt.fires("downstream"); got != 1 {
		t.Errorf("downstream fired %d times, want 1 — an absent source port must NOT skip a non-decision successor", got)
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 0 {
		t.Errorf("node_skipped = %v, want none", got)
	}
}

// routerLivenessKernel builds a kernel where `router` (and `join`,
// needed by the reconvergence case) keep their production executors and
// every other kind runs the scripted runtime. The router is the subject
// under test here the way `decision` is above.
func routerLivenessKernel(rt *scriptedRuntime, log EventLog) *Kernel {
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		if k == NodeKindRouter || k == NodeKindJoin {
			continue
		}
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	return NewKernel(opts...)
}

// routerLivenessGraph builds
//
//	seed -> route -{research|draft|done}-> three leaves -> rejoin
//
// with the three successors hanging off their own choice ports and
// reconverging on a `join`. `seed` also plays the part of the fused
// mode's source node: the router reads its `assistant` output for the
// choice.
func routerLivenessGraph() *Graph {
	return &Graph{
		SpecVersion: SpecVersion,
		ID:          "router-liveness",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				Outputs: []Port{
					{Name: "out", Type: PortType("any")},
					{Name: "assistant", Type: PortType("any")},
				}},
			{ID: "route", Kind: NodeKindRouter,
				Outputs: []Port{
					{Name: "next", Type: PortType("text")},
					{Name: "out", Type: PortType("any")},
					{Name: "research", Type: PortType("any")},
					{Name: "draft", Type: PortType("any")},
					{Name: "done", Type: PortType("any")},
				},
				Attrs: RouterAttrs{
					Mode:       routerModeFused,
					SourceNode: "seed",
					Choices: map[string]any{
						"research": map[string]any{"target": "do_research"},
						"draft":    map[string]any{"target": "do_draft"},
						"done":     map[string]any{"target": "do_done"},
					},
					DefaultChoice: "done",
				}},
			{ID: "do_research", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "research"},
				Outputs: []Port{{Name: "ack", Type: PortType("bool")}, {Name: "out", Type: PortType("any")}}},
			{ID: "do_draft", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "draft"},
				Outputs: []Port{{Name: "ack", Type: PortType("bool")}, {Name: "out", Type: PortType("any")}}},
			{ID: "do_done", Kind: NodeKindTraceWrite, Attrs: TraceWriteAttrs{Severity: "info", Message: "done"},
				Outputs: []Port{{Name: "ack", Type: PortType("bool")}, {Name: "out", Type: PortType("any")}}},
			{ID: "rejoin", Kind: NodeKindJoin,
				Inputs: []Port{
					{Name: "in", Type: PortType("any")},
					{Name: "a", Type: PortType("any")},
					{Name: "b", Type: PortType("any")},
					{Name: "c", Type: PortType("any")},
				},
				Attrs: JoinAttrs{From: []string{"do_research", "do_draft", "do_done"}}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "out"}, To: EndpointRef{Node: "route", Port: "in"}},
			{From: EndpointRef{Node: "route", Port: "research"}, To: EndpointRef{Node: "do_research", Port: "in"}},
			{From: EndpointRef{Node: "route", Port: "draft"}, To: EndpointRef{Node: "do_draft", Port: "in"}},
			{From: EndpointRef{Node: "route", Port: "done"}, To: EndpointRef{Node: "do_done", Port: "in"}},
			{From: EndpointRef{Node: "do_research", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "a"}},
			{From: EndpointRef{Node: "do_draft", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "b"}},
			{From: EndpointRef{Node: "do_done", Port: "out"}, To: EndpointRef{Node: "rejoin", Port: "c"}},
		},
	}
}

// TestKernel_RouterFiresExactlyOneSuccessor is the WP11a counterpart of
// TestKernel_DecisionFiresExactlyOneSuccessor, and it is what proves
// the EdgeRouter seam is genuinely kind-agnostic: `router` gets
// conditional promotion without the kernel naming it anywhere.
//
// It also covers the reconvergence case in the same run: `rejoin` sits
// below all three branches and must still fire exactly once, because a
// skipped inbound edge decrements in-degree while contributing no
// liveness.
func TestKernel_RouterFiresExactlyOneSuccessor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		reply     string
		wantFired string
		wantSkip  []string
	}{
		{name: "research", reply: `{"next_choice": "research"}`, wantFired: "do_research", wantSkip: []string{"do_done", "do_draft"}},
		{name: "draft", reply: `{"next_choice": "draft"}`, wantFired: "do_draft", wantSkip: []string{"do_done", "do_research"}},
		{name: "unparseable falls back to default", reply: "no idea", wantFired: "do_done", wantSkip: []string{"do_draft", "do_research"}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := newScriptedRuntime()
			reply := tc.reply
			rt.behaviour["seed"] = func(int) (Result, error) {
				res := NewResult()
				res.Outputs["out"] = "seed"
				res.Outputs["assistant"] = Message{Role: "assistant", Content: reply}
				return res, nil
			}
			log := NewMemoryEventLog()
			k := routerLivenessKernel(rt, log)
			g := routerLivenessGraph()
			env := &Env{RunID: "run-router-" + tc.name, Graph: g}
			applyEnvDefaults(env)
			if err := k.Run(context.Background(), env); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := rt.fires(tc.wantFired); got != 1 {
				t.Errorf("%s fired %d times, want 1", tc.wantFired, got)
			}
			for _, id := range tc.wantSkip {
				if got := rt.fires(id); got != 0 {
					t.Errorf("%s fired %d times, want 0 — an unselected choice must not execute", id, got)
				}
			}
			gotSkipped := skippedIDs(t, log, env.RunID)
			if len(gotSkipped) != len(tc.wantSkip) {
				t.Fatalf("node_skipped = %v, want %v", gotSkipped, tc.wantSkip)
			}
			for i := range gotSkipped {
				if gotSkipped[i] != tc.wantSkip[i] {
					t.Fatalf("node_skipped = %v, want %v", gotSkipped, tc.wantSkip)
				}
			}
			// The reconvergence point below all three branches still
			// fires exactly once: two of its three inbound edges were
			// skipped, which satisfies its in-degree without adding
			// liveness. `join` keeps its production executor here, so
			// it never lands in rt.fired — assert on RunState.
			if !env.State.Completed("rejoin") {
				t.Error("rejoin never fired — the two skipped branches deadlocked the reconvergence point")
			}
			if collected, ok := env.State.Outputs("rejoin")["out"].([]any); !ok || len(collected) != 3 {
				t.Errorf("rejoin out = %#v, want 3 entries (one per JoinAttrs.From, skipped branches contributing empty bags)", env.State.Outputs("rejoin")["out"])
			}
			// FR-005: the routing decision is on the EventLog.
			var sawChoice bool
			_ = log.Replay(env.RunID, func(e Event) error {
				if e.Kind == EventRouterChoice {
					sawChoice = true
				}
				return nil
			})
			if !sawChoice {
				t.Error("no router_choice event in the run log")
			}
		})
	}
}

// TestKernel_NonEdgeRouterRouterPromotesEveryBranch is the router's
// half of TestKernel_NonEdgeRouterExecutorPromotesEveryBranch: an
// override that does not implement EdgeRouter gets unconditional
// promotion, whatever it writes. The reachable in-executor fallback is
// covered separately by
// TestRouterExecutor_MissingChoiceFallbackIsReachable.
func TestKernel_NonEdgeRouterRouterPromotesEveryBranch(t *testing.T) {
	t.Parallel()
	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = "seed"
		return res, nil
	}
	log := NewMemoryEventLog()
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		if k == NodeKindJoin {
			continue
		}
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	k := NewKernel(opts...)
	g := routerLivenessGraph()
	env := &Env{RunID: "run-router-no-choice", Graph: g}
	applyEnvDefaults(env)
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, id := range []string{"do_research", "do_draft", "do_done"} {
		if got := rt.fires(id); got != 1 {
			t.Errorf("%s fired %d times, want 1 — a non-EdgeRouter executor gets unconditional promotion", id, got)
		}
	}
	if got := skippedIDs(t, log, env.RunID); len(got) != 0 {
		t.Errorf("node_skipped = %v, want none", got)
	}
}

// TestValidate_RouterRoutingAttrs covers the WP11a validator rule.
// `choices` is load-bearing at run time — the kernel promotes the
// winning choice's target and skips the rest — so a typo there is a
// silent mis-route, the same failure WP02c closed for `decision`.
func TestValidate_RouterRoutingAttrs(t *testing.T) {
	t.Parallel()
	base := func(mutate func(*Graph)) Graph {
		g := *routerLivenessGraph()
		g.Nodes = append([]Node(nil), g.Nodes...)
		g.Edges = append([]Edge(nil), g.Edges...)
		mutate(&g)
		return g
	}
	routerAttrs := func(g *Graph) RouterAttrs {
		a := g.Nodes[1].Attrs.(RouterAttrs)
		a.Choices = map[string]any{
			"research": map[string]any{"target": "do_research"},
			"draft":    map[string]any{"target": "do_draft"},
			"done":     map[string]any{"target": "do_done"},
		}
		return a
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*Graph)
		mustHas string
	}{
		{
			name: "choice targets an unknown node",
			mutate: func(g *Graph) {
				a := routerAttrs(g)
				a.Choices["draft"] = map[string]any{"target": "nowhere"}
				g.Nodes[1].Attrs = a
			},
			mustHas: "targets unknown node",
		},
		{
			name: "default_choice is not in the menu",
			mutate: func(g *Graph) {
				a := routerAttrs(g)
				a.DefaultChoice = "teleport"
				g.Nodes[1].Attrs = a
			},
			mustHas: "is not one of the declared choices",
		},
		{
			name: "fused mode without a source node",
			mutate: func(g *Graph) {
				a := routerAttrs(g)
				a.SourceNode = ""
				g.Nodes[1].Attrs = a
			},
			mustHas: "requires source_node",
		},
		{
			name: "choice port edge disagrees with the menu",
			mutate: func(g *Graph) {
				g.Edges[1] = Edge{From: EndpointRef{Node: "route", Port: "research"}, To: EndpointRef{Node: "do_draft", Port: "in"}}
			},
			mustHas: "must agree",
		},
		{
			name: "replan_choice is not in the menu",
			mutate: func(g *Graph) {
				a := routerAttrs(g)
				a.ReplanChoice = "panic"
				g.Nodes[1].Attrs = a
			},
			mustHas: "replan_choice",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := base(tc.mutate)
			err := Validate(g)
			if err == nil {
				t.Fatalf("Validate: want an error containing %q, got nil", tc.mustHas)
			}
			ve, ok := err.(*ValidationError)
			if !ok || !ve.Has(tc.mustHas) {
				t.Fatalf("Validate: want an issue containing %q, got %v", tc.mustHas, err)
			}
		})
	}
}

// TestValidate_RouterRoutingAcceptsWellFormedGraph guards the new rule
// against false positives.
func TestValidate_RouterRoutingAcceptsWellFormedGraph(t *testing.T) {
	t.Parallel()
	if err := Validate(*routerLivenessGraph()); err != nil {
		t.Fatalf("Validate(router graph): %v", err)
	}
}
