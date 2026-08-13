package agentgraph

import (
	"encoding/json"
	"fmt"
	"time"

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

	// Provenance records what this node instance actually DID, and is
	// set only by the materialization projection (materialize.go,
	// agentgraph-total-convergence-01PMGX01 WP12). An authored graph
	// leaves it nil; it is `omitempty` on both wires, so an authored
	// graph's YAML/JSON is byte-identical whether or not this field
	// exists.
	//
	// This is what makes "a conversation is a graph" a data-model
	// statement rather than a metaphor: a materialized node is the same
	// Node type, with the same Kind, validated by the same Validate(),
	// and carries one extra block saying which run fire produced it.
	// The validator ignores it entirely — provenance can never make a
	// graph valid or invalid.
	Materialized *NodeMaterialization `json:"materialized,omitempty" yaml:"materialized,omitempty"`
}

// Error classifications a materialized node can carry
// (agentgraph-total-convergence-01PMGX01 WP12, review finding F1).
//
// This vocabulary is CLOSED and that is the security property: the
// materializer emits one of these constants or nothing, so no byte of
// an error string — which routinely quotes file paths, URLs, and
// argument values — can reach a shareable artifact. Adding a class is
// fine; making one carry a fragment of the input is not.
const (
	MaterializedErrorNotFound         = "not_found"
	MaterializedErrorPermissionDenied = "permission_denied"
	MaterializedErrorTimeout          = "timeout"
	MaterializedErrorCancelled        = "cancelled"
	MaterializedErrorBudgetExceeded   = "budget_exceeded"
	MaterializedErrorAuthFailed       = "auth_failed"
	MaterializedErrorRateLimited      = "rate_limited"
	MaterializedErrorInvalidInput     = "invalid_input"
	MaterializedErrorUnavailable      = "unavailable"
	MaterializedErrorNotImplemented   = "not_implemented"
	MaterializedErrorOther            = "other"
)

// SpecProvenanceLibraryFallback is the Graph.SpecProvenance value for a
// materialized run projected against the library file rather than the
// resolved spec that actually ran.
const SpecProvenanceLibraryFallback = "library_fallback"

