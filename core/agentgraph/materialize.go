package agentgraph

// Dynamic materialization — "a conversation is a graph built as it goes"
// (agentgraph-total-convergence-01PMGX01 §4.5, WP12).
//
// ─────────────────────────────────────────────────────────────────────
// THE SHAPE DECISION: PROJECTION, NOT LIVE MUTATION
//
// Two shapes satisfy "the chat run appends a node per action taken":
//
//	(a) mutate the running Graph — append nodes/edges to env.Graph as
//	    the model chooses tools;
//	(b) project — derive the executed graph from the run's recorded
//	    EventLog plus the spec that ran, after the fact.
//
// This file implements (b), deliberately. (a) fights the kernel on
// three fronts at once: in-degree and promotion bookkeeping are computed
// from a snapshot of the edge set (buildEdges) taken before the first
// fire, so appending edges mid-run silently desynchronises the ready
// set; `env.Graph` is read concurrently by every in-flight node, so
// mutation needs a lock on the hottest read path in the kernel; and the
// promotion goldens pin the exact fire order of a fixed topology, which
// a self-extending graph no longer has. None of that risk buys anything
// the contract asks for. "Identical in kind to an authored one" is a
// claim about the ARTIFACT — a materialized run is a `Graph`, with real
// `NodeKind`s, that passes the same `Validate()` and reloads through the
// same `LoadYAML` — not a claim that the runtime mutated one.
//
// What (b) cost, and what was added to pay it: the EventLog did not
// record Loop/Retry body fires at all (the kernel hides body nodes from
// its edge walk, so nothing emitted node_start/node_complete for them).
// Every model turn and every tool dispatch of every chat turn happens
// inside a loop body, so the projection would have been blind to exactly
// the fires that matter. `appendBodyFireStart` / `appendBodyFireComplete`
// (exec_control.go) close that hole additively — same two EventKinds
// every kernel-scheduled node already emits, plus `iteration` and
// `container` payload keys. No new EventKind, no execution change.
//
// ─────────────────────────────────────────────────────────────────────
// ONE REPRESENTATION (contract clause 4)
//
// Trace, audit, compaction, resume and materialization all read the
// SAME stream, and this file adds no second one:
//
//	trace         Manager.runTrace → EventLog.Replay (raw rows to the UI)
//	resume        Kernel.RebuildState → EventLog.Replay
//	compaction    compaction.EventLogEmitter → EventLog.Append
//	audit / fs    EventFileRead/Write, appendFSToolProvenance → EventLog
//	materialize   MaterializeRun → EventLog.Replay   ← this file
//
// The projection therefore cannot drift from the trace: if an event is
// not in the log, it is not in the materialized graph, and every
// materialized node carries the `start_seq`/`end_seq` that joins it back
// to the exact trace rows it was derived from.
//
// ─────────────────────────────────────────────────────────────────────
// REDACTION
//
// The EventLog persists the full argument map (tool_invocation.go) and
// the full error text, because a run trace is a local debugging surface.
// A materialized graph is a shareable artifact — it round-trips to YAML
// and can be saved into the graph library — so the projection describes
// rather than copies, on all three axes:
//
//	arguments  toolloop.SummarizeArgs over rune-capped keys: key names
//	           and JSON kinds, never a value
//	results    a byte count and an error bit, never content
//	errors     classifyErrorText, whose return value is always one of a
//	           fixed set of constants (review finding F1)
//
// Errors were the leak that review found: producers interpolate the
// failing arguments into the message (`open /etc/shadow: permission
// denied`), so bounding and neutralizing the text still shipped the
// argument. Classification is the only forwarding rule with no path
// from input bytes to output bytes.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Materialization status values recorded on NodeMaterialization.Status.
const (
	// MaterializedStatusCompleted — the fire ran to completion
	// (EventNodeComplete).
	MaterializedStatusCompleted = "completed"
	// MaterializedStatusError — the fire ended in an error the kernel
	// recorded and never completed.
	MaterializedStatusError = "error"
	// MaterializedStatusSkipped — the kernel resolved the node's
	// in-degree with no live inbound edge (EventNodeSkipped): the
	// not-taken side of a conditional.
	MaterializedStatusSkipped = "skipped"
	// MaterializedStatusNotReached — the run never visited this node.
	// Materialized only because a surviving node-id reference (a
	// decision's next_false, a router choice target, a review's
	// upstream_node) points at it and the spec requires those
	// references to resolve.
	MaterializedStatusNotReached = "not_reached"
	// MaterializedStatusIncomplete — the fire started and the log ends
	// without a matching complete or error. A cancelled or crashed run.
	MaterializedStatusIncomplete = "incomplete"
)

// maxMaterializedArgKeyRunes bounds a single argument KEY name in an
// args summary; maxMaterializedArgsSummaryRunes bounds the whole
// summary. Neither bounds a value — values never appear (review
// finding N5).
const (
	maxMaterializedArgKeyRunes      = 64
	maxMaterializedArgsSummaryRunes = 512
)

