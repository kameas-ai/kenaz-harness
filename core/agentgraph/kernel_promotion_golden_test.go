package agentgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// kernel_promotion_golden_test.go — blast-radius pin for
// agentgraph-total-convergence-01PMGX01 Phase 1.5 (WP02b/WP02c).
//
// WP02b changes the kernel's promotion loop: an out-edge only satisfies
// a downstream dependency when the source port delivered a live value,
// and a node whose every inbound edge was skipped is itself skipped
// (transitively). That is a change to *execution semantics for every
// graph*, so the mission's acceptance criterion is "existing graphs
// produce byte-identical output, pinned by golden test before the
// change".
//
// This file is that pin. It walks every graph artifact the repo ships —
// the two library graphs the product actually runs
// (core/rpc/views/agentgraph/library), all eight bundled activities
// (core/agentgraph/activities/yaml), and the kernel's own multi-node
// testdata fixtures — runs each one through a real Kernel, and records
// the traversal: which nodes fired, in what order, with which output
// ports, which nodes were skipped, and how the run terminated.
//
// The recorded goldens are literals in wantPromotionGoldens below. They
// were captured on the pre-WP02b tree and MUST NOT be edited to
// accommodate the change: a diff here means the promotion rewrite moved
// a graph the product ships.
//
// Fidelity note: leaf/compute kinds are stubbed (they need LLM, tool,
// memory and filesystem seams that have nothing to do with promotion),
// but every *control-flow* kind runs its real executor — loop, retry,
// parallel, join, decision, branch, merge. So loop bodies really
// iterate, parallel really fans out, and `decision` really evaluates
// its condition expression. What is stubbed is exactly the part
// promotion does not depend on.

// keepRealKinds are the node kinds whose production executors run
// during the golden capture. They are the control-flow family: the
// kinds that decide what else executes. Stubbing these would make the
// golden vacuous.
// `branch` and `merge` are deliberately NOT in this set: their
// production executors are themselves stubs (see the exec_control.go
// header) that need fork/merge seams to do anything, so running them
// for real only injects seam errors that mask the traversal being
// pinned.
var keepRealKinds = map[NodeKind]bool{
	NodeKindDecision: true,
	NodeKindLoop:     true,
	NodeKindRetry:    true,
	NodeKindParallel: true,
	NodeKindJoin:     true,
}

// stubPortValue returns a deterministic, type-appropriate value for one
// declared output port. Types matter: `loop`/`decision` evaluate real
// condition expressions against these values (chat_default's
// `tool_call_count > 0`, default_toolloop's `finish_reason ==
// "tool_use"`), so a blanket string would make every numeric condition
// an evaluator error and the golden would pin a crash instead of a
// traversal.
func stubPortValue(nodeID string, p Port) any {
	switch p.Type {
	case PortType("bool"):
		return false
	case PortType("number"):
		return 0
	case PortType("messages"):
		return []Message{}
	default:
		return "stub:" + nodeID + ":" + p.Name
	}
}

// promotionRecorder is the race-safe fake the stub executors write to.
// The kernel fires nodes from bare goroutines, so every field is
// mutex-guarded and every test-side read goes through a snapshot helper
// (CLAUDE.md "Race-safe test fakes").
type promotionRecorder struct {
	mu    sync.Mutex
	fired []string
	ports map[string][]string
}

func newPromotionRecorder() *promotionRecorder {
	return &promotionRecorder{ports: map[string][]string{}}
}

func (r *promotionRecorder) record(nodeID string, outs PortValues) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fired = append(r.fired, nodeID)
	names := make([]string, 0, len(outs))
	for name := range outs {
		names = append(names, name)
	}
	sort.Strings(names)
	r.ports[nodeID] = names
}

// snapshotFired returns the observed fire order.
func (r *promotionRecorder) snapshotFired() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.fired))
	copy(out, r.fired)
	return out
}

// stubExecutor stands in for one non-control node kind. It emits every
// output port the node declares (explicit `outputs:` on the node, else
// the manifest default surface) carrying a deterministic marker value,
// so downstream promotion sees exactly the port surface the graph
// author wired against.
type stubExecutor struct {
	kind NodeKind
	rec  *promotionRecorder
}

func (s stubExecutor) Kind() NodeKind { return s.kind }

