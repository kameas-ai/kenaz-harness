package agentgraph

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// EventKind enumerates every entry the kernel writes to the EventLog.
// Strings are wire-stable: renaming any of them breaks replay of an
// existing on-disk log.
type EventKind string

const (
	// Node lifecycle.
	EventNodeStart    EventKind = "node_start"
	EventNodeComplete EventKind = "node_complete"
	EventNodeError    EventKind = "node_error"

	// Compute side-effects.
	EventLLMCall    EventKind = "llm_call"
	EventToolCall   EventKind = "tool_call"
	EventToolResult EventKind = "tool_result"

	// State side-effects.
	EventMemoryWrite  EventKind = "memory_write"
	EventMemoryRead   EventKind = "memory_read"
	EventTraceWrite   EventKind = "trace_write"
	EventCheckpoint   EventKind = "checkpoint"
	EventSessionWrite EventKind = "session_write"

	// Control flow.
	EventBranchFork    EventKind = "branch_fork"
	EventBranchMerge   EventKind = "branch_merge"
	EventForkRequested EventKind = "fork_requested"
	EventMergeRequest  EventKind = "merge_requested"

	// Compaction (subsystem ships in Bundle D — kernel only emits the
	// signal events here so other agents can wire into them.)
	EventCompactionFired   EventKind = "compaction_fired"
	EventCompactionApplied EventKind = "compaction_applied"

	// Approval flow (FR-048). Mirrors the ask_pending / ask_answered
	// pair; runs pause at `approval_pending` until the user clicks
	// Approve / Reject in the harness UI.
	EventApprovalPending  EventKind = "approval_pending"
	EventApprovalResolved EventKind = "approval_resolved"

	// Artifact emission (FR-058). Terminal output; the harness UI
	// surfaces these as session messages or files depending on the
	// node's `output_target` attr.
	EventArtifactEmitted EventKind = "artifact_emitted"

	// Filesystem / bash-output state-kind events (FR-057a/b/c). The
	// `state_action` payload field carries the FR-058b action UID
	// classification ("Read::file", "Read::bash_output", "Write::file")
	// so audit consumers can group by surface without re-parsing the
	// event kind.
	EventFileRead       EventKind = "file_read"
	EventFileWrite      EventKind = "file_write"
	EventBashOutputRead EventKind = "bash_output_read"

	// Alias resolution (NFR-003). Emitted once per run when the loader
	// rewrites a deprecated kind name to its canonical form.
	EventKindAliasResolved EventKind = "kind_alias_resolved"

	// Dial / budget.
	EventDialOverridden EventKind = "dial_overridden"
	EventCostCapHit     EventKind = "cost_cap_hit"
	EventBudgetCapHit   EventKind = "budget_cap_hit"

	// EventDoomLoopDetected fires from the tool-dispatch executor when a
	// (tool name, normalized-arg-hash) pair repeats DoomLoopThreshold+
	// times within a run (autonomy-recovery-runtime-01PMDL03 WP02). This
	// is a *behavioral* signal, distinct from the blunt budget caps
	// above: a model can thrash on the same failing call indefinitely
	// within MaxToolCallsPerRun and never trip a cap. Payload carries
	// the tool name, repeat count, and threshold; the node also sets
	// `should_replan=true` on its Outputs so a future ladder controller
	// (WP04) can force escalation regardless of remaining budget.
	EventDoomLoopDetected EventKind = "doom_loop_detected"

	// Ask flow.
	EventAskPending  EventKind = "ask_pending"
	EventAskAnswered EventKind = "ask_answered"

	// Reflect / review / escalate.
	EventReflectStarted    EventKind = "reflect_started"
	EventReflectCompleted  EventKind = "reflect_completed"
	EventReviewPass        EventKind = "review_pass"
	EventReviewFail        EventKind = "review_fail"
	EventReviewUnrecov     EventKind = "review_failed_unrecoverable"
	EventEscalateTriggered EventKind = "escalate_triggered"
	EventPlanCreated       EventKind = "plan_created"

	// Greedy memory hook journal (FR-027).
	EventHookFired EventKind = "memory_hook_fired"

	// Kernel backtrack primitive (autonomy-recovery-runtime-01PMDL03
	// WP01). Fired when the kernel honors a BacktrackRequest and
	// rewinds a completed node + its downstream. Cap-hit on the
	// backtrack budget reuses EventBudgetCapHit (reason
	// "max_backtracks_per_run") rather than a new kind, matching the
	// existing checkBudget pattern.
	EventBacktrackFired EventKind = "backtrack_fired"

	// TaskState persistence (autonomy-recovery-runtime-01PMDL03 WP03).
	// TaskState is the structured "goal / completed-step summary /
	// forbidden-actions" object re-injected on every compute re-entry
	// (task_state.go); these three events are how mutations survive
	// Kernel.RebuildState on resume. FailedAttempts deliberately has NO
	// event of its own here — it is a rendered view over the existing
	// FailureAnnotation records EventBacktrackFired already carries, so
	// resume reconstructs it by replaying that event, not a new one.
	EventTaskGoalSet       EventKind = "task_goal_set"
	EventTaskStepCompleted EventKind = "task_step_completed"
	EventTaskForbidden     EventKind = "task_forbidden_added"

	// Run lifecycle.
	EventRunStart    EventKind = "run_start"
	EventRunComplete EventKind = "run_complete"
	EventRunPaused   EventKind = "run_paused"
)