// MaterializeRun projects a run's recorded EventLog onto a Graph spec:
// one node per action the run actually took, in the order it took them,
// with the loop unrolled and every model-emitted tool call promoted from
// an opaque fan-out into its own node instance.
//
// `source` is the RESOLVED spec the run executed — post gate-rewrite,
// post dial-override — not the on-disk library file. Projecting against
// a different spec than the one that ran produces a graph whose attrs
// lie about the run, which is why the caller (Manager.materializeRun)
// resolves it from the run registry rather than re-reading the library.
//
// The returned Graph passes Validate() by construction; MaterializeRun
// does not call it (the caller does, and a test pins it) so that a
// projection of a pathological run is still inspectable rather than
// replaced by an error.
func MaterializeRun(source Graph, runID string, log EventLog, opts ...MaterializeOption) (Graph, error) {
	if runID == "" {
		return Graph{}, fmt.Errorf("agentgraph: materialize: empty run id")
	}
	if log == nil {
		return Graph{}, fmt.Errorf("agentgraph: materialize: nil event log")
	}
	m := newMaterializer(source, runID)
	for _, o := range opts {
		o(m)
	}
	if err := log.Replay(runID, m.observe); err != nil {
		return Graph{}, fmt.Errorf("agentgraph: materialize: replay run %q: %w", runID, err)
	}
	if len(m.fires) == 0 {
		return Graph{}, fmt.Errorf("agentgraph: materialize: run %q has no recorded node fires", runID)
	}
	return m.build(), nil
}

// MaterializeOption tunes a projection.
type MaterializeOption func(*materializer)

// WithSpecProvenance marks HOW the caller obtained the source spec
// (WP12 review F2). Callers that resolved the exact spec the run
// executed pass nothing; a caller falling back to the library file
// passes SpecProvenanceLibraryFallback, which stamps the graph and
// appends a plain-language warning to its description so a degraded
// projection can never be mistaken for a faithful one.
func WithSpecProvenance(p string) MaterializeOption {
	return func(m *materializer) { m.specProvenance = p }
}

// ---- pass 1: fold the event stream into per-fire records ----

// fire is one execution of one authored node. A node that fires N times
// (a loop body node, a backtracked node) yields N fires and therefore N
// materialized node instances.
type fire struct {
	source    string
	kind      NodeKind
	instance  int
	iteration int
	container string
	order     int
	id        string

	startSeq  int64
	endSeq    int64
	startedAt time.Time
	endedAt   time.Time
	closed    bool
	status    string

	// Error state is STRUCTURAL only — a set of closed-vocabulary
	// classes, a count, and a byte total. The raw error string is never
	// stored on a fire, so it cannot reach the artifact through a later
	// edit either (review finding F1).
	errClasses map[string]struct{}
	errCount   int
	errBytes   int
	sawError   bool

	// calls holds the per-call instances materialized out of a
	// tool_dispatch fan-out, in the order the log recorded them.
	calls   []*fire
	byCall  map[string]*fire
	isCall  bool
	details NodeMaterialization
}

type materializer struct {
	source   Graph
	runID    string
	srcIndex map[string]*Node
	bodyOf   map[string]string // body node id → container node id

	fires  []*fire
	open   map[string]*fire
	counts map[string]int
	order  int

	graphID        string
	specProvenance string
}

func newMaterializer(source Graph, runID string) *materializer {
	m := &materializer{
		source:   source,
		runID:    runID,
		srcIndex: map[string]*Node{},
		bodyOf:   map[string]string{},
		open:     map[string]*fire{},
		counts:   map[string]int{},
	}
	for i := range source.Nodes {
		m.srcIndex[source.Nodes[i].ID] = &source.Nodes[i]
	}
	for i := range source.Nodes {
		n := &source.Nodes[i]
		switch n.Kind {
		case NodeKindLoop:
			if a, ok := n.Attrs.(LoopAttrs); ok {
				for _, b := range a.Body {
					m.bodyOf[b] = n.ID
				}
			}
		case NodeKindRetry:
			if a, ok := n.Attrs.(RetryAttrs); ok {
				for _, b := range a.Body {
					m.bodyOf[b] = n.ID
				}
			}
		}
	}
	return m
}

func (m *materializer) observe(e Event) error {
	switch e.Kind {
	case EventRunStart:
		p := decodeEventPayload(e.Payload)
		m.graphID = payloadString(p, "graph_id")
	case EventNodeStart:
		p := decodeEventPayload(e.Payload)
		// A second start without an intervening complete means the
		// previous fire never closed (cancel, crash, pause).
		m.closeOpen(e.NodeID, MaterializedStatusIncomplete, e.Seq, e.Timestamp)
		f := m.newFire(e.NodeID, NodeKind(payloadString(p, "kind")), e.Seq, e.Timestamp)
		f.iteration = payloadInt(p, "iteration")
		f.container = payloadString(p, "container")
		if f.container == "" {
			f.container = m.bodyOf[e.NodeID]
		}
		m.open[e.NodeID] = f
	case EventNodeComplete:
		m.closeOpen(e.NodeID, MaterializedStatusCompleted, e.Seq, e.Timestamp)
	case EventNodeError:
		m.observeNodeError(e)
	case EventNodeSkipped:
		p := decodeEventPayload(e.Payload)
		f := m.newFire(e.NodeID, NodeKind(payloadString(p, "kind")), e.Seq, e.Timestamp)
		f.container = m.bodyOf[e.NodeID]
		m.close(f, MaterializedStatusSkipped, e.Seq, e.Timestamp)
	case EventLLMCall:
		if f := m.open[e.NodeID]; f != nil {
			p := decodeEventPayload(e.Payload)
			f.details.Provider = payloadString(p, "provider")
			f.details.Model = payloadString(p, "model")
			f.details.Tokens += payloadInt(p, "tokens")
			f.details.CostUSD += payloadFloat(p, "cost_usd")
			f.details.FinishReason = payloadString(p, "finish_reason")
		}
	case EventRouterChoice:
		if f := m.open[e.NodeID]; f != nil {
			p := decodeEventPayload(e.Payload)
			f.details.Choice = payloadString(p, "choice")
			f.details.ChoiceSource = payloadString(p, "source")
		}
	case EventToolCall:
		m.observeToolCall(e)
	case EventToolResult:
		m.observeToolResult(e)
	}
	return nil
}

