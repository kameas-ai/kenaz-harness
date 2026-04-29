// Package wiring assembles the concrete implementations of the four
// compaction.Engine dependency interfaces (MessageStore, LLMCaller,
// CapabilityLookup, AuditEmitter) plus the SweepStore the scheduler
// drives. The compaction engine itself stays free of every
// concrete subsystem (engine.go, rolling.go, sweep.go); this package
// is the seam where the engine meets core/session, core/llm, and the
// audit pipeline.
//
// All adapters here are thin: they translate between the engine's
// interface shapes and the production subsystems' concrete APIs. No
// algorithm lives in this file — every behavior decision is the
// engine's. (mission compaction-strategy-ui-01KQ8TDI WP08)
package wiring

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/compaction"
	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// MessageStore adapts the session.Store onto the compaction.MessageStore
// interface. The engine calls ListActiveMessages to pull the surviving
// transcript and ApplyCompaction to atomically insert the summary row +
// flip the originals. Both paths delegate to the session store; this
// adapter only translates the value shapes.
type MessageStore struct {
	store session.Store
}

// NewMessageStore wraps a session.Store. nil store returns a nil
// adapter so the chassis can branch on "compaction wired vs. not".
func NewMessageStore(store session.Store) *MessageStore {
	if store == nil {
		return nil
	}
	return &MessageStore{store: store}
}

// ListActiveMessages translates session.Message → compaction.Message.
// The engine works with a narrower shape (just id, role, content,
// sequence, tool-pair markers, created-at), so this adapter projects
// the persistence shape onto the algorithm shape.
func (a *MessageStore) ListActiveMessages(ctx context.Context, sessionID string) ([]compaction.Message, error) {
	if a == nil {
		return nil, nil
	}
	src, err := a.store.ListMessagesActive(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]compaction.Message, 0, len(src))
	for _, m := range src {
		out = append(out, compaction.Message{
			ID:              m.ID,
			Role:            string(m.Role),
			Content:         m.Content,
			Sequence:        m.Sequence,
			ToolUseID:       toolUseID(m),
			ToolResultForID: toolResultForID(m),
			CreatedAt:       m.CreatedAt,
		})
	}
	return out, nil
}

// ApplyCompaction forwards to the session store's transactional helper.
// The summary's SessionID is already the canonical id; we copy the
// engine's compaction.Message fields onto a session.Message so the
// store can persist it with the canonical content_json shape.
func (a *MessageStore) ApplyCompaction(ctx context.Context, sessionID string, summary compaction.Message,
	originalIDs []string, archivedAt time.Time) error {
	if a == nil {
		return nil
	}
	row := session.Message{
		ID:        summary.ID,
		SessionID: sessionID,
		Role:      session.Role(summary.Role),
		Content:   summary.Content,
		Sequence:  summary.Sequence,
		CreatedAt: summary.CreatedAt,
	}
	return a.store.ApplyCompaction(ctx, sessionID, row, originalIDs, archivedAt)
}

// toolUseID returns the assistant-side tool_use id carried on m, or ""
// for a non-tool-call message. The current session.Message shape stores
// every tool_use inside ToolCalls; we lift the first id so the
// compaction boundary-snap can recognize "this row opened a tool call"
// without re-parsing content_json.
func toolUseID(m session.Message) string {
	if m.Role == session.RoleAssistant && len(m.ToolCalls) > 0 {
		// The boundary-snap helper treats any non-empty ToolUseID as
		// "this row is the open half of a pair"; multi-call rows are
		// rare in chat (usually one tool_use per assistant turn) and
		// the snap helper extends the boundary to cover the row in
		// either case, so reporting the first id is sufficient.
		return m.ToolCalls[0].ID
	}
	return ""
}

// toolResultForID returns the tool_use id this tool-result row closes,
// or "" for a non-tool message.
func toolResultForID(m session.Message) string {
	if m.Role == session.RoleTool && len(m.ToolCalls) > 0 {
		return m.ToolCalls[0].ID
	}
	return ""
}

// SweepStore adapts the session.Store onto the compaction.SweepStore
// interface the soft-archive sweep (compaction.RunSweep) drives.
type SweepStore struct {
	store session.Store
}

// NewSweepStore wraps a session.Store. nil store returns a nil
// adapter; callers MUST nil-check before binding to compaction.Scheduler.
func NewSweepStore(store session.Store) *SweepStore {
	if store == nil {
		return nil
	}
	return &SweepStore{store: store}
}

// DeleteArchivedBefore forwards to session.Store.DeleteArchivedBefore.
func (a *SweepStore) DeleteArchivedBefore(ctx context.Context, cutoff time.Time, pageLimit int) (
	deleted int, oldest, newest time.Time, err error) {
	if a == nil {
		return 0, time.Time{}, time.Time{}, nil
	}
	return a.store.DeleteArchivedBefore(ctx, cutoff, pageLimit)
}
