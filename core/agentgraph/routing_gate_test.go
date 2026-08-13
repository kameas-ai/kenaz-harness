package agentgraph

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// routing_gate_test.go — the load-bearing pin for
// agentgraph-total-convergence-01PMGX01 WP11b's launch gate.
//
// The routing flag ships OFF, so the graph every chat turn actually
// runs is GateAgenticTurnRouting's OUTPUT, not the authored YAML. These
// tests are what make that derived graph trustworthy.

const (
	chatDefaultPath = "../rpc/views/agentgraph/library/chat_default.yaml"
	chatClassicPath = "graphs/chat_default_classic.yaml"
)

func loadGraphFile(t *testing.T, path string) Graph {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	g, err := LoadYAML(data)
	if err != nil {
		t.Fatalf("LoadYAML %s: %v", path, err)
	}
	return g
}

// normalizedShape renders the parts of a graph that decide what the
// kernel does into a comparable, order-insensitive form. Identity
// fields that legitimately differ between the product graph and its
// frozen copy (id, name, description) are excluded on purpose: this
// compares TOPOLOGY plus the run-shaping fields, which is what the gate
// is responsible for.
//
// It returns a SORTED SLICE, not a set, and the diff below compares it
// as a multiset (review finding N7). A set-based comparison silently
// swallows duplicates — and a duplicated edge is not inert, it
// double-counts in the kernel's in-degree, so the target waits for two
// arrivals that only ever produce one. The bug this file exists to
// catch is exactly that class.
func normalizedShape(g Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		ports := func(ps []Port) string {
			names := make([]string, 0, len(ps))
			for _, p := range ps {
				names = append(names, p.Name+":"+string(p.Type))
			}
			sort.Strings(names)
			return strings.Join(names, ",")
		}
		out = append(out, fmt.Sprintf("node %s kind=%s in=[%s] out=[%s] attrs=%#v",
			n.ID, n.Kind, ports(n.Inputs), ports(n.Outputs), n.Attrs))
	}
	for _, e := range g.Edges {
		out = append(out, fmt.Sprintf("edge %s:%s -> %s:%s",
			e.From.Node, e.From.Port, e.To.Node, e.To.Port))
	}
	// N8: the run-shaping fields belong in the comparison too. A strip
	// that dropped an entrypoint or halved a budget would otherwise
	// pass a "node-for-node identical" test while changing what the
	// graph does.
	out = append(out, "entrypoints="+strings.Join(append([]string(nil), g.Entrypoints...), ","))
	out = append(out, fmt.Sprintf("budget=%#v", g.Budget))
	out = append(out, "system_prompt="+g.SystemPrompt)
	sort.Strings(out)
	return out
}

// diffShapes reports multiset differences between two normalized
// shapes, so a duplicated element shows up as a difference rather than
// being absorbed.
func diffShapes(got, want []string) (extra, missing []string) {
	count := map[string]int{}
	for _, l := range got {
		count[l]++
	}
	for _, l := range want {
		count[l]--
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for i := 0; i < count[k]; i++ {
			extra = append(extra, k)
		}
		for i := 0; i > count[k]; i-- {
			missing = append(missing, k)
		}
	}
	return extra, missing
}

// TestGateAgenticTurnRouting_OffRestoresTheClassicGraph is THE test for
// this WP. chat_default.yaml was rewritten in place; the flag ships
// off; therefore the strip must reproduce the pre-rewrite graph
// exactly, and "exactly" has to mean a diff rather than a vibe.
//
// testdata/chat_default_classic.yaml is a frozen copy of the real
// pre-rewrite file. If this fails, either the gate is wrong or a
// chat_default change was made on one side only — and which of those it
// is, is the question the failure exists to force.
func TestGateAgenticTurnRouting_OffRestoresTheClassicGraph(t *testing.T) {
	t.Parallel()
	stripped := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), false)
	classic := loadGraphFile(t, chatClassicPath)

	extra, missing := diffShapes(normalizedShape(stripped), normalizedShape(classic))
	for _, l := range extra {
		t.Errorf("gate(off) produced an element the classic graph does not have:\n  + %s", l)
	}
	for _, l := range missing {
		t.Errorf("gate(off) dropped an element the classic graph has:\n  - %s", l)
	}
}

