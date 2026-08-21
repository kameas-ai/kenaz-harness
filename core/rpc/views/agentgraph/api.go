// Package agentgraph is the view-scoped RPC surface for the
// agent-kernel-graph mission's Bundle A WP06: graph library, run
// control, and trace tail. The frontend's /agentgraph route consumes
// this surface to list / edit / save graph specs and to drive runs
// against the kernel exposed by `core/agentgraph`.
//
// The package name is `agentgraph` (matching the underlying engine's
// package); the rpc/views directory disambiguates by path. The frontend
// imports types from `@/lib/types` so the wire shapes here are
// camelCase and avoid leaking core types directly.
package agentgraph

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/elicitation"
)

// GraphScope narrows ListGraphs to a layer:
//
//	"library"  - bundled / shipped graphs (read-only)
//	"user"     - on-disk user graphs at <DataDir>/agent_graph/library/
//	""         - both layers, library first then user (de-duplicated by id)
type GraphScope = string

// GraphInfo is the wire shape for a single library entry.
type GraphInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope"` // "library" | "user"
	Source      string `json:"source,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

// GraphSpec is the wire shape carrying the graph YAML across the boundary.
// The frontend treats YAML as opaque text so the editor can use a Monaco
// or textarea instance without the harness re-encoding a typed payload.
//
// Scope is "library" | "user" | "materialized". The third
// (agentgraph-total-convergence-01PMGX01 WP12) is a graph projected from
// a run's EventLog rather than read from disk: it is the executed
// conversation, and it is read-only for the same reason a log line is —
// editing a record of what happened would make it a record of something
// else.
type GraphSpec struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Scope string `json:"scope"`
	YAML  string `json:"yaml"`
}

// ValidationIssue is one validator violation. Stable shape so the
// frontend renders rule prefixes consistently.
type ValidationIssue struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// ValidationResult is what Validate returns: ok + per-issue details.
type ValidationResult struct {
	OK     bool              `json:"ok"`
	Issues []ValidationIssue `json:"issues"`
}

// EdgeEndpoint is one end of a candidate edge: a node id + a port name.
// Mirrors coreag.EndpointRef in the camelCase wire dialect.
type EdgeEndpoint struct {
	Node string `json:"node"`
	Port string `json:"port"`
}

// EdgeRef is the candidate edge CheckEdge is asked about.
type EdgeRef struct {
	From EdgeEndpoint `json:"from"`
	To   EdgeEndpoint `json:"to"`
}

// EdgeCheckResult is CheckEdge's verdict. Reason carries the
// validator's own message so the canvas shows the author exactly what
// the save path would say — not a paraphrase.
type EdgeCheckResult struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// RunState enumerates the kernel-visible run lifecycle states. The
// values mirror the kernel's emitted EventLog kinds (run_start,
// run_paused, run_complete, node_error) so the frontend can pattern
// match on the same strings the trace tail surfaces.
type RunState = string

const (
	RunStateRunning   RunState = "running"
	RunStatePaused    RunState = "paused"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
)

// RunStatus is the wire shape for a run status query.
type RunStatus struct {
	RunID         string  `json:"runId"`
	GraphID       string  `json:"graphId"`
	SessionID     string  `json:"sessionId,omitempty"`
	State         string  `json:"state"`
	StartedAt     string  `json:"startedAt"`
	UpdatedAt     string  `json:"updatedAt"`
	CompletedAt   string  `json:"completedAt,omitempty"`
	Error         string  `json:"error,omitempty"`
	NodesComplete int     `json:"nodesComplete"`
	LLMTokens     int     `json:"llmTokens"`
	LLMCalls      int     `json:"llmCalls"`
	ToolCalls     int     `json:"toolCalls"`
	CostUSD       float64 `json:"costUsd"`
	PendingAsk    *PendingAsk `json:"pendingAsk,omitempty"`

	// PendingApproval surfaces an approval node's parked decision,
	// beside (not replacing) PendingAsk — the two pending kinds must
	// not collapse into one shape (approval-node-01PMZC12 UNIT-3,
	// FR-002). Exactly one of PendingAsk / PendingApproval is set on
	// any given paused run: the kernel parks on at most one node per
	// pause cycle, and its kind is what decides which field this is.
	PendingApproval *PendingApproval `json:"pendingApproval,omitempty"`
}

// PendingAsk surfaces an AskNode's parked question so the UI can
// render a resume prompt. Empty Question means there is no pending ask.
//
// Kind and Spec are populated only when the paused node asked a
// structured question (agentgraph-total-convergence-01PMGX01 WP06). A
// free-form ask — every ask that existed before that WP, including the
// chat graph's turn boundary — leaves both empty and the wire shape
// byte-identical to what the RunView already renders.
type PendingAsk struct {
	NodeID   string `json:"nodeId"`
	Question string `json:"question"`

	// Kind is one of core/elicitation's seven dialog kinds, or empty
	// for a free-form ask.
	Kind string `json:"kind,omitempty"`

	// Spec carries the full question (options, bounds, preview) so the
	// UI can render the same controls the dialog does.
	Spec *elicitation.Question `json:"spec,omitempty"`
}

// PendingApproval surfaces an approval node's parked decision so the UI
// can render an approve/reject prompt (approval-node-01PMZC12 UNIT-3,
// FR-002/FR-010). Distinct from PendingAsk: an approval's resume verb
// is Graph_ResolveApproval(runID, nodeID, approved, reason), not
// Resume(runID, freeText) — resuming an approval through Resume, or an
// ask through ResolveApproval, is refused (spec.md §5.2).
type PendingApproval struct {
	NodeID       string `json:"nodeId"`
	Prompt       string `json:"prompt"`
	ApproverRole string `json:"approverRole,omitempty"`
}

// RunTraceEvent is one row of the EventLog tail.
type RunTraceEvent struct {
	Seq       int64  `json:"seq"`
	RunID     string `json:"runId"`
	NodeID    string `json:"nodeId,omitempty"`
	Kind      string `json:"kind"`
	Timestamp string `json:"ts"`
	Payload   string `json:"payload,omitempty"` // JSON-encoded payload
}

// StartRunRequest is the body for StartRun.
type StartRunRequest struct {
	GraphID   string         `json:"graphId"`
	SessionID string         `json:"sessionId,omitempty"`
	Inputs    map[string]any `json:"inputs,omitempty"`
}

// StartRunResponse pairs the new run id with the initial RunStatus.
type StartRunResponse struct {
	RunID  string    `json:"runId"`
	Status RunStatus `json:"status"`
}

// API is the view-scoped accessor backing /agentgraph.
//
// The implementation lives in impl.go and wraps a Manager (defined
// alongside) that owns the kernel + event log + graph library.
//
// A nil manager is allowed; methods return ErrManagerUnavailable so
// the frontend can render an empty state without crashing the chassis.
type API interface {
	// ListGraphs returns every graph matching scope.
	ListGraphs(ctx context.Context, scope GraphScope) ([]GraphInfo, error)

	// LoadGraph returns the YAML payload + metadata for a single graph.
	LoadGraph(ctx context.Context, id string) (GraphSpec, error)

	// SaveGraph persists a user graph to <DataDir>/agent_graph/library.
	// Library-scoped graphs are read-only; saving a library id returns
	// an error.
	SaveGraph(ctx context.Context, spec GraphSpec) error

	// DeleteGraph removes a user graph (idempotent).
	DeleteGraph(ctx context.Context, id string) error

	// Validate runs the validator without persisting. The frontend
	// editor uses this for the live error pane.
	Validate(ctx context.Context, yaml string) (ValidationResult, error)

	// CheckEdge answers "may this edge be drawn?" for the canvas, at
	// drag time (visual-graph-authoring-01PMUX01 WP03, FR-002).
	//
	// It exists so there is exactly ONE source of edge-legality rules.
	// The alternative — compiling the port table into the frontend from
	// ports_gen — would have produced a second implementation that
	// drifts, and the drift presents as "the canvas let me draw it and
	// the save refused it", which teaches the author to distrust the
	// canvas. coreag.CheckEdge and coreag.Validate call the same
	// functions; a parity property over the bundled graphs pins it.
	//
	// graphJSON is the editor's current buffer as JSON (the canvas
	// already holds it parsed, so this needs no second parse of the
	// YAML text and cannot disagree with what is on screen).
	CheckEdge(ctx context.Context, graphJSON string, edge EdgeRef) (EdgeCheckResult, error)

	// StartRun launches a new run on the kernel.
	StartRun(ctx context.Context, req StartRunRequest) (StartRunResponse, error)

	// GetRunStatus returns the current state + counters for a run.
	GetRunStatus(ctx context.Context, runID string) (RunStatus, error)

	// GetRunTrace returns events with seq > since for the run.
	GetRunTrace(ctx context.Context, runID string, since int64) ([]RunTraceEvent, error)

	// Resume un-parks a paused run. Requires the run is in state=paused
	// and an AskNode is pending; otherwise returns an error — including
	// when what's pending is an approval, not an ask (use
	// ResolveApproval for that).
	Resume(ctx context.Context, runID, askResponse string) error

	// ResolveApproval resolves a paused run's pending approval node,
	// approve or reject, with an optional reason (approval-node-01PMZC12
	// UNIT-3, FR-003). Requires the run is in state=paused and an
	// approval is pending; otherwise returns an error — including when
	// what's pending is an ask, not an approval (use Resume for that).
	// Cedar-gated (cedar.ActionApprovalResolve) and audited
	// (audit.KindApprovalResolved) — a human approval is trust- or
	// compliance-relevant under CLAUDE.md's own rubric.
	ResolveApproval(ctx context.Context, runID, nodeID string, approved bool, reason string) error

	// CancelRun signals a running run to stop. Idempotent on
	// already-finished runs.
	CancelRun(ctx context.Context, runID string) error

	// MaterializeRun returns the run as a graph spec: one node per
	// action the run took, the loop unrolled into per-iteration
	// instances, and every model-emitted tool call promoted to its own
	// node (agentgraph-total-convergence-01PMGX01 §4.5, WP12).
	//
	// This is the same GraphSpec LoadGraph returns — a materialized
	// conversation and an authored graph are the same artifact in the
	// same shape, which is the whole point — carrying Scope
	// "materialized" so the editor opens it read-only. Chat runs are
	// materializable too: the chat runner registers its resolved spec
	// with the manager on every turn.
	MaterializeRun(ctx context.Context, runID string) (GraphSpec, error)
}

// nowRFC3339 is the canonical timestamp shape across the wire — kept
// here so the impl + Manager use a single source of truth.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }
