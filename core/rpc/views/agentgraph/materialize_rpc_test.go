package agentgraph_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// materializeRPCGraph is a minimal two-node graph whose executors need
// no seams, so the run completes with only the kernel's own events.
func materializeRPCGraph() coreag.Graph {
	return coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          "materialize_rpc",
		Name:        "Materialize RPC fixture",
		Entrypoints: []string{"first"},
		Nodes: []coreag.Node{
			{ID: "first", Kind: coreag.NodeKindTransform, Title: "First", Attrs: coreag.TransformAttrs{Name: "concat"}},
			{ID: "second", Kind: coreag.NodeKindTransform, Title: "Second", Attrs: coreag.TransformAttrs{Name: "concat"}},
		},
		Edges: []coreag.Edge{
			{From: coreag.EndpointRef{Node: "first", Port: "out"}, To: coreag.EndpointRef{Node: "second", Port: "in"}},
		},
	}
}

// TestMaterializeRun_TracksExternalChatRun is the wiring proof for the
// chat path: a run the manager did not start is materializable because
// the chat runner registered its resolved spec through the same seam
// core/rpc/api.go wires to ChatRunner.RunSpecRecorder.
func TestMaterializeRun_TracksExternalChatRun(t *testing.T) {
	t.Parallel()
	log := coreag.NewMemoryEventLog()
	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	api := graphview.New(mgr)

	g := materializeRPCGraph()
	env := &coreag.Env{RunID: "chat-7", Graph: &g}
	// This is exactly what the chat runner does: register the resolved
	// spec, then drive the manager's shared kernel directly.
	mgr.TrackExternalRun(env.RunID, g)
	if err := mgr.Kernel().Run(context.Background(), env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}

	spec, err := api.MaterializeRun(context.Background(), "chat-7")
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	if spec.Scope != "materialized" {
		t.Errorf("scope = %q, want materialized (the editor's read-only trigger)", spec.Scope)
	}
	if !strings.Contains(spec.ID, "chat-7") {
		t.Errorf("materialized graph id %q does not name the run", spec.ID)
	}
	mg, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		t.Fatalf("returned YAML does not parse: %v", err)
	}
	if err := coreag.Validate(mg); err != nil {
		t.Fatalf("returned YAML does not validate: %v", err)
	}
	ids := map[string]bool{}
	for _, n := range mg.Nodes {
		ids[n.ID] = true
		if n.Materialized == nil {
			t.Errorf("node %s has no materialization record", n.ID)
		}
	}
	for _, want := range []string{"first@1", "second@1"} {
		if !ids[want] {
			t.Errorf("missing materialized instance %q", want)
		}
	}
}

// TestMaterializeRun_UnknownRun keeps the failure honest: an unknown run
// errors rather than returning an empty graph that looks like a run
// which did nothing.
func TestMaterializeRun_UnknownRun(t *testing.T) {
	t.Parallel()
	mgr, err := graphview.NewManager()
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	api := graphview.New(mgr)
	if _, err := api.MaterializeRun(context.Background(), "nope"); err == nil {
		t.Error("expected an error for an unknown run")
	}
	if _, err := api.MaterializeRun(context.Background(), ""); err == nil {
		t.Error("expected an error for an empty run id")
	}
}

// TestMaterializeRun_NilManager keeps the empty-state contract.
func TestMaterializeRun_NilManager(t *testing.T) {
	t.Parallel()
	a := graphview.New(nil)
	if _, err := a.MaterializeRun(context.Background(), "r"); !errors.Is(err, graphview.ErrManagerUnavailable) {
		t.Errorf("MaterializeRun err = %v, want ErrManagerUnavailable", err)
	}
}

