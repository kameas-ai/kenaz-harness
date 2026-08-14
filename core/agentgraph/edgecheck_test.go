package agentgraph

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// libraryGraphs loads every bundled library graph. These are the
// fixtures the parity property runs over: real, shipped topologies
// including the routed chat_default's decision + router + loop body.
func libraryGraphs(t *testing.T) map[string]Graph {
	t.Helper()
	dir := filepath.Join("..", "rpc", "views", "agentgraph", "library")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read library dir: %v", err)
	}
	out := make(map[string]Graph)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		g, err := LoadYAML(data)
		if err != nil {
			t.Fatalf("load %s: %v", e.Name(), err)
		}
		out[e.Name()] = g
	}
	if len(out) == 0 {
		t.Fatal("no library graphs found — the parity property has no fixtures")
	}
	return out
}

func validationIssues(t *testing.T, g Graph) []string {
	t.Helper()
	err := Validate(g)
	if err == nil {
		return nil
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("Validate returned a non-ValidationError: %v", err)
	}
	return ve.Issues
}

// TestCheckEdge_ParityWithValidate is the property the WP03 task
// demands: per-edge acceptance and whole-graph validation must agree.
//
// Direction 1 (here): every edge of a graph that validates cleanly is
// accepted per-edge. If CheckEdge invented a rule Validate does not
// have, this fails.
func TestCheckEdge_ParityWithValidate_CleanGraphsAcceptEveryEdge(t *testing.T) {
	for name, g := range libraryGraphs(t) {
		t.Run(name, func(t *testing.T) {
			if issues := validationIssues(t, g); len(issues) > 0 {
				t.Skipf("fixture does not validate cleanly, parity direction 1 not applicable: %v", issues)
			}
			for i, e := range g.Edges {
				got := CheckEdge(g, e)
				if !got.OK {
					t.Errorf("edge #%d %s.%s → %s.%s rejected per-edge but the whole graph validates: %v",
						i, e.From.Node, e.From.Port, e.To.Node, e.To.Port, got.Reasons)
				}
			}
		})
	}
}

// Direction 2: an edge CheckEdge rejects also makes the whole graph
// fail Validate, with the same message body. If Validate stopped
// applying a rule CheckEdge still applies, this fails.
func TestCheckEdge_ParityWithValidate_RejectedEdgesAlsoFailValidate(t *testing.T) {
	graphs := libraryGraphs(t)

	// Every rejection the canvas can produce, injected into every
	// fixture: a dangling endpoint, an unknown port, a type mismatch,
	// and a decision-routing contradiction.
	for name, base := range graphs {
		t.Run(name, func(t *testing.T) {
			var candidates []Edge
			for _, n := range base.Nodes {
				ins, outs := portSurface(&n)
				if len(outs) == 0 || len(ins) == 0 {
					continue
				}
				// A dangling target.
				candidates = append(candidates, Edge{
					From: EndpointRef{Node: n.ID, Port: outs[0].Name},
					To:   EndpointRef{Node: "no_such_node_xyz", Port: "in"},
				})
				// A port that does not exist.
				candidates = append(candidates, Edge{
					From: EndpointRef{Node: n.ID, Port: "no_such_port_xyz"},
					To:   EndpointRef{Node: n.ID, Port: ins[0].Name},
				})
			}
			// Every cross-node pairing, so real type mismatches turn up.
			for _, a := range base.Nodes {
				_, outs := portSurface(&a)
				for _, b := range base.Nodes {
					ins, _ := portSurface(&b)
					for _, o := range outs {
						for _, i := range ins {
							candidates = append(candidates, Edge{
								From: EndpointRef{Node: a.ID, Port: o.Name},
								To:   EndpointRef{Node: b.ID, Port: i.Name},
							})
						}
					}
				}
			}

			checked := 0
			for _, e := range candidates {
				verdict := CheckEdge(base, e)
				if verdict.OK {
					continue
				}
				checked++
				probe := base
				probe.Edges = append(append([]Edge{}, base.Edges...), e)
				issues := validationIssues(t, probe)
				if len(issues) == 0 {
					t.Fatalf("edge %s.%s → %s.%s rejected per-edge (%v) but the whole graph validates",
						e.From.Node, e.From.Port, e.To.Node, e.To.Port, verdict.Reasons)
				}
				// The per-edge message must actually appear in the
				// whole-graph result, modulo the "edge #N" ref. That is
				// what makes "the validator's message" true rather than
				// "a message like the validator's".
				//
				// `cycle:` issues are matched on the rule only: the
				// acyclicity pass starts its DFS at a map-iteration
				// order node, so which rotation of the cycle it names
				// is not stable across two runs of the SAME function.
				for _, reason := range verdict.Reasons {
					body := stripEdgeRef(reason)
					if strings.HasPrefix(reason, "cycle:") {
						body = "cycle:"
					}
					found := false
					for _, issue := range issues {
						if strings.Contains(stripEdgeRef(issue), body) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("per-edge reason %q has no counterpart in Validate's issues %v", reason, issues)
					}
				}
			}
			if checked == 0 {
				t.Fatalf("no candidate edge was rejected — the property never ran")
			}
		})
	}
}