// observeNodeError folds one node_error into the fire it belongs to,
// as structure rather than text (review finding F1).
//
// Routing (review finding N4): tool-dispatch producers stamp `call_id`
// on the error, so a failure inside a parallel fan-out lands on the
// per-call node that failed rather than smearing across the dispatcher
// — and EVERY error is kept, not just the first, because a fan-out can
// fail four calls at once and "the first one" is an arbitrary pick.
//
// Closing (review finding F4): `retried` errors keep the fire open (the
// node re-fires and completes). Every other error closes it. `adapted`
// closes it too: the loop folds the failure into its payload and moves
// on, so that fire is over even though the run is not.
func (m *materializer) observeNodeError(e Event) {
	p := decodeEventPayload(e.Payload)
	txt := payloadString(p, "err")
	class := classifyErrorText(txt)
	open := m.open[e.NodeID]

	if open != nil {
		if callID := payloadString(p, "call_id"); callID != "" {
			if child, ok := open.byCall[callID]; ok {
				child.recordError(class, len(txt))
				child.status = MaterializedStatusError
				// The dispatcher itself is not failed by one failing
				// call; it counts the failure and carries on.
				open.errCount++
				return
			}
		}
		open.recordError(class, len(txt))
		if b, ok := p["retried"].(bool); ok && b {
			return
		}
		m.closeOpen(e.NodeID, MaterializedStatusError, e.Seq, e.Timestamp)
		return
	}

	// An error with no open fire: the kernel's own catch-all for a node
	// whose start we never saw. Record it as a closed fire so the
	// failure is still visible in the graph.
	f := m.newFire(e.NodeID, m.kindOf(e.NodeID), e.Seq, e.Timestamp)
	f.recordError(class, len(txt))
	m.close(f, MaterializedStatusError, e.Seq, e.Timestamp)
}

// recordError accumulates one error's structure onto a fire.
func (f *fire) recordError(class string, byteLen int) {
	if f.errClasses == nil {
		f.errClasses = map[string]struct{}{}
	}
	f.errClasses[class] = struct{}{}
	f.errCount++
	f.errBytes += byteLen
	f.sawError = true
}

// classifyErrorText maps an error string onto the closed
// MaterializedError* vocabulary (review finding F1).
//
// THE RETURN VALUE IS ALWAYS A CONSTANT. Nothing derived from `s` is
// ever returned, which is what makes the classification safe to put in
// a shareable artifact: producers interpolate raw arguments into error
// text (`open /etc/shadow: permission denied`), so any path that
// forwards a fragment of the input — even a bounded, neutralized first
// line — ships the argument with it.
func classifyErrorText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lower := strings.ToLower(s)
	for _, probe := range []struct {
		needles []string
		class   string
	}{
		{[]string{"budget", "cap exceeded", "max_tokens_per_run", "max_tool_calls", "max_llm_calls"}, MaterializedErrorBudgetExceeded},
		{[]string{"context canceled", "context cancelled", "cancelled", "canceled", "interrupted"}, MaterializedErrorCancelled},
		{[]string{"deadline exceeded", "timeout", "timed out"}, MaterializedErrorTimeout},
		{[]string{"permission denied", "forbidden", "not permitted", "denied by policy", "operation not permitted"}, MaterializedErrorPermissionDenied},
		{[]string{"unauthorized", "authentication", "invalid api key", "401", "credential"}, MaterializedErrorAuthFailed},
		{[]string{"rate limit", "too many requests", "429", "overloaded"}, MaterializedErrorRateLimited},
		{[]string{"no such file", "not found", "does not exist", "404", "unknown tool"}, MaterializedErrorNotFound},
		{[]string{"not implemented", "unsupported"}, MaterializedErrorNotImplemented},
		{[]string{"unavailable", "connection refused", "no route to host", "eof", "503"}, MaterializedErrorUnavailable},
		{[]string{"invalid", "malformed", "parse", "required", "schema", "bad request", "400"}, MaterializedErrorInvalidInput},
	} {
		for _, n := range probe.needles {
			if strings.Contains(lower, n) {
				return probe.class
			}
		}
	}
	return MaterializedErrorOther
}

// observeToolCall promotes one model-emitted tool call into its own node
// instance. This is contract clause 2: `tool_dispatch`'s fan-out stops
// being an opaque internal loop and becomes N materialized nodes hanging
// off the dispatcher, one per call, each carrying its own tool identity,
// call id and redacted argument summary.
//
// A tool-archetype node (`sleep`, `subagent_dispatch`) emits the same
// EventToolCall with NO call_id — its call IS the node — so the detail
// lands on the node's own fire instead of spawning a child.
func (m *materializer) observeToolCall(e Event) {
	parent := m.open[e.NodeID]
	if parent == nil {
		return
	}
	p := decodeEventPayload(e.Payload)
	tool := payloadString(p, "tool")
	callID := payloadString(p, "call_id")
	args := ""
	if raw, ok := p["args"].(map[string]any); ok {
		args = summarizeArgsForArtifact(raw)
	}
	if callID == "" {
		parent.details.Tool = tool
		parent.details.Server = toolServer(tool)
		parent.details.ArgsSummary = args
		return
	}
	child := &fire{
		source:    e.NodeID,
		kind:      NodeKindToolDispatch,
		iteration: parent.iteration,
		container: parent.container,
		isCall:    true,
		startSeq:  e.Seq,
		startedAt: e.Timestamp,
		status:    MaterializedStatusCompleted,
		details: NodeMaterialization{
			Tool:        tool,
			Server:      toolServer(tool),
			CallID:      callID,
			ArgsSummary: args,
		},
	}
	m.order++
	child.order = m.order
	if parent.byCall == nil {
		parent.byCall = map[string]*fire{}
	}
	parent.calls = append(parent.calls, child)
	parent.byCall[callID] = child
	parent.details.ToolCalls++
}