// TestDiffShapes_ReportsDuplicates is the meta-test for N7: prove the
// comparison actually catches a duplicated element, because a
// set-based one does not and that is what shipped.
func TestDiffShapes_ReportsDuplicates(t *testing.T) {
	t.Parallel()
	got := []string{"edge a:x -> b:y", "edge a:x -> b:y"}
	want := []string{"edge a:x -> b:y"}
	extra, missing := diffShapes(got, want)
	if len(extra) != 1 || len(missing) != 0 {
		t.Errorf("diffShapes(dup) = extra %v, missing %v; want exactly one extra", extra, missing)
	}
}

// TestGateAgenticTurnRouting_OnIsTheAuthoredGraph pins that the ON
// position is a no-op. The routed topology is authored in YAML, so the
// enabled path must not "build" anything — if it ever starts
// transforming, there are two sources of truth for the routed shape
// again.
func TestGateAgenticTurnRouting_OnIsTheAuthoredGraph(t *testing.T) {
	t.Parallel()
	authored := loadGraphFile(t, chatDefaultPath)
	gated := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), true)
	if extra, missing := diffShapes(normalizedShape(gated), normalizedShape(authored)); len(extra)+len(missing) > 0 {
		t.Errorf("gate(on) modified the authored graph; it must be the identity\n +%v\n -%v", extra, missing)
	}
}

// TestGateAgenticTurnRouting_BothPositionsValidate guards the obvious
// own-goal: a gate position that produces a graph the validator
// rejects would fail at StartStream, in production, for every user on
// that side of the flag.
func TestGateAgenticTurnRouting_BothPositionsValidate(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		enabled := enabled
		t.Run(fmt.Sprintf("routing=%v", enabled), func(t *testing.T) {
			t.Parallel()
			g := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), enabled)
			if err := Validate(g); err != nil {
				t.Fatalf("Validate(chat_default, routing=%v): %v", enabled, err)
			}
		})
	}
}

// TestGateAgenticTurnRouting_DoesNotMutateItsInput pins the
// non-mutation contract. The chat surface loads the graph and hands it
// to the gate per StartStream; a gate that mutated a shared spec would
// corrupt the next turn.
func TestGateAgenticTurnRouting_DoesNotMutateItsInput(t *testing.T) {
	t.Parallel()
	g := loadGraphFile(t, chatDefaultPath)
	before := normalizedShape(g)
	_ = GateAgenticTurnRouting(g, false)
	if extra, missing := diffShapes(normalizedShape(g), before); len(extra)+len(missing) > 0 {
		t.Errorf("GateAgenticTurnRouting mutated the graph it was given\n +%v\n -%v", extra, missing)
	}
}

// TestGateAgenticTurnRouting_LeavesUnroutedGraphsAlone covers every
// other caller: an activity, a test fixture, toolloop_default. The gate
// is applied at chat graph-load time and must be a no-op for anything
// that never carried the routed turn.
func TestGateAgenticTurnRouting_LeavesUnroutedGraphsAlone(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		chatClassicPath,
		"../rpc/views/agentgraph/library/toolloop_default.yaml",
		"activities/yaml/review.yaml",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			g := loadGraphFile(t, path)
			if extra, missing := diffShapes(normalizedShape(GateAgenticTurnRouting(g, false)), normalizedShape(g)); len(extra)+len(missing) > 0 {
				t.Errorf("gate(off) changed a graph that never carried the routed turn\n +%v\n -%v", extra, missing)
			}
		})
	}
}

