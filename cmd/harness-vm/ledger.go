// ledger.go — in-VM harness task-surface → reporter ledger bridge.
//
// criterion #5 / M1 finding #11: a dispatched harness task must emit its
// lifecycle (task.start, tool_call, task.complete / task.cancelled) to the
// host ledger so the operator's audit timeline shows agent activity with the
// correct workbench id.
//
// Event path (this file is the FIRST hop):
//
//	harness-vm runTask ── ledgerEmitter ──▶ reporter ingest socket
//	                                            │  (sigil TerminalSource, a
//	                                            │   Unix-socket "ingest" handler
//	                                            ▼   feeding the collector)
//	                              in-VM sigild-vm-reporter spool
//	                                            │  (vsock/TCP to NAT gateway)
//	                                            ▼
//	                              host sigildlink ingest accept
//	                                            ▼
//	                              reporter_events row in data.db
//
// This file owns only the first hop: turning task lifecycle into ledger
// records and writing them to the ingest endpoint. The downstream reporter
// (sigil cmd/sigild-vm-reporter) and host ingest (kenaz internal/vmhost) are
// separate deliverables; see the workstream brief's "Deferred" section.
//
// Wire contract toward the reporter (matches sigil internal/event.Event so the
// reporter's TerminalSource can ingest it without translation):
//
//	{
//	  "kind": "ai",                       // event.KindAI — AI/agent interaction
//	  "source": "harness",                // distinguishes from shell-hook terminal
//	  "payload": {
//	     "phase": "task.start",           // task.start | tool_call | task.complete | task.cancelled
//	     "task_id": "<id>",
//	     "workbench_id": "<id>",          // from SIGIL_WORKBENCH_ID, for ledger attribution
//	     "tool": "<name>",                // tool_call only
//	     "prompt_len": <int>              // task.start only — length, never the prompt text (privacy)
//	  },
//	  "timestamp": "2026-06-07T..."       // RFC3339
//	}
//
// Records are newline-delimited JSON (NDJSON) — one object per line — matching
// the framing the sigil terminal-ingest socket handler expects.
//
// Privacy: the emitter never writes prompt text, tool arguments, or task
// output to the ledger. Only structural metadata (phase, ids, tool name,
// prompt length) crosses the bridge. This mirrors sigil's "no user-content
// fields" discipline.
package main

import (
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"
)

// ledgerSocketEnv is the env var naming the reporter ingest endpoint inside
// the VM. When unset, ledger emission is disabled (the task surface still
// works; it just produces no audit records — the dev / no-reporter path).
const ledgerSocketEnv = "SIGIL_INGEST_SOCKET"

// ledger lifecycle phases. Stable strings — the reporter and the host
// audit timeline key off these.
const (
	phaseTaskStart     = "task.start"
	phaseToolCall      = "tool_call"
	phaseTaskComplete  = "task.complete"
	phaseTaskCancelled = "task.cancelled"
)

// ledgerEventKind / ledgerSource mirror sigil internal/event constants
// (event.KindAI, a "source" string). Duplicated as literals here rather than
// importing sigil to keep kenaz-harness free of a sigil module dependency;
// the values are part of the frozen ingest contract.
const (
	ledgerEventKind = "ai"
	ledgerSource    = "harness"
)

// ledgerRecord is the on-wire shape pushed to the reporter ingest socket.
// JSON tags match sigil internal/event.Event so the reporter's TerminalSource
// can decode it directly.
type ledgerRecord struct {
	Kind      string         `json:"kind"`
	Source    string         `json:"source"`
	Payload   map[string]any `json:"payload"`
	Timestamp time.Time      `json:"timestamp"`
}

// ledgerEmitter writes task-lifecycle records to the reporter ingest socket.
//
// Design constraints:
//   - Fire-and-forget: emission MUST NOT block or fail the task path. A slow
//     or absent reporter degrades to "no audit record", never to a stalled
//     task. Each emit dials, writes one line, and closes with short deadlines.
//   - Disabled cleanly: when the socket address is empty (env unset), every
//     method is a no-op. A nil *ledgerEmitter is also a valid no-op receiver,
//     so call sites need no nil checks.
//   - Single dependency: stdlib only (net, encoding/json). Adding a dep would
//     change the reporter image's vendorHash (finding #6) — avoid.
type ledgerEmitter struct {
	addr         string // unix socket path; "" disables
	network      string // "unix" (default) — settable for tests ("tcp")
	workbenchID  string
	dialTimeout  time.Duration
	writeTimeout time.Duration
	log          *slog.Logger

	mu     sync.Mutex
	dialFn func(network, addr string) (net.Conn, error) // injectable for tests
}