func (m *materializer) observeToolResult(e Event) {
	parent := m.open[e.NodeID]
	if parent == nil {
		return
	}
	p := decodeEventPayload(e.Payload)
	target := parent
	if id := payloadString(p, "call_id"); id != "" {
		if c, ok := parent.byCall[id]; ok {
			target = c
		}
	}
	// Content is never copied — only its length and the error bit.
	target.details.ResultBytes = payloadInt(p, "bytes")
	if b, ok := p["is_error"].(bool); ok && b {
		target.details.ResultIsError = true
		if target.isCall {
			target.status = MaterializedStatusError
		}
	}
	target.endSeq = e.Seq
}

func (m *materializer) newFire(nodeID string, kind NodeKind, seq int64, ts time.Time) *fire {
	if kind == "" {
		kind = m.kindOf(nodeID)
	}
	m.counts[nodeID]++
	m.order++
	f := &fire{
		source:    nodeID,
		kind:      kind,
		instance:  m.counts[nodeID],
		order:     m.order,
		startSeq:  seq,
		startedAt: ts,
	}
	m.fires = append(m.fires, f)
	return f
}

func (m *materializer) closeOpen(nodeID, status string, seq int64, ts time.Time) {
	f := m.open[nodeID]
	if f == nil {
		return
	}
	delete(m.open, nodeID)
	m.close(f, status, seq, ts)
}

func (m *materializer) close(f *fire, status string, seq int64, ts time.Time) {
	if f.closed {
		return
	}
	f.closed = true
	// A fire that recorded an error and then completed anyway is a
	// continueOnError retry: it completed, and the errors it survived
	// stay on the record as ErrorClass / ErrorCount.
	f.status = status
	f.endSeq = seq
	f.endedAt = ts
}

func (m *materializer) kindOf(nodeID string) NodeKind {
	if n, ok := m.srcIndex[nodeID]; ok {
		return n.Kind
	}
	return ""
}

// ---- pass 2/3: build the graph ----

func (m *materializer) build() Graph {
	// Anything still open when the log ran out never completed.
	for id, f := range m.open {
		status := MaterializedStatusIncomplete
		if f.sawError {
			status = MaterializedStatusError
		}
		m.close(f, status, f.startSeq, f.startedAt)
		delete(m.open, id)
	}

	b := &materializedBuilder{m: m, byID: map[string]*fire{}, firesBySource: map[string][]*fire{}}
	b.assignIDs()
	b.buildNodes()
	b.buildEdges()
	b.rewriteReferences()
	return b.finish()
}

type materializedBuilder struct {
	m *materializer

	nodes         []Node
	byID          map[string]*fire
	firesBySource map[string][]*fire
	edges         []Edge
	entrypoints   []string
	// synthesized holds not_reached instances created on demand by
	// reference resolution, appended after the real fires.
	synthesized []*fire
}

// assignIDs fixes the instance naming scheme:
//
//	<authored node id>@<fire ordinal>            e.g. assistant_turn@2
//	<dispatcher instance>[<call id>]             e.g. tool_dispatch@2[toolu_01ab]
//
// The ordinal is per authored node and 1-based, so `assistant_turn@1`
// and `assistant_turn@2` are the model's first and second turns of the
// same conversation. Iteration number is recorded in provenance rather
// than in the ID because a node can fire more than once per iteration
// (a continueOnError retry) and the ID must stay unique.
func (b *materializedBuilder) assignIDs() {
	for _, f := range b.m.fires {
		f.id = fmt.Sprintf("%s@%d", f.source, f.instance)
		b.byID[f.id] = f
		b.firesBySource[f.source] = append(b.firesBySource[f.source], f)
		for i, c := range f.calls {
			ref := sanitizeIDPart(c.details.CallID)
			if ref == "" {
				ref = fmt.Sprintf("%d", i+1)
			}
			c.id = fmt.Sprintf("%s[%s]", f.id, ref)
			if _, dup := b.byID[c.id]; dup {
				c.id = fmt.Sprintf("%s[%s.%d]", f.id, ref, i+1)
			}
			b.byID[c.id] = c
		}
	}
}

func (b *materializedBuilder) buildNodes() {
	for _, f := range b.m.fires {
		b.nodes = append(b.nodes, b.nodeFor(f))
		for _, c := range f.calls {
			b.nodes = append(b.nodes, b.callNodeFor(f, c))
		}
	}
}

// nodeFor materializes one fire of an authored node: same Kind, same
// attrs, same effective port surface, new ID, plus the provenance block.
//
// Ports are materialized EXPLICITLY with `required` stripped. A
// materialized node is a record of a fire that already received its
// inputs — `required` is an authoring-time contract about what must be
// wired before a graph can run, and re-asserting it here would make a
// faithful record of (say) a skipped branch fail validation for a
// connection the run itself never needed.
func (b *materializedBuilder) nodeFor(f *fire) Node {
	src := b.m.srcIndex[f.source]
	n := Node{
		ID:   f.id,
		Kind: f.kind,
	}
	if src != nil {
		n.Kind = src.Kind
		n.Title = src.Title
		n.Attrs = src.Attrs
		n.DialOverrides = cloneMap(src.DialOverrides)
		ins, outs := portSurface(src)
		n.Inputs, n.Outputs = stripRequired(ins), stripRequired(outs)
	} else {
		ins, outs := defaultPortsFor(n.Kind)
		n.Inputs, n.Outputs = stripRequired(ins), stripRequired(outs)
	}
	n.Materialized = b.materialization(f)
	return n
}

