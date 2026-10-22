// Package auditsink is the harness audit-event line-protocol writer, shared
// by the in-VM task surface (cmd/harness-vm) and the served harness
// (cmd/harness-served, audit finding WC-6). It moved here from
// cmd/harness-vm/auditsink.go so the served chat path can emit the same
// metadata-only records instead of nothing.
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
// prompt text, NO tool arguments, and NO tool output bodies — ever. Record has
// no field that can hold user-authored content, so the type system itself
// fails closed: there is no code path that can place a prompt/arg/output body
// onto the wire. The only string-typed fields are `tool` (a tool *name*) and
// `node` (a graph node *kind*) — both structural identifiers, never content.
//
// Best-effort + non-blocking. When KENAZ_HARNESS_EVENT_SOCK is unset (the dev /
// host-mode path), the sink is disabled and every method is a no-op — a missing
// socket NEVER blocks or fails a task. Emission dials per-record with a bounded
// timeout and drops the record silently on any error.
package auditsink

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// auditSocketEnv names the Unix socket the harness writes audit lines to. When
// unset, audit emission is disabled (the task surface runs unchanged).
const SocketEnv = "KENAZ_HARNESS_EVENT_SOCK"

// Audit-event kinds. Stable wire strings — the reporter's collector source keys
// off these to derive the event.Kind.
const (
	KindTaskStart    = "task.start"
	KindToolCall     = "task.tool_call"
	KindToolResult   = "task.tool_result"
	KindTaskComplete = "task.complete"
)

// Record is the on-wire shape. METADATA-ONLY: every field is either a
// structural identifier (kind, task_id, tool name, node kind) or a numeric
// measurement (timestamps, exit code, duration). There is deliberately NO field
// that can carry a prompt, tool arguments, or tool output — the privacy
// invariant is enforced by the type, not by convention.
type Record struct {
	Kind       string `json:"kind"`
	TS         int64  `json:"ts"` // unix milliseconds
	TaskID     string `json:"task_id"`
	Tool       string `json:"tool"`        // tool name, or "" when not a tool event
	Node       string `json:"node"`        // graph node kind, or "" when not a node event
	ExitCode   int    `json:"exit_code"`   // 0 unless a tool/task failed
	DurationMs int64  `json:"duration_ms"` // 0 unless measured
}

// Sink writes audit records to the configured Unix socket.
//
// Design mirrors the privacy-and-availability discipline of the OTEL span path
// (core/tasks/telemetry.go): the audit line is emitted ALONGSIDE the span at the
// same hook points, with the same metadata-only attribute set. Both are
// best-effort; neither blocks the task.
//
//   - Disabled cleanly: empty addr ⇒ every method is a no-op. A nil *Sink
//     is also a valid no-op receiver, so call sites need no nil checks.
//   - Fire-and-forget: each emit dials, writes one line, and closes with short
//     deadlines. A slow or absent reader degrades to "no audit record", never
//     to a stalled task.
//   - Single dependency: stdlib only (net, encoding/json).
type Sink struct {
	addr         string // unix socket path; "" disables
	network      string // "unix" (default) — settable for tests ("tcp")
	dialTimeout  time.Duration
	writeTimeout time.Duration
	log          *slog.Logger

	mu     sync.Mutex
	dialFn func(network, addr string) (net.Conn, error) // injectable for tests
}

