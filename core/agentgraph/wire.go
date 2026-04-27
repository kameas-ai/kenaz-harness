package agentgraph

import (
	"encoding/json"
	"fmt"
)

// wireGraph mirrors Graph but holds attrs as raw JSON so we can
// dispatch on Kind during unmarshal. Used by both YAML and JSON paths.
type wireGraph struct {
	SpecVersion   string         `json:"spec_version" yaml:"spec_version"`
	ID            string         `json:"id" yaml:"id"`
	Name          string         `json:"name,omitempty" yaml:"name,omitempty"`
	Description   string         `json:"description,omitempty" yaml:"description,omitempty"`
	Entrypoints   []string       `json:"entrypoints" yaml:"entrypoints"`
	Nodes         []wireNode     `json:"nodes" yaml:"nodes"`
	Edges         []Edge         `json:"edges,omitempty" yaml:"edges,omitempty"`
	Budget        Budget         `json:"budget,omitempty" yaml:"budget,omitempty"`
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`
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
}

func graphToWire(g Graph) (wireGraph, error) {
	out := wireGraph{
		SpecVersion:   g.SpecVersion,
		ID:            g.ID,
		Name:          g.Name,
		Description:   g.Description,
		Entrypoints:   append([]string(nil), g.Entrypoints...),
		Edges:         append([]Edge(nil), g.Edges...),
		Budget:        g.Budget,
		DialOverrides: cloneMap(g.DialOverrides),
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
		SpecVersion:   w.SpecVersion,
		ID:            w.ID,
		Name:          w.Name,
		Description:   w.Description,
		Entrypoints:   append([]string(nil), w.Entrypoints...),
		Edges:         append([]Edge(nil), w.Edges...),
		Budget:        w.Budget,
		DialOverrides: cloneMap(w.DialOverrides),
	}
	g.Nodes = make([]Node, 0, len(w.Nodes))
	for _, wn := range w.Nodes {
		n := Node{
			ID:            wn.ID,
			Kind:          wn.Kind,
			Title:         wn.Title,
			Inputs:        append([]Port(nil), wn.Inputs...),
			Outputs:       append([]Port(nil), wn.Outputs...),
			DialOverrides: cloneMap(wn.DialOverrides),
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

// decodeAttrs routes the raw attrs map through the per-kind typed
// struct. Returns the kind-specific NodeAttrs implementation.
func decodeAttrs(kind NodeKind, raw map[string]any, nodeID string) (NodeAttrs, error) {
	if !kind.IsKnown() {
		return nil, fmt.Errorf("agentgraph: node %q: unknown kind %q", nodeID, kind)
	}
	target := defaultAttrsFor(kind)
	if target == nil {
		return nil, fmt.Errorf("agentgraph: node %q: no attrs decoder for kind %q", nodeID, kind)
	}
	if len(raw) == 0 {
		// Caller may have omitted attrs; return zero-value typed struct.
		return target, nil
	}
	// Re-marshal the free-form map and decode it into the typed struct.
	// json (rather than yaml) is the lingua franca because every typed
	// struct already carries `json:` tags and yaml.v3 routes through
	// the yaml tags equivalently for the basic shapes we use.
	buf, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("agentgraph: node %q: re-encode attrs: %w", nodeID, err)
	}
	switch kind {
	case NodeKindLLM:
		var v LLMAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindTool:
		var v ToolAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindTransform:
		var v TransformAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindActivity:
		var v ActivityAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindReflect:
		var v ReflectAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindReview:
		var v ReviewAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindPlan:
		var v PlanAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindAsk:
		var v AskAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindEscalate:
		var v EscalateAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindBranch:
		var v BranchAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindParallel:
		var v ParallelAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindJoin:
		var v JoinAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindLoop:
		var v LoopAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindRetry:
		var v RetryAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindFork:
		var v ForkAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindMerge:
		var v MergeAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindMemory:
		var v MemoryAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindCorpusRead:
		var v CorpusReadAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindCorpusWrite:
		var v CorpusWriteAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindAttachment:
		var v AttachmentAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindHistoryRead:
		var v HistoryReadAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindTraceWrite:
		var v TraceWriteAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	case NodeKindCheckpoint:
		var v CheckpointAttrs
		if err := json.Unmarshal(buf, &v); err != nil {
			return nil, attrsDecodeErr(nodeID, kind, err)
		}
		return v, nil
	}
	return nil, fmt.Errorf("agentgraph: node %q: kind %q has no decoder", nodeID, kind)
}

func attrsDecodeErr(nodeID string, kind NodeKind, err error) error {
	return fmt.Errorf("agentgraph: node %q: decode attrs for kind %q: %w", nodeID, kind, err)
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