// callNodeFor materializes ONE model-emitted tool call as its own node.
//
// KIND CHOICE, and why it is not the tool's own archetype kind: a tool
// node kind carries the tool's arguments as REQUIRED attrs
// (`subagent_dispatch` requires `profile` + `prompt`; the prompt is the
// argument text verbatim). Materializing a call as its archetype kind
// would therefore force a choice between writing raw arguments into a
// shareable artifact — violating the redaction contract — and emitting a
// node that fails its own manifest's required-attr rule. `tool_dispatch`
// is the kind whose entire definition is "dispatch a tool call", it has
// no required attrs, and the call's identity (server, tool, call id,
// redacted arg summary) lives in provenance where no schema forces it to
// be the raw thing. The fan-out is visible either way, which is what the
// contract asks for.
func (b *materializedBuilder) callNodeFor(parent, c *fire) Node {
	ins, outs := defaultPortsFor(NodeKindToolDispatch)
	title := c.details.Tool
	if title == "" {
		title = "tool call"
	}
	if c.details.CallID != "" {
		title = fmt.Sprintf("%s (%s)", title, c.details.CallID)
	}
	return Node{
		ID:           c.id,
		Kind:         NodeKindToolDispatch,
		Title:        title,
		Attrs:        ToolDispatchAttrs{},
		Inputs:       stripRequired(ins),
		Outputs:      stripRequired(outs),
		Materialized: b.materialization(c),
	}
}

func (b *materializedBuilder) materialization(f *fire) *NodeMaterialization {
	out := f.details
	out.SourceNode = f.source
	out.Instance = f.instance
	out.Iteration = f.iteration
	out.Container = f.container
	out.Status = f.status
	out.StartSeq = f.startSeq
	out.EndSeq = f.endSeq
	out.StartedAt = f.startedAt.UTC()
	out.ErrorClass = joinErrorClasses(f.errClasses)
	out.ErrorCount = f.errCount
	out.ErrorBytes = f.errBytes
	if !f.endedAt.IsZero() && !f.startedAt.IsZero() {
		out.DurationMS = f.endedAt.Sub(f.startedAt).Milliseconds()
	}
	if out.Status == "" {
		out.Status = MaterializedStatusCompleted
	}
	return &out
}

// buildEdges reconstructs the data flow that actually happened.
//
// Every edge added here goes from a fire that started earlier to one
// that started later (`connect` enforces it), which is what makes the
// unrolled graph acyclic by construction — the authored graph's one
// legal cycle, the loop, is unrolled into a chain rather than preserved
// as a back-edge.
func (b *materializedBuilder) buildEdges() {
	for _, e := range b.m.source.Edges {
		fromBody, fromInBody := b.m.bodyOf[e.From.Node]
		toBody, toInBody := b.m.bodyOf[e.To.Node]
		switch {
		case fromInBody && toInBody && fromBody == toBody:
			// Intra-body edge. The kernel filters these at run time
			// (the LoopNode threads outputs directly), but they encode
			// the author's statement of the per-iteration data flow, so
			// they are replayed once per iteration.
			b.connectPerIteration(e)
		case !fromInBody && !toInBody:
			b.connectLatest(e)
		case !fromInBody && toInBody:
			// Into the body: the value entered on the first iteration.
			b.connectOne(e, b.lastBefore(e.From.Node, b.firstFire(e.To.Node)), b.firstFire(e.To.Node))
		default:
			// Out of the body: the last iteration is what escaped.
			b.connectOne(e, b.lastFire(e.From.Node), b.firstAfter(e.To.Node, b.lastFire(e.From.Node)))
		}
	}
	b.connectIterations()
	b.connectFanOut()
}

// connectPerIteration replays an intra-body edge once per iteration,
// pairing instances by their fire order within the body.
func (b *materializedBuilder) connectPerIteration(e Edge) {
	from := b.firesBySource[e.From.Node]
	to := b.firesBySource[e.To.Node]
	for i := range to {
		var src *fire
		for _, f := range from {
			if f.order < to[i].order && (src == nil || f.order > src.order) {
				src = f
			}
		}
		b.connectOne(e, src, to[i])
	}
}

// connectLatest wires each fire of the target to the most recent
// preceding fire of the source.
func (b *materializedBuilder) connectLatest(e Edge) {
	for _, t := range b.firesBySource[e.To.Node] {
		b.connectOne(e, b.lastBefore(e.From.Node, t), t)
	}
}

// connectIterations synthesizes the wrap-around edge the unrolled loop
// needs: the last body node of iteration N feeds the first body node of
// iteration N+1. The authored graph expresses this as "iterate"; the
// materialized graph has to express it as an edge, because that is the
// only way a flat spec can say "and then it went round again".
func (b *materializedBuilder) connectIterations() {
	for i := range b.m.source.Nodes {
		n := &b.m.source.Nodes[i]
		var body []string
		switch n.Kind {
		case NodeKindLoop:
			if a, ok := n.Attrs.(LoopAttrs); ok {
				body = a.Body
			}
		case NodeKindRetry:
			if a, ok := n.Attrs.(RetryAttrs); ok {
				body = a.Body
			}
		}
		if len(body) < 1 {
			continue
		}
		first, last := body[0], body[len(body)-1]
		firsts := b.firesBySource[first]
		lasts := b.firesBySource[last]
		for _, f := range firsts {
			var prev *fire
			for _, l := range lasts {
				if l.order < f.order && (prev == nil || l.order > prev.order) {
					prev = l
				}
			}
			if prev == nil {
				continue
			}
			fromPort, toPort, ok := b.pickPortPair(prev, f)
			if !ok {
				continue
			}
			b.connect(prev, fromPort, f, toPort)
		}
	}
}