// NodeMaterialization is the executed-fire record attached to a materialized
// node instance. Every field is derived from the run's EventLog — the
// same stream `Manager.runTrace` (the trace surface) and
// `Kernel.RebuildState` (resume) read — so materialization introduces no
// second event stream and cannot drift from the trace.
//
// REDACTION CONTRACT: this struct never carries tool arguments, tool
// output, or error text.
//
//   - `ArgsSummary` is toolloop.SummarizeArgs' structural summary
//     ("2 arguments: path (string), limit (number)") over rune-capped
//     key names: it renders key names and JSON kinds, never a value.
//   - Results are a byte count and an error bit, never content.
//   - Errors are a CLASSIFICATION drawn from a closed vocabulary
//     (MaterializedError* below) plus counts and byte totals — never
//     the error string. Error text is the leakiest field of the three:
//     producers interpolate raw arguments into it (exec_dispatch.go
//     quotes the failing tool call, os errors quote the path), so
//     "bound and neutralize the text" would still ship `/etc/shadow`
//     or a planted credential into a shareable artifact. Since every
//     value this struct can emit for an error is one of a fixed set of
//     constants, no input can traverse it by construction.
//
// The EventLog itself does persist raw args and raw error text, which
// is exactly why the projection classifies rather than copies: a run
// trace is a local debugging surface, and a materialized graph is a
// shareable artifact that round-trips to YAML and can be saved into the
// library. `StartSeq`/`EndSeq` join a node back to the trace rows that
// hold the full detail, for whoever is entitled to see it.
type NodeMaterialization struct {
	// SourceNode is the authored node ID this instance is a fire of.
	SourceNode string `json:"source_node,omitempty" yaml:"source_node,omitempty"`
	// Instance is the 1-based ordinal of this fire among all fires of
	// SourceNode in the run.
	Instance int `json:"instance,omitempty" yaml:"instance,omitempty"`
	// Iteration is the 1-based loop iteration the fire happened in, or
	// 0 for a node that fired outside any Loop/Retry body.
	Iteration int `json:"iteration,omitempty" yaml:"iteration,omitempty"`
	// Container is the Loop/Retry node ID whose body this fire belongs
	// to; empty outside a body.
	Container string `json:"container,omitempty" yaml:"container,omitempty"`
	// Status is one of: completed, error, skipped, not_reached,
	// incomplete. `skipped` is a conditional-branch skip the kernel
	// recorded (EventNodeSkipped); `not_reached` is a node the run
	// never visited at all, materialized only because a surviving
	// node-id reference (a decision's next_false, a router choice
	// target) points at it.
	Status string `json:"status,omitempty" yaml:"status,omitempty"`
	// StartSeq / EndSeq are the EventLog sequence numbers bounding the
	// fire. They are the join key back into the raw trace.
	StartSeq int64 `json:"start_seq,omitempty" yaml:"start_seq,omitempty"`
	EndSeq   int64 `json:"end_seq,omitempty" yaml:"end_seq,omitempty"`
	// StartedAt / DurationMS are wall-clock, from the event rows.
	StartedAt  time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty" yaml:"duration_ms,omitempty"`
	// ErrorClass classifies the errors this fire recorded, as a
	// comma-joined sorted set of MaterializedError* constants. Never
	// error text — see the REDACTION CONTRACT above.
	ErrorClass string `json:"error_class,omitempty" yaml:"error_class,omitempty"`
	// ErrorCount is how many node_error events the fire recorded. A
	// parallel tool fan-out can fail several calls at once and every
	// one of them counts here, not just the first.
	ErrorCount int `json:"error_count,omitempty" yaml:"error_count,omitempty"`
	// ErrorBytes is the total length of the error text the fire
	// produced. A size signal with no content.
	ErrorBytes int `json:"error_bytes,omitempty" yaml:"error_bytes,omitempty"`

	// ---- per-kind detail, all optional ----

	// Tool / Server / CallID / ArgsSummary describe a single tool call.
	// Set on a per-call instance materialized out of a tool_dispatch
	// fan-out, and on a tool-archetype node's own fire.
	Tool        string `json:"tool,omitempty" yaml:"tool,omitempty"`
	Server      string `json:"server,omitempty" yaml:"server,omitempty"`
	CallID      string `json:"call_id,omitempty" yaml:"call_id,omitempty"`
	ArgsSummary string `json:"args_summary,omitempty" yaml:"args_summary,omitempty"`
	// ResultBytes / ResultIsError summarise the tool result. Content is
	// never recorded.
	ResultBytes   int  `json:"result_bytes,omitempty" yaml:"result_bytes,omitempty"`
	ResultIsError bool `json:"result_is_error,omitempty" yaml:"result_is_error,omitempty"`
	// ToolCalls is the number of calls a tool_dispatch fire fanned out.
	ToolCalls int `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`

	// Provider / Model / Tokens / CostUSD / FinishReason describe an
	// llm_call the fire made.
	Provider     string  `json:"provider,omitempty" yaml:"provider,omitempty"`
	Model        string  `json:"model,omitempty" yaml:"model,omitempty"`
	Tokens       int     `json:"tokens,omitempty" yaml:"tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty" yaml:"cost_usd,omitempty"`
	FinishReason string  `json:"finish_reason,omitempty" yaml:"finish_reason,omitempty"`

	// Choice / ChoiceSource record a router fire's verdict.
	Choice       string `json:"choice,omitempty" yaml:"choice,omitempty"`
	ChoiceSource string `json:"choice_source,omitempty" yaml:"choice_source,omitempty"`
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
	MaxTokensPerRun        int     `json:"max_tokens_per_run,omitempty" yaml:"max_tokens_per_run,omitempty"`
	MaxWallclockPerRunSecs int     `json:"max_wallclock_per_run_seconds,omitempty" yaml:"max_wallclock_per_run_seconds,omitempty"`
	MaxLLMCallsPerRun      int     `json:"max_llm_calls_per_run,omitempty" yaml:"max_llm_calls_per_run,omitempty"`
	MaxToolCallsPerRun     int     `json:"max_tool_calls_per_run,omitempty" yaml:"max_tool_calls_per_run,omitempty"`
	MaxCostUSDPerRun       float64 `json:"max_cost_usd_per_run,omitempty" yaml:"max_cost_usd_per_run,omitempty"`

	// DoomLoopThreshold is the repeat count (inclusive) of a
	// near-identical (tool name, normalized args) call that trips the
	// tool-dispatch doom-loop guard (autonomy-recovery-runtime-01PMDL03
	// WP02). Zero uses DefaultDoomLoopThreshold. This is a *behavioral*
	// cap — distinct from MaxToolCallsPerRun, which only bounds total
	// call volume and never notices thrash within budget.
	DoomLoopThreshold int `json:"doom_loop_threshold,omitempty" yaml:"doom_loop_threshold,omitempty"`

	// MaxBacktracksPerRun bounds the kernel backtrack primitive
	// (autonomy-recovery-runtime-01PMDL03 WP01): the number of times a
	// completed node may be rewound and re-fired over the life of a
	// run. Unlike the other Budget fields, zero does NOT mean
	// "unlimited" — re-firing a completed node breaks the kernel's
	// normal completed-once invariant, so an explicit cap always
	// applies; zero falls back to DefaultMaxBacktracksPerRun
	// (kernel.go) rather than disabling the check.
	MaxBacktracksPerRun int `json:"max_backtracks_per_run,omitempty" yaml:"max_backtracks_per_run,omitempty"`
}

