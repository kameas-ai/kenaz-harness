package toolloop

import (
	"context"
	"errors"
)

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

// ErrSessionIDMismatch is returned by WithSessionIDChecked when ctx already
// carries a session id and a DIFFERENT one is offered.
var ErrSessionIDMismatch = errors.New("toolloop: ctx already carries a different session id")

// WithSessionIDChecked is WithSessionID for call sites whose id comes from
// data rather than from the dispatcher that owns the call (e.g. a slash
// command's SessionContext). It only FILLS an empty ctx: if ctx already
// carries a session id, an empty or equal id keeps it, and a different
// one is refused with ErrSessionIDMismatch — a session id from data can
// never override the session the call is actually running in
// (model-harness-toolset-01MHTS001 WP02 security review, H1: that
// override let a forged id escape per-session permission resolution).
func WithSessionIDChecked(ctx context.Context, sessionID string) (context.Context, error) {
	cur := SessionIDFromContext(ctx)
	if cur == "" {
		return WithSessionID(ctx, sessionID), nil
	}
	if sessionID == "" || sessionID == cur {
		return ctx, nil
	}
	return ctx, ErrSessionIDMismatch
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

// Visibility-probe context plumbing (model-harness-toolset-01MHTS001
// WP02).
//
// PermissionResolver.Resolve is asked two different questions through
// one signature: "may this call dispatch?" (kernelToolAdapter, the slash
// and workflow dispatchers) and "should this tool be LISTED for the
// session?" (core/rpc/views/llm's mcpToolDiscoverer, which drops denied
// tools so visibility matches reachability). The verdict must be the
// same for both — that is the point of filtering the listing through the
// resolver — but a resolver that RECORDS a denial (the scheduled-run
// allowlist arm writes a blocked_permission_requests row plus an audit
// record per refused call) must not record one for every off-list tool
// each time the catalog is listed: the model never asked for those.
//
// The discoverer marks its ctx with WithVisibilityProbe; a recording
// resolver checks IsVisibilityProbe and skips the record, never the
// verdict. A listing path that forgets the mark over-records (noise),
// it never under-denies.

type visibilityProbeCtxKey struct{}

// WithVisibilityProbe marks ctx as a listing-time permission probe, not
// a dispatch.
func WithVisibilityProbe(ctx context.Context) context.Context {
	return context.WithValue(ctx, visibilityProbeCtxKey{}, true)
}

// IsVisibilityProbe reports whether ctx was marked by WithVisibilityProbe.
func IsVisibilityProbe(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(visibilityProbeCtxKey{}).(bool)
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