// connectFanOut hangs every materialized tool call off the dispatcher
// fire that made it, reusing the dispatcher's own inbound edges so a
// call node is reached the same way its dispatcher was.
func (b *materializedBuilder) connectFanOut() {
	inbound := map[string][]Edge{}
	for _, e := range b.edges {
		inbound[e.To.Node] = append(inbound[e.To.Node], e)
	}
	for _, f := range b.m.fires {
		for _, c := range f.calls {
			for _, in := range inbound[f.id] {
				src := b.byID[in.From.Node]
				if src == nil {
					continue
				}
				b.connect(src, in.From.Port, c, in.To.Port)
			}
		}
	}
}

func (b *materializedBuilder) connectOne(e Edge, from, to *fire) {
	if from == nil || to == nil {
		return
	}
	b.connect(from, e.From.Port, to, e.To.Port)
}

// connect adds an edge if and only if it is forward in fire order and
// both ports exist with compatible types on the materialized surfaces.
func (b *materializedBuilder) connect(from *fire, fromPort string, to *fire, toPort string) {
	if from == nil || to == nil || from.id == to.id {
		return
	}
	if from.order >= to.order {
		return
	}
	fromNode := b.nodeByID(from.id)
	toNode := b.nodeByID(to.id)
	if fromNode == nil || toNode == nil {
		return
	}
	_, outs := portSurface(fromNode)
	ins, _ := portSurface(toNode)
	fp, ok := lookupPort(outs, fromPort)
	if !ok {
		return
	}
	tp, ok := lookupPort(ins, toPort)
	if !ok {
		return
	}
	if !fp.Type.Compatible(tp.Type) {
		return
	}
	edge := Edge{From: EndpointRef{Node: from.id, Port: fromPort}, To: EndpointRef{Node: to.id, Port: toPort}}
	for _, existing := range b.edges {
		if existing == edge {
			return
		}
	}
	b.edges = append(b.edges, edge)
}

// pickPortPair chooses the most meaningful compatible (output, input)
// pair between two nodes, preferring the canonical payload ports.
func (b *materializedBuilder) pickPortPair(from, to *fire) (string, string, bool) {
	fromNode := b.nodeByID(from.id)
	toNode := b.nodeByID(to.id)
	if fromNode == nil || toNode == nil {
		return "", "", false
	}
	_, outs := portSurface(fromNode)
	ins, _ := portSurface(toNode)
	preferredOut := []string{"out", "messages", "response", "result"}
	preferredIn := []string{"in", "messages", "input", "context"}
	for _, on := range append(append([]string{}, preferredOut...), portNames(outs)...) {
		op, ok := lookupPort(outs, on)
		if !ok {
			continue
		}
		for _, in := range append(append([]string{}, preferredIn...), portNames(ins)...) {
			ip, ok := lookupPort(ins, in)
			if !ok {
				continue
			}
			if op.Type.Compatible(ip.Type) {
				return on, in, true
			}
		}
	}
	return "", "", false
}

// rewriteReferences repoints every node-id-valued attr at a materialized
// instance. A `decision` whose `next_false` still named the authored
// `exit_gate` would not validate in a graph whose nodes are all
// `exit_gate@1`-shaped — and, worse, would describe a route the run did
// not take. Where a reference points at a node the run never visited,
// a `not_reached` instance is synthesized so the reference resolves and
// the fact that the branch existed but was never entered stays visible.
func (b *materializedBuilder) rewriteReferences() {
	for i := range b.nodes {
		n := &b.nodes[i]
		f := b.byID[n.ID]
		if f == nil || f.isCall {
			continue
		}
		switch a := n.Attrs.(type) {
		case DecisionAttrs:
			a.NextTrue = b.resolveRef(a.NextTrue, f)
			a.NextFalse = b.resolveRef(a.NextFalse, f)
			n.Attrs = a
		case ReviewAttrs:
			a.UpstreamNode = b.resolveRefSoft(a.UpstreamNode, f)
			n.Attrs = a
		case EscalationLadderAttrs:
			a.UpstreamNode = b.resolveRefSoft(a.UpstreamNode, f)
			n.Attrs = a
		case EscalateAttrs:
			a.UpstreamNode = b.resolveRefSoft(a.UpstreamNode, f)
			n.Attrs = a
		case LoopAttrs:
			a.Body = b.resolveBody(a.Body, f)
			n.Attrs = a
		case RetryAttrs:
			a.Body = b.resolveBody(a.Body, f)
			n.Attrs = a
		case RouterAttrs:
			n.Attrs = b.rewriteRouter(a, f)
		}
	}
	// Synthesized not_reached instances are appended last so the
	// executed path reads top-to-bottom.
	for _, f := range b.synthesized {
		b.nodes = append(b.nodes, b.nodeFor(f))
	}
}

