// auditsink.go — the in-VM task surface's handle on the harness audit-event
// line-protocol writer.
//
// The writer itself lives in core/auditsink (moved there for audit finding
// WC-6 so the served harness can emit the same metadata-only records per
// chat turn). This file keeps the unexported names the task surface and its
// tests already use; every method forwards to the shared implementation.
//
// Wire contract and privacy invariant: see core/auditsink and
// contracts/harness-audit-events.md.
package main

import (
	"log/slog"

	"github.com/kameas-ai/kenaz-harness/core/auditsink"
)

// auditSocketEnv names the Unix socket the harness writes audit lines to.
const auditSocketEnv = auditsink.SocketEnv

// Audit-event kinds. Stable wire strings.
const (
	auditKindTaskStart    = auditsink.KindTaskStart
	auditKindToolCall     = auditsink.KindToolCall
	auditKindToolResult   = auditsink.KindToolResult
	auditKindTaskComplete = auditsink.KindTaskComplete
)

// auditRecord is the on-wire shape (metadata only).
type auditRecord = auditsink.Record

// auditSink wraps *auditsink.Sink. A nil *auditSink is a valid no-op
// receiver, as before the move.
type auditSink struct {
	s    *auditsink.Sink
	addr string
}

// newAuditSink builds a sink targeting addr. Empty addr ⇒ disabled.
func newAuditSink(addr, network string, log *slog.Logger) *auditSink {
	return &auditSink{s: auditsink.New(addr, network, log), addr: addr}
}

func (a *auditSink) inner() *auditsink.Sink {
	if a == nil {
		return nil
	}
	return a.s
}

func (a *auditSink) enabled() bool { return a.inner().Enabled() }

func (a *auditSink) emitTaskStart(taskID string) { a.inner().EmitTaskStart(taskID) }

func (a *auditSink) emitToolCall(taskID, tool, node string) {
	a.inner().EmitToolCall(taskID, tool, node)
}

func (a *auditSink) emitToolResult(taskID, tool, node string, exitCode int, durationMs int64) {
	a.inner().EmitToolResult(taskID, tool, node, exitCode, durationMs)
}

func (a *auditSink) emitTaskComplete(taskID string, exitCode int, durationMs int64) {
	a.inner().EmitTaskComplete(taskID, exitCode, durationMs)
}

// nodeTracer adapts the sink to agentgraph.TraceSink (see
// auditsink.NodeTracer).
type nodeTracer = auditsink.NodeTracer

// newNodeTracer returns a TraceSink-shaped tracer bound to a task.
func newNodeTracer(sink *auditSink, taskID string) *nodeTracer {
	return auditsink.NewNodeTracer(sink.inner(), taskID)
}
