package toolloop

import "context"

// Session-ID context plumbing for built-in tools.
//
// Built-in tools satisfy BuiltinTool, whose Call signature is
// (ctx, args) — no explicit session ID parameter. Most built-ins
// (websearch, bash) don't need one: they're stateless or scoped to a
// process-wide sandbox. The save_artifact built-in is the exception:
// every call must land an artifacts row keyed by sessionID, and the
// row only makes sense in the context of a live session.
//
// Rather than thread a sessionID parameter through MCPPool / BuiltinPool
// (which would force every MCP call site to grow a parameter that 99%
// of tools ignore), the chassis stuffs the session ID into the context
// at dispatch time and the consuming tool reads it back here.
//
// Convention: dispatch-side callers (kernelToolAdapter and friends)
// invoke WithSessionID(ctx, id) immediately before pool.Call so every
// tool that wants the session ID can ask for it. Tools that don't ask
// pay nothing — the value sits unread in the context.

type sessionIDCtxKey struct{}

// WithSessionID attaches a session ID to ctx. Empty id is a no-op:
// SessionIDFromContext on the resulting context returns "".
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionIDCtxKey{}, sessionID)
}

// SessionIDFromContext returns the session ID attached via
// WithSessionID, or "" if none.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(sessionIDCtxKey{}).(string)
	return v
}

// Turn-span context plumbing (model fork tool, review M1, 2026-10-05).
//
// The turn span is the id of the user message that opened the chat turn
// a tool call runs inside — the same value session.TranscriptEntry.
// TurnSpanID carries. kenaz__fork_conversation reads it so its default
// branch point lands BEFORE the live turn instead of on the still-open
// "please fork this" request. Set by the chat path's kernel tool adapter
// beside WithSessionID; absent everywhere else (workflows, slash
// commands), where readers fall back to their no-span behaviour.

type turnSpanCtxKey struct{}

// WithTurnSpanID attaches the live turn's span id to ctx. Empty id is a
// no-op.
func WithTurnSpanID(ctx context.Context, spanID string) context.Context {
	if spanID == "" {
		return ctx
	}
	return context.WithValue(ctx, turnSpanCtxKey{}, spanID)
}

// TurnSpanIDFromContext returns the span id attached via WithTurnSpanID,
// or "" if none.
func TurnSpanIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(turnSpanCtxKey{}).(string)
	return v
}