// stripEdgeRef removes the "edge #N: " / "edge: " positional prefix so
// a per-edge message can be compared against the whole-graph one.
func stripEdgeRef(msg string) string {
	for _, prefix := range []string{"port: edge"} {
		if !strings.HasPrefix(msg, prefix) {
			continue
		}
		if i := strings.Index(msg, ": "); i >= 0 {
			rest := msg[i+2:]
			if j := strings.Index(rest, ": "); j >= 0 {
				return rest[j+2:]
			}
		}
	}
	return msg
}

func TestCheckEdge_PortRules(t *testing.T) {
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{
				ID:      "a",
				Kind:    NodeKindTransform,
				Outputs: []Port{{Name: "text_out", Type: PortTypeText}},
			},
			{
				ID:     "b",
				Kind:   NodeKindTransform,
				Inputs: []Port{{Name: "json_in", Type: PortTypeJSON}, {Name: "any_in", Type: PortTypeAny}},
			},
		},
	}

	t.Run("accepts a compatible edge", func(t *testing.T) {
		got := CheckEdge(g, Edge{
			From: EndpointRef{Node: "a", Port: "text_out"},
			To:   EndpointRef{Node: "b", Port: "any_in"},
		})
		if !got.OK {
			t.Fatalf("want ok, got %v", got.Reasons)
		}
	})

	t.Run("refuses a type mismatch with the validator's message", func(t *testing.T) {
		got := CheckEdge(g, Edge{
			From: EndpointRef{Node: "a", Port: "text_out"},
			To:   EndpointRef{Node: "b", Port: "json_in"},
		})
		if got.OK {
			t.Fatal("want rejection")
		}
		if len(got.Reasons) != 1 || !strings.Contains(got.Reasons[0], "type mismatch a.text_out(text) → b.json_in(json)") {
			t.Fatalf("unexpected reasons: %v", got.Reasons)
		}
	})

	t.Run("refuses an unknown node", func(t *testing.T) {
		got := CheckEdge(g, Edge{
			From: EndpointRef{Node: "ghost", Port: "out"},
			To:   EndpointRef{Node: "b", Port: "any_in"},
		})
		if got.OK || !strings.Contains(got.Reasons[0], `from-node "ghost" does not exist`) {
			t.Fatalf("unexpected verdict: %+v", got)
		}
	})

	t.Run("refuses an unknown port", func(t *testing.T) {
		got := CheckEdge(g, Edge{
			From: EndpointRef{Node: "a", Port: "nope"},
			To:   EndpointRef{Node: "b", Port: "any_in"},
		})
		if got.OK || !strings.Contains(got.Reasons[0], `node "a" has no output port "nope"`) {
			t.Fatalf("unexpected verdict: %+v", got)
		}
	})
}

