// session_hooks.go — SessionHookRunner seam for hooks-event-surface-expansion.
//
// This file defines the interface the session Manager uses to fire v2
// lifecycle hooks (session_start, setup, cwd_changed) without importing
// core/hooks (which would create an import cycle: hooks → agentgraph →
// session → hooks).
//
// The production wiring (core/rpc) binds a *hooks.Runner-backed adapter
// onto this seam.
package session

import "context"

// SessionHookRunner fires session lifecycle hooks.
// Implementations must be safe for concurrent use.
type SessionHookRunner interface {
	// FireSessionStart fires the session_start event after a new session
	// is created. It may return WatchPaths for the fsnotify watcher and
	// AdditionalContext to prepend to the session's first system message.
	FireSessionStart(ctx context.Context, req SessionStartRequest) (SessionHookResult, error)

	// FireSetup fires the setup event once per session before the first
	// LLM turn. One-time preparation (project rules ingestion, etc.) goes
	// here. Returns AdditionalContext for the first turn's system message.
	FireSetup(ctx context.Context, sessionID, projectID, cwd string) (SessionHookResult, error)

	// FireCwdChanged fires the cwd_changed event when the working directory
	// changes. May return updated WatchPaths.
	FireCwdChanged(ctx context.Context, sessionID, oldCWD, newCWD string) (SessionHookResult, error)
}

// SessionStartRequest is the input to FireSessionStart.
type SessionStartRequest struct {
	SessionID string
	ProjectID string
	CWD       string
	Model     string
}

// SessionHookResult is the subset of hooks.MergedOutput relevant to the
// session lifecycle. Keeping a local type avoids importing core/hooks.
type SessionHookResult struct {
	// WatchPaths is the list of absolute paths to add to the fsnotify
	// watcher for this session.
	WatchPaths []string
	// AdditionalContext is injected as a system message at the start of the
	// session (for session_start) or before the first LLM turn (for setup).
	AdditionalContext string
}
