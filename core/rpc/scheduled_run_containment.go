package rpc

// ScheduledRunContainmentRegistry — the per-session tool allowlist a fired
// scheduled chat run executes under (model-harness-toolset-01MHTS001 WP02,
// finding H-1).
//
// Owner ruling B-3 made a model-created schedule's tool allowlist "the only
// boundary" (there is no human review moment), and the B-3 Cedar policy
// file assigned per-run enforcement to "harness-self-attach-01PMHS01's
// merged PermissionResolver". Until this file nothing did it: the fire path
// reduced the allowlist to a has-an-allowlist boolean and the run could
// call any tool. This registry is the per-session arm of that merged
// resolver:
//
//   - WRITER: LiveChatRunDispatcher.Contain(sessionID, …) after creating
//     the run's headless session and BEFORE StartStream, using the
//     boundary scheduler.ResolveRunContainment computed. The subagent run
//     spawner calls Inherit(child, parent) so a child a contained run
//     dispatches is bound by the same list rather than escaping it.
//   - READER: cedarSessionKindResolver.Resolve (the session arm of
//     toolloop.NewMergedResolver(staticPerms, sessionArm), api.go) calls
//     Check FIRST for every (server, tool) on every session. An off-list
//     tool resolves to PolicyDeny; the deny is a session-arm match, so it
//     wins over the static arm. An on-list tool falls through to the
//     arm's ordinary Cedar evaluation — the allowlist only narrows.
//   - RECORD: every off-list DISPATCH (not a listing probe — see
//     toolloop.WithVisibilityProbe) writes a blocked_permission_requests
//     row (family "tool", action "use_tool", resource the published tool
//     name, origin scheduled_chat_run) and a
//     policy.blocked_permission_request audit record through the same
//     blockedRequestSink the fs gate uses. A failed record never changes
//     the deny.
//
// Lifetime (security review L4, re-review): an entry is released when one
// of the run's OWN streams reports a terminal event — the dispatched
// stream, or a key-rotation redrive of it (RedriveLastTurn re-runs the
// turn under a new sub id, linked by chat.AuthResumedPayload.PausedSubID,
// and stays contained while it runs). A stream someone else runs in the
// same session (a user who opened the "Scheduled:" session mid-run) never
// releases it. A run that times out or is cancelled keeps its entry until
// its own stream ends (LiveChatRunDispatcher.releaseOnOwnTerminal); if
// none does within containmentWatchMax the entry is kept (fail-safe). A
// stream that never started releases immediately.
//
// Safe for concurrent use.

import (
	"context"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// toolFamilyBlocked is the blocked_permission_requests.family value for a
// tool refused by a scheduled run's allowlist. The column was designed for
// a future non-fs family (corefs.BlockedRequest.Family's doc); Grant in
// views/blockedrequests refuses non-fs families (ErrUnsupportedFamily), so
// these rows are recorded and dismissable, never auto-grantable — a
// widened allowlist is an edit to the schedule, not a Cedar snippet.
const toolFamilyBlocked = "tool"

type runContainment struct {
	chatRunID string
	allow     map[string]struct{}
}

// ScheduledRunContainmentRegistry maps a session id to the allowlist its
// run is contained to.
type ScheduledRunContainmentRegistry struct {
	mu   sync.RWMutex
	m    map[string]runContainment
	sink corefs.BlockedRequestSink
}

// NewScheduledRunContainmentRegistry returns an empty registry recording
// refused calls through sink (nil records nothing; the deny still holds).
func NewScheduledRunContainmentRegistry(sink corefs.BlockedRequestSink) *ScheduledRunContainmentRegistry {
	return &ScheduledRunContainmentRegistry{m: make(map[string]runContainment), sink: sink}
}

// Contain binds sessionID to allow (published tool names, e.g.
// "kenaz__web_fetch"). An empty allow denies every tool.
func (r *ScheduledRunContainmentRegistry) Contain(sessionID, chatRunID string, allow []string) {
	if r == nil || sessionID == "" {
		return
	}
	set := make(map[string]struct{}, len(allow))
	for _, n := range allow {
		set[n] = struct{}{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sessionID] = runContainment{chatRunID: chatRunID, allow: set}
}

// Inherit binds childSessionID to the containment parentSessionID is under,
// if any, and reports whether it did. Used by the subagent run spawner so a
// contained run cannot widen itself by dispatching a child.
func (r *ScheduledRunContainmentRegistry) Inherit(childSessionID, parentSessionID string) bool {
	if r == nil || childSessionID == "" || parentSessionID == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.m[parentSessionID]
	if !ok {
		return false
	}
	r.m[childSessionID] = c
	return true
}

// Release drops sessionID's containment. Call only once the session's run
// is known to be over (see the file doc's "Lifetime").
func (r *ScheduledRunContainmentRegistry) Release(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, sessionID)
}

// Contained reports whether sessionID is currently contained.
func (r *ScheduledRunContainmentRegistry) Contained(sessionID string) bool {
	if r == nil || sessionID == "" {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.m[sessionID]
	return ok
}

// Check returns (deny resolution, true) when sessionID is contained and
// server__tool is not on its allowlist, recording the refusal unless ctx is
// a visibility probe. It returns (zero, false) for an uncontained session
// or an allowed tool — the caller then evaluates as it always did.
func (r *ScheduledRunContainmentRegistry) Check(ctx context.Context, sessionID, server, tool string) (toolloop.Resolution, bool) {
	if r == nil || sessionID == "" {
		return toolloop.Resolution{}, false
	}
	r.mu.RLock()
	c, ok := r.m[sessionID]
	r.mu.RUnlock()
	if !ok {
		return toolloop.Resolution{}, false
	}
	name := server + "__" + tool
	if _, allowed := c.allow[name]; allowed {
		return toolloop.Resolution{}, false
	}
	reason := "not on this scheduled run's tool allowlist"
	if !toolloop.IsVisibilityProbe(ctx) && r.sink != nil {
		if err := r.sink.RecordBlocked(ctx, corefs.BlockedRequest{
			Origin:    corefs.OriginScheduledChatRun,
			OriginID:  c.chatRunID,
			SessionID: sessionID,
			Family:    toolFamilyBlocked,
			Action:    "use_tool",
			Resource:  name,
			Reason:    reason,
		}); err != nil {
			logging.L().Warn("scheduler.containment.record_failed",
				"chat_run_id", c.chatRunID, "session_id", sessionID,
				"tool", name, "err", err.Error())
		}
	}
	return toolloop.Resolution{
		Server: server,
		Tool:   tool,
		Policy: toolloop.PolicyDeny,
		Reason: reason + " (scheduled run " + c.chatRunID + ")",
	}, true
}