// TestGateAgenticTurnRouting_DriftFailsClosed is review finding B2.
//
// The gate used to log and return its INPUT when chat_default no longer
// had the shape it strips. In that state the input is the ROUTED graph,
// so "leave it alone" meant the flag silently degraded to ON while the
// user had it off — the one outcome the flag exists to prevent, and the
// opposite of what api.go's GraphLoader promises ("never to on"). A
// revert lever that stops working the moment someone edits the thing it
// reverts is not a lever.
//
// Each case below is a realistic drift, and the assertion is the SAFETY
// PROPERTY (no routed kinds survive, and the result still validates)
// rather than only shape-equality with the fallback, which would be
// tautological.
func TestGateAgenticTurnRouting_DriftFailsClosed(t *testing.T) {
	t.Parallel()
	routedKinds := map[NodeKind]bool{
		NodeKindRouter:           true,
		NodeKindReview:           true,
		NodeKindEscalationLadder: true,
	}
	for name, mutate := range map[string]func(*Graph){
		// B3's scenario: rename one of the four routed nodes. The old
		// guard did not look for replan_check at all, so this passed
		// the guard and then half-stripped into a graph that fails
		// Validate().
		"replan_check renamed": func(g *Graph) {
			for i := range g.Nodes {
				if g.Nodes[i].ID == chatReplanCheckNodeID {
					g.Nodes[i].ID = "replan_gate"
				}
			}
		},
		"exit_gate removed": func(g *Graph) {
			nodes := make([]Node, 0, len(g.Nodes))
			for _, n := range g.Nodes {
				if n.ID != chatExitGateNodeID {
					nodes = append(nodes, n)
				}
			}
			g.Nodes = nodes
		},
		"recover removed": func(g *Graph) {
			nodes := make([]Node, 0, len(g.Nodes))
			for _, n := range g.Nodes {
				if n.ID != chatRecoverNodeID {
					nodes = append(nodes, n)
				}
			}
			g.Nodes = nodes
		},
		"assistant_write renamed": func(g *Graph) {
			for i := range g.Nodes {
				if g.Nodes[i].ID == chatAssistantWriteNodeID {
					g.Nodes[i].ID = "history_out"
				}
			}
		},
	} {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g := loadGraphFile(t, chatDefaultPath)
			mutate(&g)

			out := GateAgenticTurnRouting(g, false)

			for _, n := range out.Nodes {
				if routedKinds[n.Kind] {
					t.Errorf("drift produced a graph still carrying routed kind %q (node %q) with the flag OFF", n.Kind, n.ID)
				}
			}
			if GraphUsesAgenticTurnRouting(out) {
				t.Error("drift degraded the flag to ON")
			}
			if err := Validate(out); err != nil {
				t.Errorf("drift produced a graph that does not validate: %v", err)
			}
			// And it is specifically the frozen classic graph, not
			// some half-stripped thing that merely happens to validate.
			classic := loadGraphFile(t, chatClassicPath)
			if extra, missing := diffShapes(normalizedShape(out), normalizedShape(classic)); len(extra)+len(missing) > 0 {
				t.Errorf("drift fallback is not the classic graph\n +%v\n -%v", extra, missing)
			}
		})
	}
}

// TestClassicChatGraph_EmbeddedAssetParsesAndValidates guards the
// fallback itself. It is compiled into the binary and only read on a
// drift path, so without this test a broken embed would be discovered
// by the first user to hit drift — at the exact moment they needed it
// to work.
func TestClassicChatGraph_EmbeddedAssetParsesAndValidates(t *testing.T) {
	t.Parallel()
	g, err := ClassicChatGraph()
	if err != nil {
		t.Fatalf("ClassicChatGraph: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Fatalf("Validate(embedded classic graph): %v", err)
	}
	if GraphUsesAgenticTurnRouting(g) {
		t.Error("the fail-closed fallback carries the routed turn — it is supposed to be the classic topology")
	}
	// It must equal the file on disk: the embed and the file the
	// equality test reads have to be the same artifact.
	onDisk := loadGraphFile(t, chatClassicPath)
	if extra, missing := diffShapes(normalizedShape(g), normalizedShape(onDisk)); len(extra)+len(missing) > 0 {
		t.Errorf("embedded classic graph differs from graphs/chat_default_classic.yaml\n +%v\n -%v", extra, missing)
	}
}

// TestGateAgenticTurnRouting_LockstepFieldsArePinned is N8: prove the
// equality comparison actually covers the run-shaping fields, not just
// nodes and edges. Plant a budget change and the diff must fail.
func TestGateAgenticTurnRouting_LockstepFieldsArePinned(t *testing.T) {
	t.Parallel()
	classic := loadGraphFile(t, chatClassicPath)
	for name, mutate := range map[string]func(*Graph){
		"budget":        func(g *Graph) { g.Budget.MaxLLMCallsPerRun = 999 },
		"entrypoints":   func(g *Graph) { g.Entrypoints = []string{"ask_user"} },
		"system_prompt": func(g *Graph) { g.SystemPrompt = "planted" },
	} {
		mutate := mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			planted := loadGraphFile(t, chatClassicPath)
			mutate(&planted)
			extra, missing := diffShapes(normalizedShape(planted), normalizedShape(classic))
			if len(extra)+len(missing) == 0 {
				t.Errorf("planting a %s change did not register as a difference — the comparison does not cover it", name)
			}
		})
	}
}

