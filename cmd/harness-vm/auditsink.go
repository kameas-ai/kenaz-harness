// auditsink.go — harness audit-event line-protocol writer.
//
// On every task lifecycle transition (task.start, each tool call, each tool
// result, task.complete) the harness writes ONE line-delimited JSON record to
// the Unix socket named by KENAZ_HARNESS_EVENT_SOCK. The reporter's collector
// source reads these lines, maps "kind" onto a new event.Kind, and the other
// fields onto event metadata (Subject = tool/node, SizeChip = exit/duration).
//
// Wire contract: see kenaz-harness/contracts/harness-audit-events.md.
//
//	{
//	  "kind":        "task.start" | "task.tool_call" | "task.tool_result" | "task.complete",
//	  "ts":          <unix_ms int>,
//	  "task_id":     "<string>",
//	  "tool":        "<tool name or empty>",
//	  "node":        "<graph node kind or empty>",
//	  "exit_code":   <int>,
//	  "duration_ms": <int>
//	}
//
// PRIVACY (HARD GATE). The audit line-protocol is METADATA-ONLY: it carries NO
// prompt text, NO tool arguments, and NO tool output bodies — ever. auditRecord
// has no field that can hold user-authored content, so the type system itself
// fails closed: there is no code path that can place a prompt/arg/output body
// onto the wire. The only string-typed fields are `tool` (a tool *name*) and
// `node` (a graph node *kind*) — both structural identifiers, never content.
//
// Best-effort + non-blocking. When KENAZ_HARNESS_EVENT_SOCK is unset (the dev /
// host-mode path), the sink is disabled and every method is a no-op — a missing
// socket NEVER blocks or fails a task. Emission dials per-record with a bounded
// timeout and drops the record silently on any error.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"
)

// auditSocketEnv names the Unix socket the harness writes audit lines to. When
// unset, audit emission is disabled (the task surface runs unchanged).
const auditSocketEnv = "KENAZ_HARNESS_EVENT_SOCK"

// Audit-event kinds. Stable wire strings — the reporter's collector source keys
// off these to derive the event.Kind.
const (
	auditKindTaskStart    = "task.start"
	auditKindToolCall     = "task.tool_call"
	auditKindToolResult   = "task.tool_result"
	auditKindTaskComplete = "task.complete"
)

// auditRecord is the on-wire shape. METADATA-ONLY: every field is either a
// structural identifier (kind, task_id, tool name, node kind) or a numeric
// measurement (timestamps, exit code, duration). There is deliberately NO field
// that can carry a prompt, tool arguments, or tool output — the privacy
// invariant is enforced by the type, not by convention.
type auditRecord struct {
	Kind       string `json:"kind"`
	TS         int64  `json:"ts"` // unix milliseconds
	TaskID     string `json:"task_id"`
	Tool       string `json:"tool"`        // tool name, or "" when not a tool event
	Node       string `json:"node"`        // graph node kind, or "" when not a node event
	ExitCode   int    `json:"exit_code"`   // 0 unless a tool/task failed
	DurationMs int64  `json:"duration_ms"` // 0 unless measured
}

// auditSink writes audit records to the configured Unix socket.
//
// Design mirrors the privacy-and-availability discipline of the OTEL span path
// (core/tasks/telemetry.go): the audit line is emitted ALONGSIDE the span at the
// same hook points, with the same metadata-only attribute set. Both are
// best-effort; neither blocks the task.
//
//   - Disabled cleanly: empty addr ⇒ every method is a no-op. A nil *auditSink
//     is also a valid no-op receiver, so call sites need no nil checks.
//   - Fire-and-forget: each emit dials, writes one line, and closes with short
//     deadlines. A slow or absent reader degrades to "no audit record", never
//     to a stalled task.
//   - Single dependency: stdlib only (net, encoding/json).
type auditSink struct {
	addr         string // unix socket path; "" disables
	network      string // "unix" (default) — settable for tests ("tcp")
	dialTimeout  time.Duration
	writeTimeout time.Duration
	log          *slog.Logger

	mu     sync.Mutex
	dialFn func(network, addr string) (net.Conn, error) // injectable for tests
}

// newAuditSink builds a sink targeting addr. When addr is empty the returned
// sink is disabled (all methods no-op). network defaults to "unix" when empty.
func newAuditSink(addr, network string, log *slog.Logger) *auditSink {
	if network == "" {
		network = "unix"
	}
	if log == nil {
		log = slog.Default()
	}
	const dialTimeout = 500 * time.Millisecond
	return &auditSink{
		addr:         addr,
		network:      network,
		dialTimeout:  dialTimeout,
		writeTimeout: 500 * time.Millisecond,
		log:          log,
		// Bounded dial so a stalled reader never holds a task goroutine.
		dialFn: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, dialTimeout)
		},
	}
}