func TestCheckEdge_DecisionRouting(t *testing.T) {
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"d"},
		Nodes: []Node{
			{
				ID:      "d",
				Kind:    NodeKindDecision,
				Attrs:   DecisionAttrs{Condition: "x == 1", NextTrue: "t", NextFalse: "f"},
				Outputs: []Port{{Name: "true", Type: PortTypeAny}, {Name: "false", Type: PortTypeAny}},
			},
			{ID: "t", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}},
			{ID: "f", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}},
		},
	}

	ok := CheckEdge(g, Edge{
		From: EndpointRef{Node: "d", Port: "true"},
		To:   EndpointRef{Node: "t", Port: "in"},
	})
	if !ok.OK {
		t.Fatalf("agreeing edge rejected: %v", ok.Reasons)
	}

	bad := CheckEdge(g, Edge{
		From: EndpointRef{Node: "d", Port: "true"},
		To:   EndpointRef{Node: "f", Port: "in"},
	})
	if bad.OK {
		t.Fatal("want rejection of an edge contradicting next_true")
	}
	if !strings.Contains(bad.Reasons[0], "next_true names") {
		t.Fatalf("unexpected reason: %v", bad.Reasons)
	}
}

func TestCheckEdge_RouterRouting(t *testing.T) {
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"r"},
		Nodes: []Node{
			{
				ID:   "r",
				Kind: NodeKindRouter,
				Attrs: RouterAttrs{
					Mode:          "fused",
					SourceNode:    "r",
					DefaultChoice: "go",
					Choices:       map[string]any{"go": map[string]any{"target": "a"}},
				},
				Outputs: []Port{{Name: "go", Type: PortTypeAny}},
			},
			{ID: "a", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}},
			{ID: "b", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}},
		},
	}

	if got := CheckEdge(g, Edge{
		From: EndpointRef{Node: "r", Port: "go"},
		To:   EndpointRef{Node: "a", Port: "in"},
	}); !got.OK {
		t.Fatalf("agreeing choice edge rejected: %v", got.Reasons)
	}

	got := CheckEdge(g, Edge{
		From: EndpointRef{Node: "r", Port: "go"},
		To:   EndpointRef{Node: "b", Port: "in"},
	})
	if got.OK || !strings.Contains(got.Reasons[0], "the choice names") {
		t.Fatalf("unexpected verdict: %+v", got)
	}
}

func TestCheckEdge_RefusesAnEdgeThatWouldCycle(t *testing.T) {
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}, Outputs: []Port{{Name: "out", Type: PortTypeAny}}},
			{ID: "b", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny}}, Outputs: []Port{{Name: "out", Type: PortTypeAny}}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "a", Port: "out"}, To: EndpointRef{Node: "b", Port: "in"}},
		},
	}
	got := CheckEdge(g, Edge{
		From: EndpointRef{Node: "b", Port: "out"},
		To:   EndpointRef{Node: "a", Port: "in"},
	})
	if got.OK {
		t.Fatal("want rejection of a cycle-closing edge")
	}
	if !strings.Contains(got.Reasons[0], "outside-loop cycle") {
		t.Fatalf("unexpected reason: %v", got.Reasons)
	}
	// And Validate agrees, which is the point.
	probe := g
	probe.Edges = append(append([]Edge{}, g.Edges...), Edge{
		From: EndpointRef{Node: "b", Port: "out"},
		To:   EndpointRef{Node: "a", Port: "in"},
	})
	if len(validationIssues(t, probe)) == 0 {
		t.Fatal("Validate accepted a graph CheckEdge called cyclic")
	}
}

// A half-built graph is the normal case at drag time — the canvas must
// not refuse the very first edge because the graph has no entrypoint
// yet or an input is still unconnected.
func TestCheckEdge_IgnoresWholeGraphRules(t *testing.T) {
	g := Graph{
		// No spec_version, no entrypoints: Validate would reject this.
		ID: "",
		Nodes: []Node{
			{ID: "a", Kind: NodeKindTransform, Outputs: []Port{{Name: "out", Type: PortTypeAny}}},
			{ID: "b", Kind: NodeKindTransform, Inputs: []Port{{Name: "in", Type: PortTypeAny, Required: true}}},
		},
	}
	if len(validationIssues(t, g)) == 0 {
		t.Fatal("fixture should not validate — the test proves CheckEdge ignores that")
	}
	got := CheckEdge(g, Edge{
		From: EndpointRef{Node: "a", Port: "out"},
		To:   EndpointRef{Node: "b", Port: "in"},
	})
	if !got.OK {
		t.Fatalf("a legal edge in a half-built graph was refused: %v", got.Reasons)
	}
}