// AllEventKinds is used by the SQL store + validators to whitelist
// known kinds. Adding a new kind requires bumping migration semantics —
// the table itself accepts arbitrary strings, but consumers should
// only see kinds in this list.
func AllEventKinds() []EventKind {
	return []EventKind{
		EventNodeStart, EventNodeComplete, EventNodeError,
		EventLLMCall, EventToolCall, EventToolResult,
		EventMemoryWrite, EventMemoryRead, EventTraceWrite, EventCheckpoint, EventSessionWrite,
		EventBranchFork, EventBranchMerge, EventForkRequested, EventMergeRequest,
		EventCompactionFired, EventCompactionApplied,
		EventApprovalPending, EventApprovalResolved,
		EventArtifactEmitted, EventKindAliasResolved,
		EventFileRead, EventFileWrite, EventBashOutputRead,
		EventDialOverridden, EventCostCapHit, EventBudgetCapHit,
		EventDoomLoopDetected,
		EventAskPending, EventAskAnswered,
		EventReflectStarted, EventReflectCompleted,
		EventReviewPass, EventReviewFail, EventReviewUnrecov,
		EventEscalateTriggered, EventPlanCreated,
		EventHookFired,
		EventBacktrackFired,
		EventTaskGoalSet, EventTaskStepCompleted, EventTaskForbidden,
		EventRunStart, EventRunComplete, EventRunPaused,
	}
}