// newLedgerEmitter builds an emitter targeting addr. When addr is empty the
// returned emitter is disabled (all methods no-op). network defaults to
// "unix" when empty.
func newLedgerEmitter(addr, network, workbenchID string, log *slog.Logger) *ledgerEmitter {
	if network == "" {
		network = "unix"
	}
	if log == nil {
		log = slog.Default()
	}
	const dialTimeout = 500 * time.Millisecond
	return &ledgerEmitter{
		addr:         addr,
		network:      network,
		workbenchID:  workbenchID,
		dialTimeout:  dialTimeout,
		writeTimeout: 500 * time.Millisecond,
		log:          log,
		// Bounded dial so a stalled reporter never holds a task goroutine.
		dialFn: func(network, addr string) (net.Conn, error) {
			return net.DialTimeout(network, addr, dialTimeout)
		},
	}
}

// enabled reports whether emission is active (socket configured).
func (e *ledgerEmitter) enabled() bool {
	return e != nil && e.addr != ""
}

// emitTaskStart records the beginning of a task. promptLen is the prompt's
// length in bytes; the prompt text itself is never recorded.
func (e *ledgerEmitter) emitTaskStart(taskID string, promptLen int) {
	e.emit(taskID, phaseTaskStart, map[string]any{"prompt_len": promptLen})
}

// emitToolCall records a single tool invocation by name. Tool arguments are
// never recorded.
func (e *ledgerEmitter) emitToolCall(taskID, tool string) {
	e.emit(taskID, phaseToolCall, map[string]any{"tool": tool})
}

// emitTaskComplete records successful terminal completion of a task.
func (e *ledgerEmitter) emitTaskComplete(taskID string) {
	e.emit(taskID, phaseTaskComplete, nil)
}

// emitTaskCancelled records cancellation (terminal) of a task.
func (e *ledgerEmitter) emitTaskCancelled(taskID string) {
	e.emit(taskID, phaseTaskCancelled, nil)
}

// emit builds a ledgerRecord and pushes it. No-op when disabled. Never blocks
// the caller beyond the bounded dial+write deadlines; errors are logged at
// Debug (a missing reporter is expected on the dev path) and swallowed.
func (e *ledgerEmitter) emit(taskID, phase string, extra map[string]any) {
	if !e.enabled() {
		return
	}

	payload := map[string]any{
		"phase":        phase,
		"task_id":      taskID,
		"workbench_id": e.workbenchID,
	}
	for k, v := range extra {
		payload[k] = v
	}

	rec := ledgerRecord{
		Kind:      ledgerEventKind,
		Source:    ledgerSource,
		Payload:   payload,
		Timestamp: time.Now().UTC(),
	}

	line, err := json.Marshal(rec)
	if err != nil {
		// Should never happen for map[string]any of scalars; log and drop.
		e.log.Debug("ledger: marshal failed", "phase", phase, "err", err)
		return
	}
	line = append(line, '\n')

	// Serialise dials so concurrent task goroutines don't interleave on a
	// stream socket; each emit is its own short-lived connection.
	e.mu.Lock()
	dial := e.dialFn
	e.mu.Unlock()

	conn, err := dial(e.network, e.addr)
	if err != nil {
		e.log.Debug("ledger: dial reporter ingest failed (record dropped)",
			"phase", phase, "addr", e.addr, "err", err)
		return
	}
	defer conn.Close()

	_ = conn.SetWriteDeadline(time.Now().Add(e.writeTimeout))
	if _, err := conn.Write(line); err != nil {
		e.log.Debug("ledger: write reporter ingest failed (record dropped)",
			"phase", phase, "err", err)
		return
	}
	e.log.Debug("ledger: emitted", "phase", phase, "task_id", taskID)
}