func (s stubExecutor) Execute(_ context.Context, _ *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	_, outs := portSurface(node)
	for _, p := range outs {
		res.Outputs[p.Name] = stubPortValue(node.ID, p)
	}
	// The manifest under-declaration patch that used to live here is
	// gone: agentgraph-total-convergence-01PMGX01 Phase 2 reconciled
	// tool_dispatch.yaml with what exec_dispatch.go actually emits
	// (manifest_version 1.1.0), so `tool_call_count` — the port
	// chat_default's agent_loop breaks on — now comes from the declared
	// surface like every other port.
	//
	// Consequence worth stating: `assistant_text` is now declared too,
	// so this file no longer incidentally pins the "downstream edge
	// sourced from a port the upstream did not produce still promotes"
	// property. That property moved to a dedicated, non-incidental test
	// — TestKernel_AbsentSourcePortStillPromotes in
	// kernel_liveness_test.go — rather than being lost.
	s.rec.record(node.ID, res.Outputs)
	return res, nil
}

// goldenKernel builds a Kernel whose non-control kinds are all stubbed
// against rec.
func goldenKernel(rec *promotionRecorder, log EventLog) *Kernel {
	// NOTE: the kernel's default maxInFlight (8) is used deliberately.
	// WithMaxInFlight(1) cannot be used to serialise the trace: a
	// dispatch goroutine holds its own semaphore slot while blocking on
	// `sem <- struct{}{}` to launch each child, so a cap of 1 deadlocks
	// any chain of two or more nodes. That is a pre-existing kernel
	// bug, unrelated to WP02b, recorded here so the next reader does
	// not rediscover it the hard way. Determinism instead comes from
	// the graphs themselves: every bundled graph is a chain at the
	// kernel level, so exactly one node is ready at a time.
	opts := []KernelOption{WithEventLog(log)}
	for _, k := range AllNodeKinds() {
		if keepRealKinds[k] {
			continue
		}
		opts = append(opts, WithExecutor(stubExecutor{kind: k, rec: rec}))
	}
	return NewKernel(opts...)
}

// promotionTrace is the golden shape: what fired, what was skipped, and
// how the run ended.
type promotionTrace struct {
	Fired   []string
	Skipped []string
	Outcome string
}

func (t promotionTrace) String() string {
	return fmt.Sprintf("fired=[%s] skipped=[%s] outcome=%s",
		strings.Join(t.Fired, " "), strings.Join(t.Skipped, " "), t.Outcome)
}

// routingOnSuffix marks a golden key as "run this graph with the
// agentic-turn-routing flag ON" (agentgraph-total-convergence-01PMGX01
// WP11b). Every other key runs with the flag OFF, which is how the
// product ships — see GateAgenticTurnRouting.
//
// Both positions of a shipped flag get pinned. Pinning only the default
// would leave the opt-in path — the one a soak is supposed to exercise
// — with no golden at all, and pinning only the routed one would leave
// the path 100% of users are on unpinned.
const routingOnSuffix = "#routing_on"

// runPromotionTrace loads one graph file and returns its traversal.
func runPromotionTrace(t *testing.T, key string) promotionTrace {
	t.Helper()
	path := strings.TrimSuffix(key, routingOnSuffix)
	routingEnabled := path != key
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, err := LoadYAML(data)
	if err != nil {
		return promotionTrace{Outcome: "load_error"}
	}
	// The gate is applied here rather than in the chat surface because
	// this file pins what the KERNEL traverses, and after WP11b what
	// the kernel traverses for chat is the gate's output, never the raw
	// YAML. A golden that read the raw file would be pinning a graph
	// the product does not run.
	g = GateAgenticTurnRouting(g, routingEnabled)
	rec := newPromotionRecorder()
	log := NewMemoryEventLog()
	k := goldenKernel(rec, log)
	env := &Env{
		RunID:     "golden-" + filepath.Base(key),
		SessionID: "golden",
		Graph:     &g,
	}
	applyEnvDefaults(env)
	runErr := k.Run(context.Background(), env)

	outcome := "ok"
	switch {
	case runErr == nil:
	case errors.Is(runErr, ErrPaused):
		outcome = "paused"
	case errors.Is(runErr, ErrBudgetExceeded):
		outcome = "budget_exceeded"
	default:
		outcome = "error"
	}
	return promotionTrace{
		Fired:   rec.snapshotFired(),
		Skipped: skippedNodeIDs(t, log, env.RunID),
		Outcome: outcome,
	}
}