// Event is one entry in the log. It is intentionally small: the kernel
// writes a high volume of these (every node start, every node complete,
// every memory hook, etc.) and the on-disk shape mirrors this struct
// closely (see migrations_agent_graph_events.go).
type Event struct {
	// Seq is monotonic per (run_id). Assigned by the EventLog when the
	// batch is committed.
	Seq int64 `json:"seq"`
	// RunID identifies the run this event belongs to.
	RunID string `json:"run_id"`
	// SessionID is the owning session (denormalised for fast lookup;
	// the run already pins this but we copy it to avoid joins on every
	// projection rebuild).
	SessionID string `json:"session_id,omitempty"`
	// NodeID is the firing node when applicable; empty for run-level
	// events (run_start, run_complete, etc.).
	NodeID string `json:"node_id,omitempty"`
	// Kind discriminates the payload interpretation.
	Kind EventKind `json:"kind"`
	// Timestamp is wall-clock at append time. Only the SeqOrder is
	// authoritative for replay; this is for human-readability.
	Timestamp time.Time `json:"ts"`
	// Payload carries kind-specific structured data. Round-trips
	// through json.RawMessage so hot-path appenders don't pay reflection
	// cost twice.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// MarshalPayload is a convenience that JSON-encodes v into Payload.
func (e *Event) MarshalPayload(v any) error {
	if v == nil {
		e.Payload = nil
		return nil
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("agentgraph: marshal payload: %w", err)
	}
	e.Payload = buf
	return nil
}

// EventBatch is a collection of events that must commit atomically.
// The kernel produces one batch per node-fire and hands it to EventLog
// as a unit so partial writes never appear after a crash.
type EventBatch struct {
	Events []Event
}

// Append adds an event to the batch.
func (b *EventBatch) Append(e Event) {
	b.Events = append(b.Events, e)
}

// AppendKind is shorthand for the common case of adding an event with
// a kind + payload. The other fields are filled in by the caller (or
// later by the EventLog at commit time).
func (b *EventBatch) AppendKind(runID, nodeID string, kind EventKind, payload any) error {
	e := Event{
		RunID:     runID,
		NodeID:    nodeID,
		Kind:      kind,
		Timestamp: time.Now().UTC(),
	}
	if err := e.MarshalPayload(payload); err != nil {
		return err
	}
	b.Append(e)
	return nil
}

// Len returns the number of events in the batch.
func (b *EventBatch) Len() int { return len(b.Events) }

// EventLog is the durable append-only stream of events for a run /
// session. Implementations MUST commit batches atomically and assign
// monotonic per-(run_id) sequence numbers.
type EventLog interface {
	// Append commits a batch of events. The implementation MUST assign
	// monotonic Seq numbers per RunID and persist atomically. Returns
	// the highest seq number assigned (== last event's Seq).
	Append(batch EventBatch) (int64, error)
	// Replay walks every event for the given run in seq order and
	// invokes fn. fn MUST NOT panic; an error from fn aborts the walk.
	Replay(runID string, fn func(Event) error) error
	// Len returns the number of events currently stored for a run.
	// Used by tests + the resume path to size pre-allocations.
	Len(runID string) int
	// Close releases any resources (file handles, connections, etc.).
	Close() error
}

// memEventLog is the in-memory EventLog used by tests and as the
// default when the kernel boots without a SQLite-backed log. It is
// safe for concurrent use.
type memEventLog struct {
	mu     sync.Mutex
	rows   map[string][]Event
	nextSeq map[string]int64
}

// NewMemoryEventLog returns an EventLog that lives entirely in RAM.
// Suitable for tests; production callers should pass a SQLEventLog.
func NewMemoryEventLog() EventLog {
	return &memEventLog{
		rows:    make(map[string][]Event),
		nextSeq: make(map[string]int64),
	}
}

func (l *memEventLog) Append(batch EventBatch) (int64, error) {
	if batch.Len() == 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var last int64
	for i := range batch.Events {
		ev := batch.Events[i]
		if ev.RunID == "" {
			return 0, fmt.Errorf("agentgraph: event #%d missing run_id", i)
		}
		l.nextSeq[ev.RunID]++
		ev.Seq = l.nextSeq[ev.RunID]
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		l.rows[ev.RunID] = append(l.rows[ev.RunID], ev)
		last = ev.Seq
	}
	return last, nil
}

func (l *memEventLog) Replay(runID string, fn func(Event) error) error {
	l.mu.Lock()
	rows := append([]Event(nil), l.rows[runID]...)
	l.mu.Unlock()
	for _, e := range rows {
		if err := fn(e); err != nil {
			return err
		}
	}
	return nil
}

func (l *memEventLog) Len(runID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.rows[runID])
}

func (l *memEventLog) Close() error { return nil }
