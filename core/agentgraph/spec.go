package agentgraph

import (
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SpecVersion is the canonical DSL schema version. Loader rejects
// unknown major versions; minor bumps are forward-compatible.
const SpecVersion = "1"

// NodeKind enumerates every primitive the spec defines (FR-001 / FR-004
// .. FR-025). The string values are the on-the-wire identifiers used in
// YAML / JSON; renaming any of them is a breaking spec change.
//
// As of WP04 the canonical NodeKind* constants and the AllNodeKinds
// helper live in wire_gen.go (codegen-emitted from the manifest set).
// IsKnown delegates to that generated slice plus the runtime alias map
// so v0 graphs (kind: llm | plan | branch | fork) still load.
type NodeKind string

// IsKnown reports whether k is a recognised node-kind. Recognition
// covers both the canonical (manifest-emitted) names and the live
// alias map (FR-029..FR-058 deprecation aliases).
func (k NodeKind) IsKnown() bool {
	for _, candidate := range AllNodeKinds() {
		if candidate == k {
			return true
		}
	}
	if _, ok := lookupAlias(string(k)); ok {
		return true
	}
	return false
}

// PortType is the lightweight type system on edges. It is intentionally
// nominal (string) rather than structural; the validator checks
// equality (or `any` compatibility) when wiring an edge. Kernel
// implementations own the actual unmarshalling.
type PortType string

const (
	PortTypeAny         PortType = "any"
	PortTypeText        PortType = "text"
	PortTypeMessages    PortType = "messages"
	PortTypeJSON        PortType = "json"
	PortTypeBool        PortType = "bool"
	PortTypeNumber      PortType = "number"
	PortTypeAttachment  PortType = "attachment"
	PortTypeMemoryChunk PortType = "memory_chunk"
	PortTypeCorpusHits  PortType = "corpus_hits"
	PortTypeToolResult  PortType = "tool_result"
	PortTypeBranchID    PortType = "branch_id"
	PortTypeTrace       PortType = "trace"
)

// Compatible reports whether a value of type src can flow into a port
// of type dst. `any` is the universal donor + acceptor.
func (src PortType) Compatible(dst PortType) bool {
	if src == "" || dst == "" {
		return false
	}
	if src == PortTypeAny || dst == PortTypeAny {
		return true
	}
	return src == dst
}

// Port describes one input or output of a Node. Names are unique within
// the (node, direction) tuple; ordering in YAML is preserved for human
// readability but is not load-bearing.
type Port struct {
	Name     string   `json:"name" yaml:"name"`
	Type     PortType `json:"type" yaml:"type"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	// Description is purely documentation; ignored by validator.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Node is the unit of execution in a graph. Attrs holds the per-kind
// typed payload (e.g. ModelAttrs for `kind: model`); the loader populates
// it from the raw `attrs:` block once Kind is known.
type Node struct {
	ID    string   `json:"id" yaml:"id"`
	Kind  NodeKind `json:"kind" yaml:"kind"`
	Title string   `json:"title,omitempty" yaml:"title,omitempty"`
	// Inputs / Outputs declare the node's port surface. The default
	// surface for each kind is filled in by `defaultPortsFor` if the
	// author omits them; explicit ports always win so authors can
	// constrain `any`-typed defaults to concrete types.
	Inputs  []Port `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Outputs []Port `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	// Attrs is the strongly-typed per-kind config. After unmarshalling
	// it is one of the *Attrs types defined below; the raw form on
	// disk is a free-form map that we re-encode through the typed
	// shape during loading.
	Attrs NodeAttrs `json:"attrs,omitempty" yaml:"attrs,omitempty"`
	// DialOverrides lets a node override a dial value for the duration
	// of its fire (FR-050). Validated against the known-dials list.
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`
}

// NodeAttrs is the typed payload union. Each *Attrs type satisfies it
// by virtue of carrying its own discriminator-free shape — Node.Kind
// drives the dispatch.
type NodeAttrs interface {
	nodeAttrsMarker()
	// Validate is invoked by the validator after loading. Returns a
	// non-nil error keyed by the per-kind rule that failed; the
	// validator wraps it with the offending node ID.
	Validate() error
}

// EndpointRef identifies one end of an edge: a node ID + a port name.
type EndpointRef struct {
	Node string `json:"node" yaml:"node"`
	Port string `json:"port" yaml:"port"`
}

// Edge connects one node's output port to another node's input port.
type Edge struct {
	From EndpointRef `json:"from" yaml:"from"`
	To   EndpointRef `json:"to"   yaml:"to"`
}

// Budget declares per-graph hard caps the kernel enforces (FR-048).
// All fields zero ⇒ kernel defaults apply.
type Budget struct {
	MaxTokensPerRun         int     `json:"max_tokens_per_run,omitempty" yaml:"max_tokens_per_run,omitempty"`
	MaxWallclockPerRunSecs  int     `json:"max_wallclock_per_run_seconds,omitempty" yaml:"max_wallclock_per_run_seconds,omitempty"`
	MaxLLMCallsPerRun       int     `json:"max_llm_calls_per_run,omitempty" yaml:"max_llm_calls_per_run,omitempty"`
	MaxToolCallsPerRun      int     `json:"max_tool_calls_per_run,omitempty" yaml:"max_tool_calls_per_run,omitempty"`
	MaxCostUSDPerRun        float64 `json:"max_cost_usd_per_run,omitempty" yaml:"max_cost_usd_per_run,omitempty"`
}

// Graph is the top-level immutable spec. Authors construct it directly
// in Go, in YAML, or via the (future) UI editor.
type Graph struct {
	SpecVersion string  `json:"spec_version" yaml:"spec_version"`
	ID          string  `json:"id" yaml:"id"`
	Name        string  `json:"name,omitempty" yaml:"name,omitempty"`
	Description string  `json:"description,omitempty" yaml:"description,omitempty"`
	Entrypoints []string `json:"entrypoints" yaml:"entrypoints"`
	Nodes       []Node  `json:"nodes" yaml:"nodes"`
	Edges       []Edge  `json:"edges,omitempty" yaml:"edges,omitempty"`
	Budget      Budget  `json:"budget,omitempty" yaml:"budget,omitempty"`
	// DialOverrides at graph scope (FR-050).
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`
}

// findNode returns the node with the given ID, or nil if not found.
// O(N) — the validator memoises into a map for repeated lookup.
func (g *Graph) findNode(id string) *Node {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

// nodesByID returns a map of node ID → *Node for repeated lookups.
func (g *Graph) nodesByID() map[string]*Node {
	out := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		out[g.Nodes[i].ID] = &g.Nodes[i]
	}
	return out
}

// MarshalJSON emits a stable JSON representation. Node.Attrs is
// re-encoded as a kind-specific payload object.
func (g Graph) MarshalJSON() ([]byte, error) {
	wire, err := graphToWire(g)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire)
}

// UnmarshalJSON parses JSON, then routes Node.Attrs through the
// kind-specific decoder.
func (g *Graph) UnmarshalJSON(data []byte) error {
	var wire wireGraph
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	parsed, err := wireToGraph(wire)
	if err != nil {
		return err
	}
	*g = parsed
	return nil
}

// MarshalYAML implements yaml.Marshaler.
func (g Graph) MarshalYAML() (any, error) {
	return graphToWire(g)
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (g *Graph) UnmarshalYAML(value *yaml.Node) error {
	var wire wireGraph
	if err := value.Decode(&wire); err != nil {
		return err
	}
	parsed, err := wireToGraph(wire)
	if err != nil {
		return err
	}
	*g = parsed
	return nil
}

// LoadYAML parses YAML bytes into a Graph (without validation). Call
// `Validate(g)` separately to enforce schema + topology rules.
func LoadYAML(data []byte) (Graph, error) {
	var g Graph
	if err := yaml.Unmarshal(data, &g); err != nil {
		return Graph{}, fmt.Errorf("agentgraph: yaml decode: %w", err)
	}
	return g, nil
}

// LoadJSON parses JSON bytes into a Graph (without validation).
func LoadJSON(data []byte) (Graph, error) {
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return Graph{}, fmt.Errorf("agentgraph: json decode: %w", err)
	}
	return g, nil
}

// DumpYAML emits canonical YAML bytes for a Graph. Suitable for on-disk
// persistence at `<DataDir>/agent_graph/library/*.yaml`.
func DumpYAML(g Graph) ([]byte, error) {
	out, err := yaml.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("agentgraph: yaml encode: %w", err)
	}
	return out, nil
}

// DumpJSON emits canonical JSON bytes for a Graph. Suitable for the
// over-RPC wire format.
func DumpJSON(g Graph) ([]byte, error) {
	out, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("agentgraph: json encode: %w", err)
	}
	return out, nil
}
