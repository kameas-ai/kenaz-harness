package agentgraph

import "fmt"

// Per-edge validation (visual-graph-authoring-01PMUX01 WP03).
//
// ONE RULE SOURCE. The canvas refuses an illegal edge at drag time, and
// the save path refuses the same edge through Validate(). If those were
// two implementations they would drift, and the drift would present as
// "the canvas let me draw it and then the save said no" — which is
// worse than having no drag-time check at all, because it teaches the
// author to distrust the canvas.
//
// So the rules live here, once, in functions that take a single edge,
// and BOTH callers use them:
//
//   - `Validate` → `checkEdges` calls `edgePortIssues` per edge, and
//     `checkDecisionRouting` / `checkRouterRouting` call
//     `decisionEdgeIssues` / `routerEdgeIssues` per (node, edge) pair.
//   - `CheckEdge` (the RPC behind the canvas) calls exactly the same
//     three, plus the acyclicity pass — not a copy of it, the actual
//     `checkAcyclicityOutsideLoops` method — over the graph with the
//     candidate edge added.
//
// `edgecheck_test.go` pins the equivalence as a property over the
// bundled library graphs rather than trusting the arrangement.
//
// WHAT IS DELIBERATELY NOT HERE: the whole-graph rules that no single
// edge can answer — "required input X is not connected", entrypoint
// resolution, attr schemas. Those stay save-time. A per-edge check that
// reported them would refuse the first edge of every graph under
// construction.

// EdgeCheck is the verdict on one candidate edge.
type EdgeCheck struct {
	// OK is true when the edge breaks no per-edge rule.
	OK bool
	// Reasons carries the validator's own messages, unmodified, so the
	// canvas can show the author exactly what the save path would have
	// said. Empty when OK.
	Reasons []string
}

// CheckEdge reports whether e may be drawn in g, applying exactly the
// edge-scoped rules Validate applies.
//
// It is safe to call on a graph that does not otherwise validate: a
// half-built graph is the normal case at drag time, and a missing
// entrypoint must not make every edge illegal.
func CheckEdge(g Graph, e Edge) EdgeCheck {
	idx := g.nodesByID()

	issues, _ := edgePortIssues(idx, "edge", e)
	issues = append(issues, edgeRoutingIssues(idx, e)...)
	issues = append(issues, edgeCycleIssues(g, e)...)

	if len(issues) == 0 {
		return EdgeCheck{OK: true}
	}
	return EdgeCheck{OK: false, Reasons: issues}
}

// edgePortIssues is the port-rule half of `checkEdges`, callable for one
// edge. `ref` names the edge in the message ("edge #3" from Validate,
// "edge" from CheckEdge) so Validate's output is byte-identical to what
// it was before the extraction.
//
// resolved reports whether both endpoints AND both ports were found —
// Validate uses it to decide whether the edge counts as covering a
// required input port.
func edgePortIssues(idx map[string]*Node, ref string, e Edge) (issues []string, resolved bool) {
	fromNode, fromOK := idx[e.From.Node]
	if !fromOK {
		return []string{fmt.Sprintf("port: %s: from-node %q does not exist", ref, e.From.Node)}, false
	}
	toNode, toOK := idx[e.To.Node]
	if !toOK {
		return []string{fmt.Sprintf("port: %s: to-node %q does not exist", ref, e.To.Node)}, false
	}
	_, fromOuts := portSurface(fromNode)
	toIns, _ := portSurface(toNode)
	fromPort, fromPortOK := lookupPort(fromOuts, e.From.Port)
	if !fromPortOK {
		return []string{fmt.Sprintf("port: %s: node %q has no output port %q", ref, e.From.Node, e.From.Port)}, false
	}
	toPort, toPortOK := lookupPort(toIns, e.To.Port)
	if !toPortOK {
		return []string{fmt.Sprintf("port: %s: node %q has no input port %q", ref, e.To.Node, e.To.Port)}, false
	}
	if !fromPort.Type.Compatible(toPort.Type) {
		return []string{fmt.Sprintf("port: %s: type mismatch %s.%s(%s) → %s.%s(%s)",
			ref,
			e.From.Node, e.From.Port, fromPort.Type,
			e.To.Node, e.To.Port, toPort.Type,
		)}, true
	}
	return nil, true
}

