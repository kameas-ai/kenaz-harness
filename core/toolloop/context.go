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