// TestGraphUsesAgenticTurnRouting reports the flag's effective position
// from the graph itself, which is what keeps the Env policies that must
// move WITH the topology (TaskStateArming) from being set independently
// and drifting out of step.
func TestGraphUsesAgenticTurnRouting(t *testing.T) {
	t.Parallel()
	authored := loadGraphFile(t, chatDefaultPath)
	if !GraphUsesAgenticTurnRouting(GateAgenticTurnRouting(authored, true)) {
		t.Error("routed graph not detected as routed")
	}
	if GraphUsesAgenticTurnRouting(GateAgenticTurnRouting(authored, false)) {
		t.Error("stripped graph detected as routed")
	}
}

// TestChatDefault_RoutedTurnHasAVerifiedExit asserts the SHAPE the
// mission is actually about, so a future edit cannot quietly return the
// graph to "the model stopped calling tools is the whole completion
// criterion" (01PMAG01 G1) while leaving the flag in place.
func TestChatDefault_RoutedTurnHasAVerifiedExit(t *testing.T) {
	t.Parallel()
	g := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), true)

	byID := map[string]*Node{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}

	gate, ok := byID[chatExitGateNodeID]
	if !ok {
		t.Fatal("routed chat_default has no exit_gate")
	}
	if gate.Kind != NodeKindReview {
		t.Errorf("exit_gate kind = %q, want %q", gate.Kind, NodeKindReview)
	}
	ra, ok := gate.Attrs.(ReviewAttrs)
	if !ok {
		t.Fatalf("exit_gate attrs = %T, want ReviewAttrs", gate.Attrs)
	}
	if ra.UpstreamNode != chatAgentLoopNodeID {
		t.Errorf("exit_gate.upstream_node = %q, want %q — a FAIL verdict must rewind into the loop", ra.UpstreamNode, chatAgentLoopNodeID)
	}
	if ra.MaxIterations <= 0 {
		t.Error("exit_gate has no iteration cap; a FAIL-looping gate would never return to the user")
	}

	route, ok := byID[chatRouteNodeID]
	if !ok {
		t.Fatal("routed chat_default has no route node")
	}
	ro, ok := route.Attrs.(RouterAttrs)
	if !ok {
		t.Fatalf("route attrs = %T, want RouterAttrs", route.Attrs)
	}
	// Fused mode is not a preference, it is FR-003/FR-004: routing must
	// cost no additional model call on the common path.
	if ro.Mode != routerModeFused {
		t.Errorf("route.mode = %q, want %q — standalone routing would add an LLM call to every chat turn", ro.Mode, routerModeFused)
	}
	if ro.SourceNode == "" {
		t.Error("route.source_node is empty; fused routing has nothing to read")
	}

	// The exit gate must sit BETWEEN the loop and the writer — not
	// beside it. An assistant_write still fed directly from agent_loop
	// would mean the gate's verdict decides nothing.
	for _, e := range g.Edges {
		if e.To.Node == chatAssistantWriteNodeID && e.From.Node != chatExitGateNodeID {
			t.Errorf("assistant_write is fed from %q, bypassing the exit gate", e.From.Node)
		}
	}
}