// New builds a sink targeting addr. When addr is empty the returned
// sink is disabled (all methods no-op). network defaults to "unix" when empty.
func New(addr, network string, log *slog.Logger) *Sink {
	if network == "" {
		network = "unix"
	}
	if log == nil {
		log = slog.Default()
	}
	const dialTimeout = 500 * time.Millisecond
	return &Sink{
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

// Addr returns the configured socket address ("" when disabled).
func (a *Sink) Addr() string {
	if a == nil {
		return ""
	}
	return a.addr
}

// Enabled reports whether emission is active (socket configured).
func (a *Sink) Enabled() bool {
	return a != nil && a.addr != ""
}

// EmitTaskStart records the beginning of a task. No content is recorded — only
// the task id and a timestamp.
func (a *Sink) EmitTaskStart(taskID string) {
	a.emit(Record{Kind: KindTaskStart, TaskID: taskID})
}

// EmitToolCall records the start of a single tool/node invocation by name. The
// tool name and node kind are structural identifiers; tool arguments are NEVER
// recorded.
func (a *Sink) EmitToolCall(taskID, tool, node string) {
	a.emit(Record{Kind: KindToolCall, TaskID: taskID, Tool: tool, Node: node})
}

// EmitToolResult records the completion of a tool/node invocation: its exit
// code (0 = ok, non-zero = failure) and duration. Tool output bodies are NEVER
// recorded.
func (a *Sink) EmitToolResult(taskID, tool, node string, exitCode int, durationMs int64) {
	a.emit(Record{
		Kind:       KindToolResult,
		TaskID:     taskID,
		Tool:       tool,
		Node:       node,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	})
}

// EmitTaskComplete records terminal completion of a task with its overall exit
// code and wall-clock duration.
func (a *Sink) EmitTaskComplete(taskID string, exitCode int, durationMs int64) {
	a.emit(Record{
		Kind:       KindTaskComplete,
		TaskID:     taskID,
		ExitCode:   exitCode,
		DurationMs: durationMs,
	})
}

// emit stamps the record's timestamp and pushes it. No-op when disabled. Never
// blocks the caller beyond the bounded dial+write deadlines; errors are logged
// at Debug (a missing reader is expected on the dev path) and swallowed.
func (a *Sink) emit(rec Record) {
	if !a.Enabled() {
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
// NodeTracer adapts the audit sink to agentgraph.TraceSink so a real core/ graph
// run drives tool_call / tool_result audit lines automatically. The kernel calls
// Span("node.<kind>", {node_id, run_id}) on every node fire and calls the
// returned end(err) closure when the node completes. We map:
//
//	Span()   → task.tool_call  (tool = node_id, node = node kind)
//	end(err) → task.tool_result (exit_code derived from err, duration measured)
//
// The kernel only ever passes structural attrs (node_id, run_id) — never prompt
// text or tool I/O — so this adapter is metadata-only by construction.
type NodeTracer struct {
	sink   *Sink
	taskID string
}

// NewNodeTracer returns a TraceSink-shaped tracer bound to a task. A nil sink
// (or disabled sink) yields a tracer whose spans are cheap no-ops.
func NewNodeTracer(sink *Sink, taskID string) *NodeTracer {
	return &NodeTracer{sink: sink, taskID: taskID}
}

// Span satisfies agentgraph.TraceSink. name is "node.<kind>"; attrs carries
// node_id. We emit a tool_call on entry and a tool_result from the end closure.
func (nt *NodeTracer) Span(ctx context.Context, name string, attrs map[string]any) (context.Context, func(error)) {
	nodeKind := name
	if len(name) > len("node.") && name[:len("node.")] == "node." {
		nodeKind = name[len("node."):]
	}
	nodeID, _ := attrs["node_id"].(string)

	nt.sink.EmitToolCall(nt.taskID, nodeID, nodeKind)
	start := time.Now()

	return ctx, func(err error) {
		exit := 0
		if err != nil {
			exit = 1
		}
		nt.sink.EmitToolResult(nt.taskID, nodeID, nodeKind, exit, time.Since(start).Milliseconds())
	}
}

// --- process default + per-task context (served chat path, WP-free: WC-6) ---
//
// The served harness has no task RPC: its "task" is a chat turn, run by
// core/rpc/views/agentgraph/chat, and its tool calls go through that
// package's kernelToolAdapter. Threading a *Sink through rpc.New → the
// chat runner config would touch every constructor in between for one
// optional, process-wide writer, so cmd/harness-served installs it once
// with SetDefault and the chat path reads Default(). Nothing else sets it:
// the desktop build and tests leave it nil, which makes every helper below
// a no-op.

var defaultSink atomic.Pointer[Sink]

// SetDefault installs the process-wide sink the chat path emits to. Pass
// nil to clear it. Only a binary's main should call this.
func SetDefault(s *Sink) { defaultSink.Store(s) }

// Default returns the process-wide sink, or nil when none is installed. A
// nil *Sink is a valid no-op receiver.
func Default() *Sink { return defaultSink.Load() }

type taskIDKey struct{}

// WithTask stamps the audit task id (the chat turn's run id) on ctx so a
// tool call made under it can be attributed to the turn.
func WithTask(ctx context.Context, taskID string) context.Context {
	if taskID == "" {
		return ctx
	}
	return context.WithValue(ctx, taskIDKey{}, taskID)
}

// TaskFromContext returns the task id WithTask stamped, or "".
func TaskFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(taskIDKey{}).(string)
	return id
}

// Tool records task.tool_call for the tool named tool against the task on
// ctx, on the default sink, and returns the function that records the
// matching task.tool_result. failed=true writes exit_code 1. When there is
// no default sink or no task on ctx both halves are no-ops, so the call
// site needs no checks. tool must be a tool NAME, never arguments or
// output.
func Tool(ctx context.Context, tool string) func(failed bool) {
	s := Default()
	taskID := TaskFromContext(ctx)
	if !s.Enabled() || taskID == "" {
		return func(bool) {}
	}
	s.EmitToolCall(taskID, tool, "")
	start := time.Now()
	return func(failed bool) {
		exit := 0
		if failed {
			exit = 1
		}
		s.EmitToolResult(taskID, tool, "", exit, time.Since(start).Milliseconds())
	}
}
