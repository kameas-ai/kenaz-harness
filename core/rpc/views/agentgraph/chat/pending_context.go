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
// AppendSystemContext enqueues, and the session's next LLMProviderAdapter
// Generate drains it into the system prompt as one layer. "Next LLM turn"
// is literal — the very next model call for that session, which is the
// next tool-loop iteration of the same run when the run continues, or
// the session's next StartStream when it does not. Process-lifetime
// only: a restart drops anything still queued, the same lifetime as the
// hook output itself had before.

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
}

var _ coreag.PendingContextAppender = (*pendingContextQueue)(nil)

func newPendingContextQueue() *pendingContextQueue {
	return &pendingContextQueue{m: map[string][]string{}}
}

// AppendSystemContext implements coreag.PendingContextAppender.
func (q *pendingContextQueue) AppendSystemContext(_ context.Context, sessionID, text string) error {
	if q == nil || sessionID == "" || strings.TrimSpace(text) == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := append(q.m[sessionID], text)
	if len(entries) > maxPendingContextEntries {
		logging.L().Warn("chat.pending_context.dropped_oldest",
			"session_id", sessionID, "cap", maxPendingContextEntries)
		entries = entries[len(entries)-maxPendingContextEntries:]
	}
	q.m[sessionID] = entries
	return nil
}

// drain returns the session's queued context as one prompt layer (empty
// when nothing is queued) and clears the queue.
func (q *pendingContextQueue) drain(sessionID string) string {
	if q == nil || sessionID == "" {
		return ""
	}
	q.mu.Lock()
	entries := q.m[sessionID]
	delete(q.m, sessionID)
	q.mu.Unlock()
	if len(entries) == 0 {
		return ""
	}
	return pendingContextHeading + "\n\n" + strings.Join(entries, "\n\n")
}