// TestChatDefault_DoomLoopSignalReachesTheLadder is WP11c's acceptance
// at the graph level: the path from tool_dispatch's `should_replan`
// output to a node that can actually do something about it exists, and
// every hop of it is wired.
//
// Before this the signal was emitted into the void — events.go called
// the consumer "a future ladder controller" and it was never built,
// while escalationLadderExecutor sat fully implemented and referenced
// by zero graphs (01PMAG01 G4). Asserting the CHAIN rather than any one
// hop is deliberate: each hop existing in isolation is exactly the
// state the mission found.
func TestChatDefault_DoomLoopSignalReachesTheLadder(t *testing.T) {
	t.Parallel()
	g := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), true)

	byID := map[string]*Node{}
	for i := range g.Nodes {
		byID[g.Nodes[i].ID] = &g.Nodes[i]
	}

	// Hop 1: the router declares a replan route, so it reads
	// should_replan at all.
	route, ok := byID[chatRouteNodeID]
	if !ok {
		t.Fatal("no route node")
	}
	ra := route.Attrs.(RouterAttrs)
	if ra.ReplanChoice == "" {
		t.Fatal("route declares no replan_choice; the doom-loop signal has no consumer")
	}
	replanTarget := routerTargets(routerChoices(ra))[ra.ReplanChoice]
	if replanTarget == "" {
		t.Fatalf("replan_choice %q names no target node", ra.ReplanChoice)
	}

	// Hop 2: the in-loop choice becomes kernel-visible branching. The
	// router lives in a Loop body, where the kernel hides its edges, so
	// a `decision` outside the loop is what turns `next` into a real
	// branch.
	dec, ok := byID[chatReplanCheckNodeID]
	if !ok {
		t.Fatal("no replan_check node; the router's choice never becomes kernel-visible branching")
	}
	if dec.Kind != NodeKindDecision {
		t.Fatalf("replan_check kind = %q, want %q", dec.Kind, NodeKindDecision)
	}
	da := dec.Attrs.(DecisionAttrs)
	if !strings.Contains(da.Condition, ra.ReplanChoice) {
		t.Errorf("replan_check condition %q does not test for the router's replan choice %q", da.Condition, ra.ReplanChoice)
	}

	// Hop 3: the true branch is the ladder, and it is the node the
	// router's replan choice names.
	if da.NextTrue != replanTarget {
		t.Errorf("replan_check.next_true = %q but the router's replan choice targets %q", da.NextTrue, replanTarget)
	}
	ladder, ok := byID[da.NextTrue]
	if !ok {
		t.Fatalf("replan_check.next_true names a node that does not exist: %q", da.NextTrue)
	}
	if ladder.Kind != NodeKindEscalationLadder {
		t.Fatalf("recovery node kind = %q, want %q", ladder.Kind, NodeKindEscalationLadder)
	}
	la := ladder.Attrs.(EscalationLadderAttrs)
	if la.UpstreamNode != chatAgentLoopNodeID {
		t.Errorf("ladder.upstream_node = %q, want %q — the retry rung must rewind into the loop that thrashed", la.UpstreamNode, chatAgentLoopNodeID)
	}

	// Hop 4: both branches reconverge on the exit gate, so a recovered
	// turn is still verified before it reaches the user.
	var fromLadder, fromDecision bool
	for _, e := range g.Edges {
		if e.To.Node != chatExitGateNodeID {
			continue
		}
		if e.From.Node == da.NextTrue {
			fromLadder = true
		}
		if e.From.Node == chatReplanCheckNodeID && e.From.Port == "false" {
			fromDecision = true
		}
	}
	if !fromLadder {
		t.Error("the ladder's result does not reach the exit gate; a recovered turn would bypass verification")
	}
	if !fromDecision {
		t.Error("the non-replan branch does not reach the exit gate")
	}
}