// rewriteRouter narrows a router's menu to the choice it actually took.
//
// The authored menu is a set of options; a materialized router is a
// decision that already happened. Keeping the untaken options would
// force their targets to resolve to instances the run never produced —
// inventing nodes to satisfy a menu nobody chose. The choice that WAS
// taken is preserved with its description, and `default_choice` is set
// to it so the node still satisfies the router rule.
func (b *materializedBuilder) rewriteRouter(a RouterAttrs, f *fire) RouterAttrs {
	choices := routerChoices(a)
	taken := f.details.Choice
	if taken == "" {
		taken = a.DefaultChoice
	}
	var chosen *routerChoice
	for i := range choices {
		if choices[i].ID == taken {
			chosen = &choices[i]
			break
		}
	}
	if chosen == nil && len(choices) > 0 {
		chosen = &choices[0]
	}
	if chosen == nil {
		return a
	}
	target := b.resolveRef(chosen.Target, f)
	a.Choices = map[string]any{
		chosen.ID: map[string]any{
			"target":      target,
			"description": chosen.Description,
		},
	}
	a.DefaultChoice = chosen.ID
	if a.ReplanChoice != chosen.ID {
		a.ReplanChoice = ""
	}
	if a.SourceNode != "" {
		a.SourceNode = b.resolveRefSoft(a.SourceNode, f)
	}
	return a
}

func (b *materializedBuilder) resolveBody(body []string, f *fire) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(body))
	// A container's body materializes as every instance of every body
	// node, in fire order — the unrolled iterations.
	type entry struct {
		order int
		id    string
	}
	var entries []entry
	for _, id := range body {
		for _, bf := range b.firesBySource[id] {
			entries = append(entries, entry{bf.order, bf.id})
			for _, c := range bf.calls {
				entries = append(entries, entry{c.order, c.id})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	for _, e := range entries {
		if seen[e.id] {
			continue
		}
		seen[e.id] = true
		out = append(out, e.id)
	}
	if len(out) == 0 {
		// The container never ran a body node; keep the reference
		// resolvable by synthesizing the authored body.
		for _, id := range body {
			out = append(out, b.resolveRef(id, f))
		}
	}
	return out
}

// resolveRef maps a FORWARD node-id reference — a successor: a
// decision's next_true/next_false, a router choice target — to the
// materialized instance the reference means, synthesizing a not_reached
// instance when the run never visited it. Empty in, empty out.
func (b *materializedBuilder) resolveRef(id string, from *fire) string {
	if id == "" {
		return ""
	}
	if inst := b.nearestInstance(id, from, true); inst != nil {
		return inst.id
	}
	return b.synthesize(id).id
}

// resolveRefSoft maps a BACKWARD node-id reference — `upstream_node`, a
// fused router's `source_node`: something that already ran — and does
// not synthesize. It returns "" when the run never visited the target,
// which is legal for every reference that uses it; inventing a node
// there would be worse than saying nothing.
//
// The direction matters and is not cosmetic. `route`'s source_node is
// the model turn it read its choice OFF, i.e. the one in the same
// iteration; resolving it forwards would name the NEXT iteration's model
// turn — a graph that says the router read a value that did not exist
// yet.
func (b *materializedBuilder) resolveRefSoft(id string, from *fire) string {
	if id == "" {
		return ""
	}
	if inst := b.nearestInstance(id, from, false); inst != nil {
		return inst.id
	}
	return ""
}

// nearestInstance returns the instance of `id` closest to the referring
// fire in the preferred direction, falling back to the other side when
// the preferred one has no instance.
func (b *materializedBuilder) nearestInstance(id string, from *fire, forward bool) *fire {
	var after, before *fire
	for _, f := range b.firesBySource[id] {
		if from == nil || f.order > from.order {
			if after == nil || f.order < after.order {
				after = f
			}
			continue
		}
		if before == nil || f.order > before.order {
			before = f
		}
	}
	if forward {
		if after != nil {
			return after
		}
		return before
	}
	if before != nil {
		return before
	}
	return after
}

func (b *materializedBuilder) synthesize(id string) *fire {
	if existing, ok := b.byID[id+"@0"]; ok {
		return existing
	}
	f := &fire{
		source: id,
		kind:   b.m.kindOf(id),
		order:  1 << 30,
		status: MaterializedStatusNotReached,
		id:     id + "@0",
	}
	b.byID[f.id] = f
	b.firesBySource[id] = append(b.firesBySource[id], f)
	b.synthesized = append(b.synthesized, f)
	return f
}

func (b *materializedBuilder) nodeByID(id string) *Node {
	for i := range b.nodes {
		if b.nodes[i].ID == id {
			return &b.nodes[i]
		}
	}
	for _, f := range b.synthesized {
		if f.id == id {
			n := b.nodeFor(f)
			return &n
		}
	}
	return nil
}

func (b *materializedBuilder) firstFire(id string) *fire {
	fires := b.firesBySource[id]
	if len(fires) == 0 {
		return nil
	}
	return fires[0]
}

func (b *materializedBuilder) lastFire(id string) *fire {
	fires := b.firesBySource[id]
	if len(fires) == 0 {
		return nil
	}
	return fires[len(fires)-1]
}

func (b *materializedBuilder) lastBefore(id string, ref *fire) *fire {
	if ref == nil {
		return b.lastFire(id)
	}
	var out *fire
	for _, f := range b.firesBySource[id] {
		if f.order < ref.order && (out == nil || f.order > out.order) {
			out = f
		}
	}
	return out
}

func (b *materializedBuilder) firstAfter(id string, ref *fire) *fire {
	if ref == nil {
		return b.firstFire(id)
	}
	var out *fire
	for _, f := range b.firesBySource[id] {
		if f.order > ref.order && (out == nil || f.order < out.order) {
			out = f
		}
	}
	return out
}

func (b *materializedBuilder) finish() Graph {
	m := b.m
	// Entrypoints: the materialized instance of each authored
	// entrypoint, falling back to the first fire of the run so the
	// graph always names one.
	seen := map[string]bool{}
	for _, ep := range m.source.Entrypoints {
		if f := b.firstFire(ep); f != nil && !seen[f.id] {
			seen[f.id] = true
			b.entrypoints = append(b.entrypoints, f.id)
		}
	}
	if len(b.entrypoints) == 0 && len(m.fires) > 0 {
		b.entrypoints = []string{m.fires[0].id}
	}

	graphID := m.source.ID
	if graphID == "" {
		graphID = m.graphID
	}
	if graphID == "" {
		graphID = "run"
	}
	desc := fmt.Sprintf(
		"Materialized run %s of graph %s: %d node fires recorded, projected from the run's EventLog "+
			"(agentgraph-total-convergence-01PMGX01 WP12). Read-only — this is a record of what happened, "+
			"not an authored graph. Tool arguments are summarised and errors are classified, never reproduced.",
		m.runID, graphID, len(m.fires))
	if m.specProvenance == SpecProvenanceLibraryFallback {
		desc += " DEGRADED: projected against the library spec; the resolved spec this run executed was no " +
			"longer available, so per-run dial overrides and any gate rewrite are unrecoverable and the " +
			"topology shown may differ from the topology that ran."
	}
	out := Graph{
		SpecVersion:    SpecVersion,
		ID:             MaterializedGraphID(graphID, m.runID),
		Name:           strings.TrimSpace(fmt.Sprintf("%s — run %s", firstNonEmpty(m.source.Name, graphID), m.runID)),
		SystemPrompt:   m.source.SystemPrompt,
		Entrypoints:    b.entrypoints,
		Nodes:          b.nodes,
		Edges:          b.edges,
		Budget:         m.source.Budget,
		SpecProvenance: m.specProvenance,
		Description:    desc,
	}
	return out
}

// MaterializedGraphID is the id a materialized run's graph carries. It
// is deterministic so re-projecting the same run yields the same spec.
func MaterializedGraphID(graphID, runID string) string {
	return fmt.Sprintf("%s__run_%s", sanitizeIDPart(graphID), sanitizeIDPart(runID))
}

// ---- small helpers ----

func stripRequired(ports []Port) []Port {
	if len(ports) == 0 {
		return nil
	}
	out := make([]Port, 0, len(ports))
	for _, p := range ports {
		p.Required = false
		out = append(out, p)
	}
	return out
}

func portNames(ports []Port) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, p.Name)
	}
	return out
}

