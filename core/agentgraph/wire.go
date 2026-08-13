package agentgraph

import (
	"encoding/json"
	"fmt"
)

// wireGraph mirrors Graph but holds attrs as raw JSON so we can
// dispatch on Kind during unmarshal. Used by both YAML and JSON paths.
//
// As of WP04 the codegen-emitted wire_gen.go owns the per-kind decoder
// (decodeAttrs), the NodeKind constants, and the defaultAttrsFor /
// defaultPortsFor dispatchers. This file retains only the Graph-level
// wire shape and helpers that operate on it.
type wireGraph struct {
	SpecVersion   string         `json:"spec_version" yaml:"spec_version"`
	ID            string         `json:"id" yaml:"id"`
	Name          string         `json:"name,omitempty" yaml:"name,omitempty"`
	Description   string         `json:"description,omitempty" yaml:"description,omitempty"`
	SystemPrompt  string         `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Entrypoints   []string       `json:"entrypoints" yaml:"entrypoints"`
	Nodes         []wireNode     `json:"nodes" yaml:"nodes"`
	Edges         []Edge         `json:"edges,omitempty" yaml:"edges,omitempty"`
	Budget        Budget         `json:"budget,omitempty" yaml:"budget,omitempty"`
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`
	// SpecProvenance round-trips the materialization fidelity marker
	// (WP12 review F2). Empty on every authored graph.
	SpecProvenance string `json:"spec_provenance,omitempty" yaml:"spec_provenance,omitempty"`
	// Layout round-trips the canvas-position metadata (WP01). Chassis
	// metadata only — see the Graph.Layout doc comment in spec.go.
	Layout map[string]NodeLayout `json:"layout,omitempty" yaml:"layout,omitempty"`
}

// wireNode mirrors Node but holds Attrs as a free-form map so we can
// dispatch on Kind. Empty attrs decode to nil; the loader fills in
// the typed default if absent.
type wireNode struct {
	ID            string         `json:"id" yaml:"id"`
	Kind          NodeKind       `json:"kind" yaml:"kind"`
	Title         string         `json:"title,omitempty" yaml:"title,omitempty"`
	Inputs        []Port         `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Outputs       []Port         `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty" yaml:"attrs,omitempty"`
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`
	// Materialized round-trips the materialization record (WP12). Nil on
	// every authored node, so an authored graph's bytes are unchanged.
	Materialized *NodeMaterialization `json:"materialized,omitempty" yaml:"materialized,omitempty"`
}

func graphToWire(g Graph) (wireGraph, error) {
	out := wireGraph{
		SpecVersion:    g.SpecVersion,
		ID:             g.ID,
		Name:           g.Name,
		Description:    g.Description,
		SystemPrompt:   g.SystemPrompt,
		Entrypoints:    append([]string(nil), g.Entrypoints...),
		Edges:          append([]Edge(nil), g.Edges...),
		Budget:         g.Budget,
		DialOverrides:  cloneMap(g.DialOverrides),
		SpecProvenance: g.SpecProvenance,
		Layout:         cloneLayoutMap(g.Layout),
	}
	out.Nodes = make([]wireNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		wn := wireNode{
			ID:            n.ID,
			Kind:          n.Kind,
			Title:         n.Title,
			Inputs:        append([]Port(nil), n.Inputs...),
			Outputs:       append([]Port(nil), n.Outputs...),
			DialOverrides: cloneMap(n.DialOverrides),
			Materialized:  n.Materialized,
		}
		if n.Attrs != nil {
			raw, err := json.Marshal(n.Attrs)
			if err != nil {
				return wireGraph{}, fmt.Errorf("agentgraph: encode attrs for %q: %w", n.ID, err)
			}
			var asMap map[string]any
			if len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &asMap); err != nil {
					return wireGraph{}, fmt.Errorf("agentgraph: re-decode attrs for %q: %w", n.ID, err)
				}
			}
			wn.Attrs = asMap
		}
		out.Nodes = append(out.Nodes, wn)
	}
	return out, nil
}

func wireToGraph(w wireGraph) (Graph, error) {
	g := Graph{
		SpecVersion:    w.SpecVersion,
		ID:             w.ID,
		Name:           w.Name,
		Description:    w.Description,
		SystemPrompt:   w.SystemPrompt,
		Entrypoints:    append([]string(nil), w.Entrypoints...),
		Edges:          append([]Edge(nil), w.Edges...),
		Budget:         w.Budget,
		DialOverrides:  cloneMap(w.DialOverrides),
		SpecProvenance: w.SpecProvenance,
		Layout:         cloneLayoutMap(w.Layout),
	}
	g.Nodes = make([]Node, 0, len(w.Nodes))
	// Track aliases observed during this load so the kernel can emit
	// per-run audit events (NFR-003). The same alias appearing on
	// multiple nodes is recorded once.
	seenAlias := map[string]struct{}{}
	for _, wn := range w.Nodes {
		// Resolve aliases at load time so downstream code only sees
		// canonical kinds. The alias map emits a deprecation warning the
		// first time it sees each old kind name (FR-029..FR-058).
		original := string(wn.Kind)
		canonical, ok := lookupAlias(original)
		if ok {
			wn.Kind = NodeKind(canonical)
			if _, dup := seenAlias[original]; !dup {
				seenAlias[original] = struct{}{}
				g.AliasesSeen = append(g.AliasesSeen, AliasResolution{
					Old:       original,
					New:       canonical,
					RemovalIn: AliasSunsetVersion,
				})
			}
		}
		n := Node{
			ID:            wn.ID,
			Kind:          wn.Kind,
			Title:         wn.Title,
			Inputs:        append([]Port(nil), wn.Inputs...),
			Outputs:       append([]Port(nil), wn.Outputs...),
			DialOverrides: cloneMap(wn.DialOverrides),
			Materialized:  wn.Materialized,
		}
		attrs, err := decodeAttrs(wn.Kind, wn.Attrs, wn.ID)
		if err != nil {
			return Graph{}, err
		}
		n.Attrs = attrs
		g.Nodes = append(g.Nodes, n)
	}
	return g, nil
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// cloneLayoutMap defensively copies a Layout map so the wire value and
// the Graph value never alias the same backing map (same discipline as
// cloneMap for DialOverrides).
func cloneLayoutMap(m map[string]NodeLayout) map[string]NodeLayout {
	if m == nil {
		return nil
	}
	out := make(map[string]NodeLayout, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