// TestRecoveryDraftReachesTheExitGate is review finding B1, and it is a
// CONTENT assertion on purpose.
//
// THE BUG. exit_gate.draft had three in-edges: replan_check's `false`
// port, recover's `result`, and a direct agent_loop:assistant_text.
// The kernel builds a node's inputs by walking its in-edges in
// DECLARATION ORDER with last-writer-wins (kernel.go), and the direct
// edge was both declared last and always present — so it silently
// shadowed the ladder's output on every recovery pass. The escalate and
// replan rungs made real, costed LLM calls, and the gate then judged
// the ORIGINAL failing draft instead of the revised one. The recovery
// path was decorative.
//
// WHY THE EXISTING TESTS MISSED IT. They counted calls. The ladder DID
// fire, the calls DID happen, the run DID complete — every count was
// right and the value was wrong. So this one asserts on the text that
// reaches the reviewer, through a real kernel, with the real decision,
// escalation_ladder and review executors.
func TestRecoveryDraftReachesTheExitGate(t *testing.T) {
	t.Parallel()

	const (
		failingDraft = "THE-ORIGINAL-FAILING-DRAFT"
		revised      = "THE-LADDERS-REVISED-DRAFT"
	)

	// The ladder's escalate rung calls the model; the exit gate calls
	// it next. Scripting them in order lets us assert exactly what the
	// gate was shown.
	llm := &stubLLM{responses: []LLMResponse{
		{Content: revised}, // escalate rung
		{Content: `{"verdict":"pass","reason":"recovered"}`}, // exit gate
	}}

	g := &Graph{
		SpecVersion: SpecVersion, ID: "recovery-draft-reaches-gate",
		Entrypoints: []string{"seed"},
		Nodes: []Node{
			{ID: "seed", Kind: NodeKindTransform, Attrs: TransformAttrs{Name: "uppercase"},
				Outputs: []Port{
					{Name: "out", Type: PortType("any")},
					{Name: "next", Type: PortType("text")},
					{Name: "assistant_text", Type: PortType("text")},
				}},
			{ID: "replan_check", Kind: NodeKindDecision,
				Inputs: []Port{
					{Name: "in", Type: PortType("any")},
					{Name: "next", Type: PortType("text")},
				},
				Attrs: DecisionAttrs{
					Condition: `next == "replan"`, NextTrue: "recover", NextFalse: "exit_gate",
				}},
			{ID: "recover", Kind: NodeKindEscalationLadder, Attrs: EscalationLadderAttrs{
				UpstreamNode: "seed", TargetModel: "strong", MaxRetries: 1,
			}},
			{ID: "exit_gate", Kind: NodeKindReview,
				Inputs: []Port{{Name: "draft", Type: PortType("any")}},
				Attrs:  ReviewAttrs{UpstreamNode: "seed", MaxIterations: 2},
			},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "seed", Port: "assistant_text"}, To: EndpointRef{Node: "replan_check", Port: "in"}},
			{From: EndpointRef{Node: "seed", Port: "next"}, To: EndpointRef{Node: "replan_check", Port: "next"}},
			{From: EndpointRef{Node: "replan_check", Port: "true"}, To: EndpointRef{Node: "recover", Port: "trigger"}},
			{From: EndpointRef{Node: "recover", Port: "result"}, To: EndpointRef{Node: "exit_gate", Port: "draft"}},
			{From: EndpointRef{Node: "replan_check", Port: "false"}, To: EndpointRef{Node: "exit_gate", Port: "draft"}},
		},
	}

	rt := newScriptedRuntime()
	rt.behaviour["seed"] = func(int) (Result, error) {
		res := NewResult()
		res.Outputs["out"] = failingDraft
		res.Outputs["next"] = "replan"
		res.Outputs["assistant_text"] = failingDraft
		return res, nil
	}
	// decision, escalation_ladder and review all run for real; only the
	// leaf that produces the draft is scripted.
	opts := []KernelOption{}
	for _, k := range AllNodeKinds() {
		switch k {
		case NodeKindDecision, NodeKindEscalationLadder, NodeKindReview:
			continue
		}
		opts = append(opts, WithExecutor(scriptedKindExecutor{kind: k, rt: rt}))
	}
	k := NewKernel(opts...)

	env := &Env{RunID: "recovery-draft", SessionID: "s", Graph: g, LLM: llm, Ask: NewMemAskBus()}
	applyEnvDefaults(env)
	env.LLM = llm
	// Start the ladder on its escalate rung: the retry rung only
	// requests a backtrack, and what this test is about is the rung
	// that produces a revised draft.
	env.State.SetOutputs(ladderStateKey("recover"), PortValues{"rung": ladderRungEscalate, "retry_attempts": 1})

	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}

	llm.mu.Lock()
	defer llm.mu.Unlock()
	if len(llm.calls) != 2 {
		t.Fatalf("Generate calls = %d, want 2 (ladder escalate rung + exit gate)", len(llm.calls))
	}
	gatePrompt := llm.calls[1].Messages[len(llm.calls[1].Messages)-1].Content

	if !strings.Contains(gatePrompt, revised) {
		t.Errorf("the exit gate did not see the ladder's revised draft.\nThe recovery path made a real LLM call whose output the gate never received.\nprompt:\n%s", gatePrompt)
	}
	if strings.Contains(gatePrompt, failingDraft) {
		t.Errorf("the exit gate saw the ORIGINAL failing draft — the direct agent_loop edge is shadowing the ladder again.\nprompt:\n%s", gatePrompt)
	}
}

// TestChatDefault_ExitGateDraftHasNoBypassEdge is the structural guard
// for the same finding: nothing may feed exit_gate.draft except the
// routed path, because a third in-edge that is always present wins the
// last-writer race and silently disables recovery.
func TestChatDefault_ExitGateDraftHasNoBypassEdge(t *testing.T) {
	t.Parallel()
	g := GateAgenticTurnRouting(loadGraphFile(t, chatDefaultPath), true)

	allowed := map[string]bool{chatReplanCheckNodeID: true, chatRecoverNodeID: true}
	var sources []string
	for _, e := range g.Edges {
		if e.To.Node == chatExitGateNodeID && e.To.Port == "draft" {
			sources = append(sources, e.From.Node+":"+e.From.Port)
			if !allowed[e.From.Node] {
				t.Errorf("exit_gate.draft is fed from %q, bypassing the routed path — on a recovery pass this shadows the ladder's revised draft", e.From.Node)
			}
		}
	}
	// Exactly two, and they are mutually exclusive by construction
	// (one is the decision's not-taken port, the other is below its
	// taken port), so declaration order cannot decide the value.
	if len(sources) != 2 {
		t.Errorf("exit_gate.draft in-edges = %v, want exactly 2 (replan_check:false and recover:result)", sources)
	}
}