// toolServer splits `server__tool` / `kenaz__tool` into its server half,
// mirroring the split the confirm path applies (kernel_tool_adapter.go).
func toolServer(name string) string {
	if i := strings.Index(name, "__"); i > 0 {
		return name[:i]
	}
	return ""
}

// sanitizeIDPart keeps node ids to a conservative charset so a
// provider-generated call id cannot produce an id the YAML round-trip or
// the editor has to escape. Truncation is rune-safe.
func sanitizeIDPart(s string) string {
	const max = 40
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			sb.WriteRune(r)
		case r == '_' || r == '-' || r == '.' || r == ':':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
		if utf8.RuneCountInString(sb.String()) >= max {
			break
		}
	}
	return sb.String()
}

// decodeEventPayload turns an event's raw payload into a map. Every
// kernel payload is an inline map[string]any (events.go AppendKind), so
// a decode failure means a payload shape nobody writes — treated as
// empty rather than fatal, because a projection that refuses to render a
// run because one row is odd is worse than one that renders the rest.
func decodeEventPayload(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func payloadString(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	s, _ := p[key].(string)
	return s
}

func payloadInt(p map[string]any, key string) int {
	if p == nil {
		return 0
	}
	switch v := p[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func payloadFloat(p map[string]any, key string) float64 {
	if p == nil {
		return 0
	}
	switch v := p[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// joinErrorClasses renders a fire's accumulated classes as a sorted
// comma-joined set. Every element is a MaterializedError* constant, so
// the result is a constant expression of the input's SHAPE and never of
// its content.
func joinErrorClasses(set map[string]struct{}) string {
	if len(set) == 0 {
		return ""
	}
	out := make([]string, 0, len(set))
	for k := range set {
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// summarizeArgsForArtifact is toolloop.SummarizeArgs over rune-capped
// key names (review finding N5).
//
// SummarizeArgs never renders a VALUE, which is the property that
// matters, but it does render every KEY verbatim and a key is
// model-authored and unbounded — a tool call with a 200 KB key name
// would put 200 KB into a graph spec. Capping the keys before
// summarising keeps the exact shared format (one function, one output
// shape with the confirm surface) while bounding the result.
func summarizeArgsForArtifact(raw map[string]any) string {
	if len(raw) == 0 {
		return toolloop.SummarizeArgs(raw)
	}
	capped := make(map[string]any, len(raw))
	for k, v := range raw {
		key := truncateRunesSafe(stripControlChars(k), maxMaterializedArgKeyRunes)
		// Two distinct long keys can collapse onto the same capped
		// form; disambiguate rather than silently dropping one.
		if _, dup := capped[key]; dup {
			for i := 2; ; i++ {
				alt := fmt.Sprintf("%s#%d", key, i)
				if _, taken := capped[alt]; !taken {
					key = alt
					break
				}
			}
		}
		capped[key] = v
	}
	return truncateRunesSafe(toolloop.SummarizeArgs(capped), maxMaterializedArgsSummaryRunes)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