// enabled reports whether emission is active (socket configured).
func (a *auditSink) enabled() bool {
	return a != nil && a.addr != ""
}

// emitTaskStart records the beginning of a task. No content is recorded — only
// the task id and a timestamp.
func (a *auditSink) emitTaskStart(taskID string) {
	a.emit(auditRecord{Kind: auditKindTaskStart, TaskID: taskID})
}

// emitToolCall records the start of a single tool/node invocation by name. The
// tool name and node kind are structural identifiers; tool arguments are NEVER
// recorded.
func (a *auditSink) emitToolCall(taskID, tool, node string) {
	a.emit(auditRecord{Kind: auditKindToolCall, TaskID: taskID, Tool: tool, Node: node})
}

// emitToolResult records the completion of a tool/node invocation: its exit
// code (0 = ok, non-zero = failure) and duration. Tool output bodies are NEVER
// recorded.
func (a *auditSink) emitToolResult(taskID, tool, node string, exitCode int, durationMs int64) {
	a.emit(auditRecord{
		Kind:       auditKindToolResult,
		TaskID:     taskID,
		Tool:       tool,
		Node:       node,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	})
}

// emitTaskComplete records terminal completion of a task with its overall exit
// code and wall-clock duration.
func (a *auditSink) emitTaskComplete(taskID string, exitCode int, durationMs int64) {
	a.emit(auditRecord{
		Kind:       auditKindTaskComplete,
		TaskID:     taskID,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	})
}

// emit stamps the record's timestamp and pushes it. No-op when disabled. Never
// blocks the caller beyond the bounded dial+write deadlines; errors are logged
// at Debug (a missing reader is expected on the dev path) and swallowed.
func (a *auditSink) emit(rec auditRecord) {
	if !a.enabled() {
		return
	}
	if rec.TS == 0 {
		rec.TS = time.Now().UnixMilli()
	}

	line, err := json.Marshal(rec)
	if err != nil {
		// Should never happen for a struct of scalars; log and drop.
		a.log.Debug("audit: marshal failed", "kind", rec.Kind, "err", err)
		return
	}
	line = append(line, '\n')

	a.mu.Lock()
	dial := a.dialFn
	a.mu.Unlock()

	conn, err := dial(a.network, a.addr)
	if err != nil {
		a.log.Debug("audit: dial event socket failed (record dropped)",
			"kind", rec.Kind, "addr", a.addr, "err", err)
		return
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetWriteDeadline(time.Now().Add(a.writeTimeout))
	if _, err := conn.Write(line); err != nil {
		a.log.Debug("audit: write event socket failed (record dropped)",
			"kind", rec.Kind, "err", err)
		return
	}
	a.log.Debug("audit: emitted", "kind", rec.Kind, "task_id", rec.TaskID)
}

// --- agentgraph.TraceSink adapter ---
//
// nodeTracer adapts the audit sink to agentgraph.TraceSink so a real core/ graph
// run drives tool_call / tool_result audit lines automatically. The kernel calls
// Span("node.<kind>", {node_id, run_id}) on every node fire and calls the
// returned end(err) closure when the node completes. We map:
//
//	Span()   → task.tool_call  (tool = node_id, node = node kind)
//	end(err) → task.tool_result (exit_code derived from err, duration measured)
//
// The kernel only ever passes structural attrs (node_id, run_id) — never prompt
// text or tool I/O — so this adapter is metadata-only by construction.
type nodeTracer struct {
	sink   *auditSink
	taskID string
}

// newNodeTracer returns a TraceSink-shaped tracer bound to a task. A nil sink
// (or disabled sink) yields a tracer whose spans are cheap no-ops.
func newNodeTracer(sink *auditSink, taskID string) *nodeTracer {
	return &nodeTracer{sink: sink, taskID: taskID}
}

// Span satisfies agentgraph.TraceSink. name is "node.<kind>"; attrs carries
// node_id. We emit a tool_call on entry and a tool_result from the end closure.
func (nt *nodeTracer) Span(ctx context.Context, name string, attrs map[string]any) (context.Context, func(error)) {
	nodeKind := name
	if len(name) > len("node.") && name[:len("node.")] == "node." {
		nodeKind = name[len("node."):]
	}
	nodeID, _ := attrs["node_id"].(string)

	nt.sink.emitToolCall(nt.taskID, nodeID, nodeKind)
	start := time.Now()

	return ctx, func(err error) {
		exit := 0
		if err != nil {
			exit = 1
		}
		nt.sink.emitToolResult(nt.taskID, nodeID, nodeKind, exit, time.Since(start).Milliseconds())
	}
}
