// Audit emission for tool execution. The toolloop emits two kinds:
//
//   - tool_invoked — every successful dispatch
//   - tool_failed  — every dispatch that errored, was blocked by the
//     permission gate, or was short-circuited by a pre-hook
//
// AuditEmitter is defined locally so core/toolloop does not import
// core/event directly; the rpc layer adapts the production event-log
// emitter into this surface. Args MUST be passed in raw and are
// forwarded to the emitter as-is — the emitter runs the redaction
// pipeline unconditionally on Append (NFR-003 / C-004), so
// pre-redacting here would double-redact and break placeholder
// stability.
//
// nil-safety: emitToolInvoked / emitToolFailed are no-ops when the
// emitter is nil, so callers that haven't wired a real one (test
// fixtures, the pre-real-emitter rpc path) stay quiet.
package toolloop

import (
	"context"
	"encoding/json"
)

// AuditEmitter is the narrow seam the toolloop calls when emitting
// tool_invoked / tool_failed events. Implementations are expected to
// run their own redaction pipeline (C-004) — the toolloop does not
// pre-scrub args.
type AuditEmitter interface {
	Emit(ctx context.Context, kind string, attrs map[string]any)
}

// emitToolInvoked publishes a tool_invoked audit event for a successful
// dispatch. parentSubID correlates the event with the rpc pump's sub_id
// so a single user turn ties together across pump → loop → MCP server.
func emitToolInvoked(
	ctx context.Context,
	emitter AuditEmitter,
	sessionID, parentSubID, server, tool string,
	args json.RawMessage,
	latencyMS int64,
) {
	if emitter == nil {
		return
	}
	emitter.Emit(ctx, "tool_invoked", map[string]any{
		"session_id":    sessionID,
		"parent_sub_id": parentSubID,
		"server":        server,
		"tool":          tool,
		"args":          args,
		"latency_ms":    latencyMS,
		"status":        "ok",
	})
}

// emitToolFailed publishes a tool_failed audit event for a dispatch
// that errored, was blocked by the permission gate, or was
// short-circuited by a pre-hook. errMsg carries the human-readable
// failure reason; latencyMS is wall time from pre-hook entry to the
// emitter call (may be 0 for synthetic blocks where no dispatch ran).
func emitToolFailed(
	ctx context.Context,
	emitter AuditEmitter,
	sessionID, parentSubID, server, tool string,
	args json.RawMessage,
	errMsg string,
	latencyMS int64,
) {
	if emitter == nil {
		return
	}
	emitter.Emit(ctx, "tool_failed", map[string]any{
		"session_id":    sessionID,
		"parent_sub_id": parentSubID,
		"server":        server,
		"tool":          tool,
		"args":          args,
		"error":         errMsg,
		"latency_ms":    latencyMS,
		"status":        "error",
	})
}