// skippedNodeIDs reads the node_skipped events out of the run log. On
// the pre-WP02b tree the kind does not exist yet and this is always
// empty — which is itself the point of the pin: no bundled graph may
// start skipping nodes as a side effect of the promotion rewrite.
func skippedNodeIDs(t *testing.T, log EventLog, runID string) []string {
	t.Helper()
	var out []string
	_ = log.Replay(runID, func(e Event) error {
		if string(e.Kind) == "node_skipped" && e.NodeID != "" {
			out = append(out, e.NodeID)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// wantPromotionGoldens is the captured pre-change traversal for every
// graph artifact the repo ships. Captured 2026-08-12 on the commit
// immediately before WP02b. Do not regenerate to make a failure go
// away — a diff means the promotion change moved a shipped graph.
var wantPromotionGoldens = map[string]promotionTrace{
	// ---- Product graphs (core/rpc/views/agentgraph/library) ----
	//
	// chat_default is the graph every chat turn runs. It is linear at
	// the kernel level — history_in -> ask_user -> agent_loop ->
	// assistant_write — because assistant_turn and tool_dispatch live
	// inside agent_loop's body and buildEdges filters body nodes out.
	// The mission brief calls for proving this rather than assuming it;
	// this entry is that proof.
	// The full chain fires, including assistant_write — even though
	// agent_loop's flattened outputs do NOT contain `assistant_text`,
	// the port its inbound edge is sourced from. That absence is the
	// single most important fact this file pins: a promotion rule that
	// derived liveness from raw port presence would skip
	// assistant_write here and silently stop persisting every chat
	// reply. See the WP02b comment block in kernel.go.
	//
	// GOLDEN UPDATED, DELIBERATELY (agentgraph-total-convergence-01PMGX01
	// WP08, spec §4.4): compact_history joins the trace between
	// history_in and ask_user. This is the only bundled graph that
	// changed, and the change is the WP's entire point — compaction
	// stops being a pre-kernel pass the kernel cannot see and becomes a
	// node in the graph, where the promotion trace can pin it like any
	// other. A future edit that removes compact_history from this list
	// is removing chat compaction.
	//
	// It fires unconditionally rather than conditionally, which is
	// correct: the node always runs, and whether it compacts anything is
	// the resolved config's and the strategy's decision, not the
	// kernel's. With no Compactor wired (this test's Env) it passes its
	// input through and records a skip event.
	//
	// GOLDEN UNCHANGED ACROSS WP11b, WHICH IS THE POINT. WP11b rewrote
	// chat_default.yaml in place for the routed turn (`route` in the
	// loop body, `exit_gate` between the loop and the writer), behind a
	// settings flag that ships OFF. This entry is the flag's OFF
	// position, and it is byte-identical to the trace recorded before
	// the router existed — which is the whole claim the gate makes.
	"../rpc/views/agentgraph/library/chat_default.yaml": {
		Fired:   []string{"history_in", "compact_history", "ask_user", "assistant_turn", "tool_dispatch", "assistant_write"},
		Skipped: nil,
		Outcome: "ok",
	},
	// The flag's ON position — the routed turn (WP11b, 01PMAG01
	// §3.1/§3.4/§3.6). Two nodes join the trace and the justification
	// for each is the mission:
	//
	//   route      — the model states its next step. It is a Loop BODY
	//                node, so it fires once per iteration; the loop
	//                condition reads the `next` it sets. Here the stub
	//                emits an unparseable `next`, so the loop exits
	//                after one iteration — which is the correct safe
	//                direction and is exactly what `default_choice:
	//                done` guarantees against a model that ignores the
	//                menu.
	//   exit_gate  — the verified exit. Before it, "the model stopped
	//                calling tools" was the entire completion criterion
	//                (01PMAG01 G1).
	//
	//   replan_check — a real `decision`, so it never lands in Fired
	//                (the golden records stubbed kinds; control-flow
	//                kinds run for real). It reads the router's choice
	//                off agent_loop's flattened outputs.
	//   recover    — the escalation ladder, reached only on a replan
	//                route. It is SKIPPED here, and that skip is the
	//                headline: the doom-loop consumer costs nothing on
	//                every turn where no thrash was detected, which is
	//                nearly all of them. Before Phase 1.5's conditional
	//                promotion this node would have fired on every
	//                single turn with a missing input port.
	//
	// The stub `route` emits an unparseable `next`, so replan_check's
	// condition is false and the not-taken branch is skipped — which is
	// also the first time a graph the repo ships records a node_skipped
	// event at all.
	"../rpc/views/agentgraph/library/chat_default.yaml" + routingOnSuffix: {
		Fired:   []string{"history_in", "compact_history", "ask_user", "assistant_turn", "tool_dispatch", "route", "exit_gate", "assistant_write"},
		Skipped: []string{"recover"},
		Outcome: "ok",
	},
	// toolloop_default's decision (`continue_check`) lives inside
	// pump_loop's body, so buildEdges hides it from the kernel and
	// pump_loop itself is never promoted (no in-edges, not an
	// entrypoint). Only history_in fires. This is why no bundled graph
	// changes under WP02b: the repo ships no kernel-visible decision.
	"../rpc/views/agentgraph/library/toolloop_default.yaml": {
		Fired:   []string{"history_in"},
		Skipped: nil,
		Outcome: "ok",
	},

	// ---- Bundled activities (core/agentgraph/activities/yaml) ----
	"activities/yaml/ask.yaml":       {Fired: []string{"asker"}, Outcome: "ok"},
	"activities/yaml/decompose.yaml": {Fired: []string{"decomposer", "normalize"}, Outcome: "ok"},
	// delegate is new in agentgraph-total-convergence-01PMGX01 WP04 — the
	// shipped exercise for the two builtin-tool kinds. It is a two-node
	// chain, so the pin is the chain firing in order.
	"activities/yaml/delegate.yaml": {Fired: []string{"dispatch", "settle"}, Outcome: "ok"},
	"activities/yaml/plan.yaml":      {Fired: []string{"planner"}, Outcome: "ok"},
	"activities/yaml/reflect.yaml":   {Fired: []string{"reflector"}, Outcome: "ok"},
	"activities/yaml/retrieve.yaml":  {Fired: []string{"reader"}, Outcome: "ok"},
	"activities/yaml/review.yaml":    {Fired: []string{"reviewer"}, Outcome: "ok"},
	"activities/yaml/summarize.yaml": {Fired: []string{"summarizer", "clip"}, Outcome: "ok"},
	"activities/yaml/validate.yaml":  {Fired: []string{"normalize", "reviewer"}, Outcome: "ok"},

	// ---- Kernel testdata fixtures ----
	// branch_v1_simple pins a reconvergence node that must NOT fire:
	// `rejoin` has two in-edges and `branch_lookup` is unreachable
	// (no in-edges, not an entrypoint), so rejoin's in-degree never
	// closes. Under WP02b it must still never fire — and must not be
	// skipped either, since no inbound edge ever arrived at all.
	"testdata/branch_v1_simple.yaml": {Fired: []string{"trunk_question", "spawn_branch"}, Outcome: "ok"},
	// The frozen pre-WP11b copy of chat_default, and the gate's
	// fail-closed fallback (review finding B2) — which is why it lives
	// in graphs/ rather than testdata/: the binary embeds it. Its trace
	// is the same list as chat_default's OFF position above, and that
	// is the assertion: the gate's output traverses like the real
	// classic graph, not merely like something plausible.
	"graphs/chat_default_classic.yaml": {
		Fired:   []string{"history_in", "compact_history", "ask_user", "assistant_turn", "tool_dispatch", "assistant_write"},
		Skipped: nil,
		Outcome: "ok",
	},
	"testdata/default_toolloop.yaml": {Fired: []string{"history_in"}, Outcome: "ok"},
	"testdata/reflect_loop.yaml":     {Fired: []string{"draft"}, Outcome: "ok"},
}

// TestPromotionGolden_BundledGraphsTraverseIdentically is the WP02b
// blast-radius gate. Every shipped graph must traverse exactly as it
// did before conditional promotion existed.
func TestPromotionGolden_BundledGraphsTraverseIdentically(t *testing.T) {
	t.Parallel()
	paths := make([]string, 0, len(wantPromotionGoldens))
	for p := range wantPromotionGoldens {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			got := runPromotionTrace(t, path)
			want := wantPromotionGoldens[path]
			if got.String() != want.String() {
				t.Errorf("promotion trace drifted for %s\n got: %s\nwant: %s",
					path, got.String(), want.String())
			}
		})
	}
}

// TestPromotionGolden_CoversEveryBundledGraph keeps the golden set
// honest: a new library graph, activity, or kernel fixture must be
// added to wantPromotionGoldens, not silently escape the pin.
func TestPromotionGolden_CoversEveryBundledGraph(t *testing.T) {
	t.Parallel()
	dirs := []string{
		"../rpc/views/agentgraph/library",
		"activities/yaml",
		"graphs",
		"testdata",
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			key := dir + "/" + e.Name()
			if _, ok := wantPromotionGoldens[key]; !ok {
				t.Errorf("graph %s is not pinned in wantPromotionGoldens — add it before changing promotion", key)
			}
		}
	}
}
