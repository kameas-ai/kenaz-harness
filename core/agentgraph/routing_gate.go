package agentgraph

import (
	_ "embed"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// routing_gate.go — the agentic-turn-routing launch gate
// (agentgraph-total-convergence-01PMGX01 WP11b; design in
// agentic-turn-routing-01PMAG01 §3.6).
//
// WHAT THIS IS. chat_default.yaml was rewritten IN PLACE for the routed
// turn: `route` joined the agent_loop body and `exit_gate` sits between
// the loop and assistant_write. §3.6 requires a flag that restores the
// previous node set, because an in-place rewrite of the graph every
// chat turn runs has no fallback otherwise, and "roll the binary back"
// is not a mitigation available to a user who already installed it.
//
// WHICH DIRECTION THE TRANSFORM RUNS, AND WHY. The YAML holds the
// ROUTED topology and this function STRIPS it when the flag is off —
// not the other way round — so that there is ONE source of truth for
// the routed shape: the YAML an author edits, rather than a YAML plus a
// Go builder that has to be kept in step with it.
//
// The cost of that choice is that the DEFAULT (flag off) path runs a
// derived graph, so the strip has to be provably right rather than
// plausibly right. It is pinned three ways:
//   - TestGateAgenticTurnRouting_OffRestoresTheClassicGraph diffs the
//     output against graphs/chat_default_classic.yaml, a frozen copy of
//     the real pre-rewrite file, node-for-node and edge-for-edge;
//   - the promotion golden pins the OFF traversal as byte-identical to
//     the traversal recorded before any of this existed;
//   - the promotion golden separately pins the ON traversal, so the
//     flag's two positions are both nailed down rather than one being
//     assumed.
//
// IT FAILS CLOSED (review finding B2). This is deliberately NOT a
// general graph algebra — it is a named transform over one known graph.
// When chat_default no longer has the shape the strip knows how to
// undo, the honest options are "return the input" or "return the
// frozen classic graph", and the first one is wrong: the input in that
// state is the ROUTED graph, so returning it degrades the flag to ON
// while the user has it off, which is precisely the outcome the flag
// exists to prevent and the opposite of what api.go's GraphLoader
// promises. A revert lever that stops working the moment someone edits
// the thing it reverts is not a lever. So on drift the gate returns the
// embedded classic graph and logs loudly.
//
// The embedded copy is why graphs/chat_default_classic.yaml lives
// outside testdata/: it is a shipped asset the binary depends on, not a
// fixture.

const (
	// The four nodes the routed rewrite ADDED to chat_default.yaml.
	// chatRoutedNodeIDs below is the single set built from them, and it
	// is what BOTH the shape guard and the strip use — review finding
	// B3 was that those two were separate lists, so renaming
	// `replan_check` passed a guard that never looked for it and then
	// half-stripped the graph into something that fails Validate().
	chatRouteNodeID       = "route"
	chatExitGateNodeID    = "exit_gate"
	chatReplanCheckNodeID = "replan_check"
	chatRecoverNodeID     = "recover"

	// chatAgentLoopNodeID / chatAssistantWriteNodeID are the two
	// pre-existing nodes the strip has to re-connect to each other.
	chatAgentLoopNodeID       = "agent_loop"
	chatAssistantWriteNodeID  = "assistant_write"
	chatAssistantTextPortName = "assistant_text"

	// chatClassicLoopCondition is the loop condition chat_default
	// carried before the rewrite: keep looping while the model is still
	// asking for tools. The routed graph replaces it with a
	// router-driven one, so the strip has to put this back.
	chatClassicLoopCondition = "tool_call_count > 0"

	// chatRoutedLoopPortName is the port the routed rewrite added to
	// agent_loop's declared output surface so `replan_check` can branch
	// on the router's choice outside the loop. It is routed-only, so
	// the strip removes it — a leftover declared port would be harmless
	// at run time but would make the gate's output stop being
	// node-for-node identical to the classic graph, and that identity
	// is the only thing standing between the default path and a
	// silently drifting hand-written transform.
	chatRoutedLoopPortName = "next"
)

// chatRoutedNodeIDs is THE set of nodes the routed rewrite added. The
// shape guard requires all of them; the strip removes exactly them.
// One set, two uses — see the B3 note above.
func chatRoutedNodeIDs() map[string]bool {
	return map[string]bool{
		chatRouteNodeID:       true,
		chatExitGateNodeID:    true,
		chatReplanCheckNodeID: true,
		chatRecoverNodeID:     true,
	}
}

//go:embed graphs/chat_default_classic.yaml
var classicChatGraphYAML []byte

var (
	classicChatGraphOnce sync.Once
	classicChatGraph     Graph
	classicChatGraphErr  error
)

// ClassicChatGraph returns the frozen pre-rewrite chat topology — the
// fail-closed fallback, parsed once from the embedded YAML.
//
// Exported so the chat surface and the graph manager can fall back to
// it too, and so a test can assert the embedded asset parses and
// validates rather than discovering it at the first drift.
func ClassicChatGraph() (Graph, error) {
	classicChatGraphOnce.Do(func() {
		classicChatGraph, classicChatGraphErr = LoadYAML(classicChatGraphYAML)
	})
	return classicChatGraph, classicChatGraphErr
}

// GateAgenticTurnRouting returns g with the routed turn either kept
// (enabled) or stripped back to the classic linear chassis (disabled).
//
// It never mutates g: the caller's graph is deep-copied for the parts
// that change. The chat surface owns the FLAG (a settings dial read at
// graph-load time); this package owns the SHAPE.
func GateAgenticTurnRouting(g Graph, enabled bool) Graph {
	if enabled {
		return g
	}

	idx := make(map[string]bool, len(g.Nodes))
	for i := range g.Nodes {
		idx[g.Nodes[i].ID] = true
	}

	strip := chatRoutedNodeIDs()

	// Nothing to strip: the flag is being applied to a graph that was
	// never rewritten — every activity, every test graph, and the
	// classic graph itself. Leave it exactly alone.
	present := 0
	for id := range strip {
		if idx[id] {
			present++
		}
	}
	if present == 0 {
		return g
	}

	// Partial routed shape, or a missing anchor: the graph has drifted
	// from what this transform was written against. Fail CLOSED (B2) —
	// returning g here would hand back the routed graph with the flag
	// off, which is the one outcome the flag exists to prevent.
	if present != len(strip) || !idx[chatAgentLoopNodeID] || !idx[chatAssistantWriteNodeID] {
		classic, err := ClassicChatGraph()
		if err != nil {
			// The embedded asset failed to parse. Nothing safe is left
			// to return, so return the input and shout — but this is a
			// build-integrity failure, and
			// TestClassicChatGraph_EmbeddedAssetParsesAndValidates
			// exists so it can never reach a user.
			logging.L().Error("agentgraph.routing_gate.fallback_unavailable",
				"graph_id", g.ID, "err", err.Error(),
				"detail", "embedded classic chat graph failed to parse; cannot fail closed")
			return g
		}
		logging.L().Warn("agentgraph.routing_gate.shape_mismatch",
			"graph_id", g.ID,
			"detail", "graph does not have the routed shape this gate strips; falling back to the embedded classic chat graph")
		return classic
	}

	out := g
	out.Nodes = make([]Node, 0, len(g.Nodes))
	for i := range g.Nodes {
		n := g.Nodes[i]
		if strip[n.ID] {
			continue
		}
		// Restore the loop: drop `route` from the body and put the
		// tool-count condition back. LoopAttrs is a value type, so
		// copying the slice is what keeps this non-mutating.
		if n.ID == chatAgentLoopNodeID {
			if a, ok := n.Attrs.(LoopAttrs); ok {
				body := make([]string, 0, len(a.Body))
				for _, b := range a.Body {
					if !strip[b] {
						body = append(body, b)
					}
				}
				a.Body = body
				a.Condition = chatClassicLoopCondition
				n.Attrs = a
			}
			outs := make([]Port, 0, len(n.Outputs))
			for _, p := range n.Outputs {
				if p.Name != chatRoutedLoopPortName {
					outs = append(outs, p)
				}
			}
			n.Outputs = outs
		}
		out.Nodes = append(out.Nodes, n)
	}

	// Drop every edge touching a stripped node, then reconnect the loop
	// straight to the writer — the edge the exit gate was inserted
	// into. Appending rather than inserting is safe: edge order carries
	// no semantics (the kernel builds in/out adjacency maps from it),
	// and the equality test normalises before comparing.
	out.Edges = make([]Edge, 0, len(g.Edges))
	for _, e := range g.Edges {
		if strip[e.From.Node] || strip[e.To.Node] {
			continue
		}
		out.Edges = append(out.Edges, e)
	}
	reconnect := Edge{
		From: EndpointRef{Node: chatAgentLoopNodeID, Port: chatAssistantTextPortName},
		To:   EndpointRef{Node: chatAssistantWriteNodeID, Port: chatAssistantTextPortName},
	}
	// Only add the reconnect edge if the graph does not already carry
	// it (review finding N7). A duplicate edge is not inert: it
	// double-counts in the kernel's in-degree, so the target would wait
	// for two arrivals that only ever produce one.
	exists := false
	for _, e := range out.Edges {
		if e == reconnect {
			exists = true
			break
		}
	}
	if !exists {
		out.Edges = append(out.Edges, reconnect)
	}

	return out
}

// GraphUsesAgenticTurnRouting reports whether g carries the routed turn
// (i.e. GateAgenticTurnRouting was called with enabled=true, or never
// called at all on a routed graph). The chat surface uses it to decide
// the Env policies that must move WITH the topology — TaskStateArming
// and nothing else today — so the flag cannot end up half-applied, with
// a routed graph running on failure-only TaskState and an exit gate
// checking the answer against an empty goal.
func GraphUsesAgenticTurnRouting(g Graph) bool {
	for i := range g.Nodes {
		if g.Nodes[i].Kind == NodeKindRouter {
			return true
		}
	}
	return false
}