// TestTrackExternalRun_EvictionIsMarkedDegraded is review finding F2.
//
// The chat-run spec registry is bounded, so a run whose spec has been
// evicted falls back to the library file the run_start event names.
// That fallback is a DEGRADED answer — the routing gate rewrites
// chat_default in place and the max-turns dial overrides the loop cap
// before a run starts, so the library file can describe a different
// graph than the one that executed — and serving it unmarked would let
// a viewer read a routed run as a classic one with nothing to notice.
//
// This test asserts the marker in both directions, so it fails if the
// marker is dropped AND if it is stamped on a faithful projection.
func TestTrackExternalRun_EvictionIsMarkedDegraded(t *testing.T) {
	t.Parallel()
	log := coreag.NewMemoryEventLog()
	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	api := graphview.New(mgr)
	src, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec: %v", err)
	}

	const runID = "chat-evicted"
	ev := &fakeTurnEvents{runID: runID}
	ev.add("", coreag.EventRunStart, map[string]any{"graph_id": "chat_default"})
	ev.fire("history_in", "history_read")
	ev.fire("compact_history", "compact")
	ev.fire("ask_user", "ask")
	ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 3})
	if _, err := log.Append(ev.batch); err != nil {
		t.Fatalf("append: %v", err)
	}

	// While the spec is tracked, the projection is faithful and must
	// carry NO degraded marker.
	mgr.TrackExternalRun(runID, src)
	faithful, err := api.MaterializeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("MaterializeRun (tracked): %v", err)
	}
	if strings.Contains(faithful.YAML, coreag.SpecProvenanceLibraryFallback) {
		t.Error("a faithful projection was marked as a library fallback")
	}
	if strings.Contains(faithful.YAML, "DEGRADED") {
		t.Error("a faithful projection carries a degraded warning in its description")
	}

	// Flood the registry past its bound to evict this run's spec.
	for i := 0; i < 200; i++ {
		mgr.TrackExternalRun(fmt.Sprintf("filler-%d", i), src)
	}

	degraded, err := api.MaterializeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("MaterializeRun (evicted): %v", err)
	}
	mg, err := coreag.LoadYAML([]byte(degraded.YAML))
	if err != nil {
		t.Fatalf("degraded projection does not parse: %v", err)
	}
	if mg.SpecProvenance != coreag.SpecProvenanceLibraryFallback {
		t.Errorf("evicted run projected with spec_provenance %q, want %q — a degraded projection served as if it were faithful",
			mg.SpecProvenance, coreag.SpecProvenanceLibraryFallback)
	}
	if !strings.Contains(mg.Description, "DEGRADED") {
		t.Errorf("evicted run's description does not warn the reader:\n%s", mg.Description)
	}
	if !strings.Contains(mg.Description, "dial overrides") {
		t.Error("the degraded warning does not say WHAT is unrecoverable")
	}
	// It is still a usable graph — degraded, not broken.
	if err := coreag.Validate(mg); err != nil {
		t.Fatalf("degraded projection does not validate: %v", err)
	}
}

// ── the real chat_default topology ───────────────────────────────────
//
// The synthetic fixtures above pin the projection's mechanics. This one
// pins it against the graph every chat turn actually runs, because that
// is where the hard part lives: chat_default carries a fused `router`
// whose choice targets are node ids, a `decision` whose next_true /
// next_false are node ids, an `escalation_ladder` and a `review` that
// both name `agent_loop` as their upstream node. Every one of those
// references has to be repointed at a materialized instance or the
// projected graph does not validate — and none of them are exercised by
// a hand-built two-node fixture.
//
// The event stream is synthesized rather than produced by a live run:
// driving the real chat graph needs the entire chassis (LLM registry,
// tool pool, history writer, ask bus, compaction pipeline). What is
// under test is the projection, and its only input is the log.
type fakeTurnEvents struct {
	batch coreag.EventBatch
	runID string
}

func (f *fakeTurnEvents) add(nodeID string, kind coreag.EventKind, payload map[string]any) {
	_ = f.batch.AppendKind(f.runID, nodeID, kind, payload)
}

func (f *fakeTurnEvents) fire(nodeID, kind string) {
	f.add(nodeID, coreag.EventNodeStart, map[string]any{"kind": kind})
	f.add(nodeID, coreag.EventNodeComplete, map[string]any{"outputs": 2})
}

