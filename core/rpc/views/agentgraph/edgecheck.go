package agentgraph

import (
	"strings"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// checkEdge is the manager-free body of the CheckEdge RPC
// (visual-graph-authoring-01PMUX01 WP03).
//
// It is a thin translation layer and nothing more: parse the buffer,
// hand the edge to coreag.CheckEdge, hand the validator's own message
// back. Every rule lives in core/agentgraph/edgecheck.go, shared with
// Validate() — see the API doc comment for why one rule source is the
// whole point of this method existing.
func checkEdge(graphJSON string, edge EdgeRef) (EdgeCheckResult, error) {
	if strings.TrimSpace(graphJSON) == "" {
		return EdgeCheckResult{OK: false, Reason: "graph is empty"}, nil
	}
	g, err := coreag.LoadJSON([]byte(graphJSON))
	if err != nil {
		// A buffer that does not parse is not an edge verdict — but
		// answering with an error would leave the canvas unable to say
		// anything, so the parse failure IS the reason.
		return EdgeCheckResult{OK: false, Reason: err.Error()}, nil
	}
	verdict := coreag.CheckEdge(g, coreag.Edge{
		From: coreag.EndpointRef{Node: edge.From.Node, Port: edge.From.Port},
		To:   coreag.EndpointRef{Node: edge.To.Node, Port: edge.To.Port},
	})
	if verdict.OK {
		return EdgeCheckResult{OK: true}, nil
	}
	// Strip the rule prefix the same way validateYAML does, so the
	// canvas renders the message the validation pane would render.
	_, body := splitRule(verdict.Reasons[0])
	return EdgeCheckResult{OK: false, Reason: body}, nil
}
