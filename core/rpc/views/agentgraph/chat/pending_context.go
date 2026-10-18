package chat

// pending_context.go — the production coreag.PendingContextAppender
// (v0.86.0 unwired sweep, 2026-10-04).
//
// Before this file, agentgraph.Env.PendingContext had ZERO production
// writers: nothing in the tree implemented PendingContextAppender, so
// every pre_tool_use / post_tool_use hook's additional_context was
// silently dropped at core/agentgraph/tool_invocation.go's
// `env.PendingContext != nil` guard — while hooks.HookOutput's doc
// promised it "is injected as a system message into the next LLM turn".
//
// The queue is per ChatRunner (one per process) and keyed by session:
// AppendSystemContext enqueues; the session's next PRIMARY turn call drains
// it into the system prompt as one layer. "Primary" means
// LLMRequest.StreamToChat — the assistant_turn model node, the call whose
// output the user reads. Auxiliary calls on the same run (the routed
// graph's router, exit-gate review verdict, escalation rungs, compaction
// summaries) never touch the queue, so they cannot consume context meant
// for the assistant (review follow-up M2, 2026-10-04). The drain happens
// on the next assistant_turn iteration of the same run when the run
// continues, or the session's next StartStream when it does not. A
// primary call that fails re-queues what it took (L3); a deleted
// session's queue is forgotten (ChatRunner.ForgetSession, L5).
// Process-lifetime only: a restart drops anything still queued.

import (
	"context"
	"strings"
	"sync"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// maxPendingContextEntries bounds one session's queue. A hook that
// returns additional_context on every tool call of a session whose
// model never runs again must not grow the process without bound; the
// oldest entry is dropped (and logged) past this cap.
const maxPendingContextEntries = 64

// pendingContextHeading introduces the drained layer so the model can
// tell hook-supplied context apart from the user's own instructions.
const pendingContextHeading = "Additional context from the user's hooks:"

// pendingContextQueue implements coreag.PendingContextAppender.
type pendingContextQueue struct {
	mu sync.Mutex
	m  map[string][]string
	// overflowing marks sessions currently dropping entries at the cap,
	// so the overflow warning logs once per burst instead of once per
	// append (cleared when the queue is taken or forgotten).
	overflowing map[string]bool
}

var _ coreag.PendingContextAppender = (*pendingContextQueue)(nil)

func newPendingContextQueue() *pendingContextQueue {
	return &pendingContextQueue{m: map[string][]string{}, overflowing: map[string]bool{}}
}

// AppendSystemContext implements coreag.PendingContextAppender.
func (q *pendingContextQueue) AppendSystemContext(_ context.Context, sessionID, text string) error {
	if q == nil || sessionID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.m[sessionID] = q.capLocked(sessionID, append(q.m[sessionID], text))
	return nil
}

// capLocked trims entries to maxPendingContextEntries, keeping the newest,
// and logs the first drop of a burst. Caller holds q.mu.
func (q *pendingContextQueue) capLocked(sessionID string, entries []string) []string {
	if len(entries) <= maxPendingContextEntries {
		return entries
	}
	if !q.overflowing[sessionID] {
		q.overflowing[sessionID] = true
		logging.L().Warn("chat.pending_context.dropping_oldest",
			"session_id", sessionID, "cap", maxPendingContextEntries)
	}
	return entries[len(entries)-maxPendingContextEntries:]
}

// take removes and returns the session's queued entries (nil when none).
func (q *pendingContextQueue) take(sessionID string) []string {
	if q == nil || sessionID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := q.m[sessionID]
	delete(q.m, sessionID)
	delete(q.overflowing, sessionID)
	return entries
}

// requeue puts entries a failed model call had taken back at the FRONT
// of the session's queue (they are older than anything appended since),
// so a provider error does not silently lose hook context.
func (q *pendingContextQueue) requeue(sessionID string, entries []string) {
	if q == nil || sessionID == "" || len(entries) == 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	merged := append(append([]string(nil), entries...), q.m[sessionID]...)
	q.m[sessionID] = q.capLocked(sessionID, merged)
}

// forget drops a session's queue — called when the session is deleted,
// so a deleted session's hook context can neither leak memory nor reach
// a later session that reuses the id.
func (q *pendingContextQueue) forget(sessionID string) {
	if q == nil || sessionID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.m, sessionID)
	delete(q.overflowing, sessionID)
}

// renderPendingContext formats taken entries as one system-prompt layer
// ("" when there are none).
func renderPendingContext(entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	return pendingContextHeading + "\n\n" + strings.Join(entries, "\n\n")
}

// drain is take + render, for callers that cannot fail afterwards.
func (q *pendingContextQueue) drain(sessionID string) string {
	return renderPendingContext(q.take(sessionID))
}

// ForgetSession drops any hook context still queued for sessionID. Wired
// to Sessions delete (core/rpc/api.go's WithDeleteHookOpt), so a deleted
// session's queue neither lingers for the process lifetime nor reaches a
// later session reusing the id. nil-safe.
func (r *ChatRunner) ForgetSession(sessionID string) {
	if r == nil {
		return
	}
	r.pendingContext.forget(sessionID)
}
