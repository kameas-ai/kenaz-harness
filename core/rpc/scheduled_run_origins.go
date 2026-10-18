package rpc

import (
	"sync"

	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// ScheduledRunOriginRegistry is a small session-keyed side channel that
// lets a filesystem-permission denial persisted mid-run (model-scheduled-
// jobs-01PMSJ01 WP06) attribute itself to the scheduled_chat_runs row
// that is executing — mirroring the existing SubagentBudgetRegistry
// pattern (core/rpc/views/agentgraph/chat.SubagentBudgetRegistry), which
// solves the identical "give a session-scoped fact to code that only
// sees a session id" problem for a different mission.
//
// WHY THIS EXISTS: corefs.PromptSurface (the fs gate's Prompt call) and
// runposture (the unattended-run marker) both carry no scheduled-run id,
// only a session id (via core/toolloop.SessionIDFromContext) and an
// unattended bool. Threading the chat-run id through
// core/rpc/views/agentgraph/chat.ChatRunner's own ctx re-derivation
// chain (chat_runner.go's context.WithCancel(context.Background()) —
// the same re-derivation runposture.Unattended already has to survive)
// would touch a call chain several missions share; a session-keyed
// registry set once at dispatch time and read by origin lookup is the
// same "isolated, no shared blast radius" choice §5.3 made for the cron
// engine itself.
//
// Safe for concurrent use.
type ScheduledRunOriginRegistry struct {
	mu sync.RWMutex
	m  map[string]string // sessionID -> scheduled_chat_runs.id
}

// NewScheduledRunOriginRegistry returns an empty registry.
func NewScheduledRunOriginRegistry() *ScheduledRunOriginRegistry {
	return &ScheduledRunOriginRegistry{m: make(map[string]string)}
}

// Set records that sessionID belongs to the scheduled run chatRunID.
// Called by LiveChatRunDispatcher immediately after creating the
// headless session, before StartStream.
func (r *ScheduledRunOriginRegistry) Set(sessionID, chatRunID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sessionID] = chatRunID
}

// Get returns the scheduled run id sessionID belongs to, and whether one
// was found.
func (r *ScheduledRunOriginRegistry) Get(sessionID string) (string, bool) {
	if r == nil || sessionID == "" {
		return "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.m[sessionID]
	return id, ok
}

// Clear removes sessionID's entry. Called by LiveChatRunDispatcher when
// DispatchChatRun returns, whatever the outcome — the session's run is
// over either way, and an unbounded registry would otherwise leak one
// entry per scheduled run for the lifetime of the process.
func (r *ScheduledRunOriginRegistry) Clear(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, sessionID)
}

// Resolve adapts the registry onto corefs.OriginResolver's signature —
// the exact func type registerFSBuiltinTools wires into
// corefs.RecordingPrompter.Resolve.
func (r *ScheduledRunOriginRegistry) Resolve(sessionID string) (origin, originID string) {
	if id, ok := r.Get(sessionID); ok {
		return corefs.OriginScheduledChatRun, id
	}
	return "", ""
}