func (f *fakeTurnEvents) bodyFire(nodeID, kind, container string, iteration int, inner func()) {
	f.add(nodeID, coreag.EventNodeStart, map[string]any{
		"kind": kind, "iteration": iteration, "container": container,
	})
	if inner != nil {
		inner()
	}
	f.add(nodeID, coreag.EventNodeComplete, map[string]any{
		"outputs": 4, "iteration": iteration, "container": container,
	})
}

func TestMaterializeRun_RealChatDefaultTopology(t *testing.T) {
	t.Parallel()
	log := coreag.NewMemoryEventLog()
	mgr, err := graphview.NewManager(graphview.WithEventLog(log))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	src, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec(chat_default): %v", err)
	}
	if err := coreag.Validate(src); err != nil {
		t.Fatalf("bundled chat_default does not validate: %v", err)
	}

	const runID = "chat-42"
	ev := &fakeTurnEvents{runID: runID}
	ev.add("", coreag.EventRunStart, map[string]any{"graph_id": "chat_default"})
	ev.fire("history_in", "history_read")
	ev.fire("compact_history", "compact")
	ev.fire("ask_user", "ask")

	ev.add("agent_loop", coreag.EventNodeStart, map[string]any{"kind": "loop"})
	// Iteration 1: the model calls two tools, then routes `continue`.
	ev.bodyFire("assistant_turn", "model", "agent_loop", 1, func() {
		ev.add("assistant_turn", coreag.EventLLMCall, map[string]any{
			"provider": "anthropic", "model": "claude", "tokens": 120,
			"cost_usd": 0.004, "finish_reason": "tool_use", "tool_calls": 2,
		})
	})
	ev.bodyFire("tool_dispatch", "tool_dispatch", "agent_loop", 1, func() {
		for _, c := range []struct{ id, tool, arg string }{
			{"toolu_a1", "kenaz__read_file", "/etc/passwd"},
			{"fs_b2", "fs__list_dir", "/var/secret"},
		} {
			ev.add("tool_dispatch", coreag.EventToolCall, map[string]any{
				"tool": c.tool, "call_id": c.id, "node_kind": "tool_dispatch",
				"args": map[string]any{"path": c.arg, "limit": 5},
			})
			ev.add("tool_dispatch", coreag.EventToolResult, map[string]any{
				"tool": c.tool, "call_id": c.id, "node_kind": "tool_dispatch",
				"is_error": false, "bytes": 128,
			})
		}
	})
	ev.bodyFire("route", "router", "agent_loop", 1, func() {
		ev.add("route", coreag.EventRouterChoice, map[string]any{
			"choice": "continue", "mode": "fused", "source": "source_node",
			"consecutive": 1, "target": "assistant_turn",
		})
	})
	// Iteration 2: the model answers and routes `done`.
	ev.bodyFire("assistant_turn", "model", "agent_loop", 2, func() {
		ev.add("assistant_turn", coreag.EventLLMCall, map[string]any{
			"provider": "anthropic", "model": "claude", "tokens": 60,
			"finish_reason": "stop", "tool_calls": 0,
		})
	})
	ev.bodyFire("tool_dispatch", "tool_dispatch", "agent_loop", 2, nil)
	ev.bodyFire("route", "router", "agent_loop", 2, func() {
		ev.add("route", coreag.EventRouterChoice, map[string]any{
			"choice": "done", "mode": "fused", "source": "source_node",
			"consecutive": 1, "target": "exit_gate",
		})
	})
	ev.add("agent_loop", coreag.EventNodeComplete, map[string]any{"outputs": 8})

	ev.fire("replan_check", "decision")
	// The recovery ladder was the not-taken side of the decision.
	ev.add("recover", coreag.EventNodeSkipped, map[string]any{
		"kind": "escalation_ladder", "skipped_by": "replan_check", "dead_inputs": 1,
	})
	ev.fire("exit_gate", "review")
	ev.fire("assistant_write", "session_write")
	ev.add("", coreag.EventRunComplete, map[string]any{"completed_nodes": 12, "skipped_nodes": 1})
	if _, err := log.Append(ev.batch); err != nil {
		t.Fatalf("append events: %v", err)
	}

	mgr.TrackExternalRun(runID, src)
	spec, err := graphview.New(mgr).MaterializeRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	mg, err := coreag.LoadYAML([]byte(spec.YAML))
	if err != nil {
		t.Fatalf("materialized chat_default does not parse: %v", err)
	}
	if err := coreag.Validate(mg); err != nil {
		t.Fatalf("materialized chat_default does not validate: %v", err)
	}

	byID := map[string]coreag.Node{}
	for _, n := range mg.Nodes {
		byID[n.ID] = n
	}
	for _, want := range []string{
		"history_in@1", "compact_history@1", "ask_user@1", "agent_loop@1",
		"assistant_turn@1", "assistant_turn@2",
		"tool_dispatch@1", "tool_dispatch@2",
		"tool_dispatch@1[toolu_a1]", "tool_dispatch@1[fs_b2]",
		"route@1", "route@2", "replan_check@1", "recover@1",
		"exit_gate@1", "assistant_write@1",
	} {
		if _, ok := byID[want]; !ok {
			t.Errorf("missing materialized instance %q", want)
		}
	}

	// The router's menu narrowed to the choice each fire took, and its
	// target points at a real instance.
	r1, ok := byID["route@1"].Attrs.(coreag.RouterAttrs)
	if !ok {
		t.Fatalf("route@1 attrs type %T", byID["route@1"].Attrs)
	}
	if len(r1.Choices) != 1 || r1.DefaultChoice != "continue" {
		t.Errorf("route@1 choices = %v, default = %q; want the taken choice only", r1.Choices, r1.DefaultChoice)
	}
	if got, _ := r1.Choices["continue"].(map[string]any); got["target"] != "assistant_turn@2" {
		t.Errorf("route@1 continue target = %v, want assistant_turn@2", got["target"])
	}
	if r1.SourceNode != "assistant_turn@1" {
		t.Errorf("route@1 source_node = %q, want assistant_turn@1", r1.SourceNode)
	}
	r2, _ := byID["route@2"].Attrs.(coreag.RouterAttrs)
	if got, _ := r2.Choices["done"].(map[string]any); got["target"] != "exit_gate@1" {
		t.Errorf("route@2 done target = %v, want exit_gate@1", got["target"])
	}

	// The decision's successors resolve to instances, and the branch the
	// run did not take is visibly skipped rather than missing.
	d, _ := byID["replan_check@1"].Attrs.(coreag.DecisionAttrs)
	if d.NextTrue != "recover@1" || d.NextFalse != "exit_gate@1" {
		t.Errorf("replan_check@1 routing = (%q, %q), want (recover@1, exit_gate@1)", d.NextTrue, d.NextFalse)
	}
	if byID["recover@1"].Materialized.Status != coreag.MaterializedStatusSkipped {
		t.Errorf("recover@1 status = %q, want skipped", byID["recover@1"].Materialized.Status)
	}

	// The review + ladder upstream references point at the loop instance.
	rev, _ := byID["exit_gate@1"].Attrs.(coreag.ReviewAttrs)
	if rev.UpstreamNode != "agent_loop@1" {
		t.Errorf("exit_gate@1 upstream_node = %q, want agent_loop@1", rev.UpstreamNode)
	}
	lad, _ := byID["recover@1"].Attrs.(coreag.EscalationLadderAttrs)
	if lad.UpstreamNode != "agent_loop@1" {
		t.Errorf("recover@1 upstream_node = %q, want agent_loop@1", lad.UpstreamNode)
	}

	// Redaction holds against the real graph too.
	for _, secret := range []string{"/etc/passwd", "/var/secret"} {
		if strings.Contains(spec.YAML, secret) {
			t.Errorf("materialized chat_default leaked %q", secret)
		}
	}
}