// Graph is the top-level immutable spec. Authors construct it directly
// in Go, in YAML, or via the (future) UI editor.
type Graph struct {
	SpecVersion string `json:"spec_version" yaml:"spec_version"`
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// SystemPrompt is the graph-level base system prompt prepended to
	// every model node's own system_prompt. Empty ⇒ each model node uses
	// only its own role prompt (no behaviour change).
	SystemPrompt string   `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Entrypoints  []string `json:"entrypoints" yaml:"entrypoints"`
	Nodes        []Node   `json:"nodes" yaml:"nodes"`
	Edges        []Edge   `json:"edges,omitempty" yaml:"edges,omitempty"`
	Budget       Budget   `json:"budget,omitempty" yaml:"budget,omitempty"`
	// DialOverrides at graph scope (FR-050).
	DialOverrides map[string]any `json:"dial_overrides,omitempty" yaml:"dial_overrides,omitempty"`

	// SpecProvenance records HOW the spec behind a materialized graph
	// was obtained (agentgraph-total-convergence-01PMGX01 WP12, review
	// finding F2). Empty on authored graphs, and on a materialized run
	// projected against the exact resolved spec that executed.
	//
	// `library_fallback` means the run's resolved spec was no longer
	// available (evicted from the chat-run registry, or the process
	// restarted) and the projection fell back to the library file the
	// run_start event names. The TOPOLOGY is then only as accurate as
	// the library file: per-run dial overrides and the routing-gate
	// rewrite are unrecoverable, so a routed run can be projected as a
	// classic one. Silently serving that as if it were the real thing
	// is the failure this field exists to prevent — the viewer badges
	// it and the graph description says so in prose.
	SpecProvenance string `json:"spec_provenance,omitempty" yaml:"spec_provenance,omitempty"`

	// AliasesSeen records every (old → new) deprecated kind name the
	// load path rewrote while parsing this graph. Populated by
	// wireToGraph. The kernel emits one EventKindAliasResolved event
	// per entry at run-start so the audit log carries a per-run record
	// even though the alias warning itself is once-per-process. Empty
	// when the graph used only canonical kind names.
	//
	// Not serialised on the wire — the field is JSON/YAML-omitted via
	// the wire types' `MarshalJSON` / `MarshalYAML` helpers (graphToWire
	// strips it).
	AliasesSeen []AliasResolution `json:"-" yaml:"-"`

	// Layout is optional canvas-position metadata, keyed by node ID
	// (visual-graph-authoring-01PMUX01 WP01, spec FR-004 / §4). It is
	// CHASSIS METADATA ONLY:
	//
	//   - Absent (nil/empty) on every authored graph today, so
	//     LoadYAML→DumpYAML of a layout-free graph is byte-identical —
	//     pinned by TestRoundTrip_BundledLibraryAndActivityGraphs.
	//   - Never enters the kernel's promotion/execution path, the
	//     manifest fingerprint, or `Validate()`'s rules: it carries no
	//     semantics the kernel or validator need to see, and adding a
	//     layout block to a graph must not change how it runs (pinned
	//     by TestLayoutMetadata_DoesNotAffectPromotionTrace).
	//   - Never emitted by MaterializeRun: a materialized run is a
	//     projection of what happened, not an authored canvas
	//     arrangement, so the projected Graph's Layout is always nil
	//     (pinned by TestMaterializeRun_NeverEmitsLayout).
	//
	// Unknown-node-id tolerance: a layout entry whose key does not match
	// any node.ID in this graph (e.g. because the node was renamed or
	// deleted by a hand-edit to the YAML) is INTENTIONALLY NOT an error.
	// Validate() never inspects Layout, so LoadYAML succeeds and the
	// stale entry round-trips inert — a renamed node must not brick its
	// graph. The canvas is expected to drop stale entries the next time
	// it computes a layout and the graph is re-saved from the UI; until
	// then the entry is harmless dead weight, not a validation failure.
	Layout map[string]NodeLayout `json:"layout,omitempty" yaml:"layout,omitempty"`
}

// NodeLayout is one node's canvas position. Coordinates are integers —
// the canvas rounds on save (tasks.md WP03) — so layout YAML/JSON stays
// diff-stable across saves that do not move that node.
type NodeLayout struct {
	X int `json:"x" yaml:"x"`
	Y int `json:"y" yaml:"y"`
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