// edgeRoutingIssues dispatches the routing-agreement rules on the edge's
// source node. Only `decision` and `router` have any — they are the two
// kinds whose attrs name a successor, so they are the two where an edge
// can contradict the attrs and silently mis-route.
func edgeRoutingIssues(idx map[string]*Node, e Edge) []string {
	from, ok := idx[e.From.Node]
	if !ok {
		return nil
	}
	switch from.Kind {
	case NodeKindDecision:
		a, ok := from.Attrs.(DecisionAttrs)
		if !ok {
			return nil
		}
		return decisionEdgeIssues(from.ID, a, e)
	case NodeKindRouter:
		a, ok := from.Attrs.(RouterAttrs)
		if !ok {
			return nil
		}
		return routerEdgeIssues(from.ID, a, e)
	}
	return nil
}

// decisionEdgeIssues enforces "the edge off a verdict port goes where
// the attr says it goes". See checkDecisionRouting's doc comment for
// why the disagreement is a silent mis-route rather than cosmetic.
//
// Edges off `verdict` / `next` / author-declared extra ports are audit
// surfaces, not routes, and are not checked.
func decisionEdgeIssues(nodeID string, a DecisionAttrs, e Edge) []string {
	if e.From.Node != nodeID {
		return nil
	}
	var want, attr string
	switch e.From.Port {
	case "true":
		want, attr = a.NextTrue, "next_true"
	case "false":
		want, attr = a.NextFalse, "next_false"
	default:
		return nil
	}
	if want == "" || e.To.Node == want {
		return nil
	}
	return []string{fmt.Sprintf(
		"schema: node %q (decision): edge from port %q targets %q but %s names %q — the kernel routes on both, so they must agree",
		nodeID, e.From.Port, e.To.Node, attr, want)}
}

// routerEdgeIssues is decisionEdgeIssues' counterpart for `router`: an
// edge leaving a port named for a choice must go where that choice says.
func routerEdgeIssues(nodeID string, a RouterAttrs, e Edge) []string {
	if e.From.Node != nodeID {
		return nil
	}
	want := ""
	found := false
	for _, c := range routerChoices(a) {
		if c.ID == e.From.Port {
			want = c.Target
			found = true
		}
	}
	if !found || want == "" || e.To.Node == want {
		return nil
	}
	return []string{fmt.Sprintf(
		"schema: node %q (router): edge from choice port %q targets %q but the choice names %q — the kernel routes on both, so they must agree",
		nodeID, e.From.Port, e.To.Node, want)}
}

// edgeCycleIssues reports the outside-loop cycle the candidate edge
// would INTRODUCE.
//
// It runs the real `checkAcyclicityOutsideLoops` — the same method
// Validate runs, not a reimplementation — twice: once on the graph as
// it stands and once with the edge added, and blames the edge only when
// the first run was clean and the second is not. A graph that already
// cycles must not have every subsequent edge blamed for it.
//
// The comparison is on PRESENCE, not on the message. The pass walks
// `for id := range idx` over a Go map, so which node the DFS starts
// from — and therefore which rotation of the cycle path the message
// names — varies run to run. Diffing the strings would make this
// function's verdict depend on map iteration order.
func edgeCycleIssues(g Graph, e Edge) []string {
	if g.findNode(e.From.Node) == nil || g.findNode(e.To.Node) == nil {
		return nil
	}
	if len(runAcyclicity(g)) > 0 {
		// Already cyclic; the save path reports it and this edge is not
		// the reason.
		return nil
	}

	probe := g
	probe.Edges = make([]Edge, 0, len(g.Edges)+1)
	probe.Edges = append(probe.Edges, g.Edges...)
	probe.Edges = append(probe.Edges, e)
	return runAcyclicity(probe)
}

func runAcyclicity(g Graph) []string {
	v := &validator{cfg: validateConfig{activities: nilActivityRegistry{}}, errs: &ValidationError{}}
	v.checkAcyclicityOutsideLoops(g, g.nodesByID())
	return v.errs.Issues
}
