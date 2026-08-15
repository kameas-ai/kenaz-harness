// Package wiring assembles the concrete implementations of the four
// compaction.SessionEngine dependency interfaces
// (compaction.SessionMessageStore, compaction.LLMCaller,
// compaction.CapabilityLookup, compaction.AuditEmitter) plus the
// SweepStore the sweep scheduler drives. The session-compaction engine
// itself stays free of every concrete subsystem (session_engine.go,
// session_rolling.go, session_sweep.go); this package is the seam where
// that engine meets core/session, core/llm, and the audit pipeline.
//
// WHY THIS PACKAGE MOVED. It used to sit at core/compaction/wiring,
// under a second compaction package. The harness shipped two: this one,
// which rewrites persisted session history, and the FR-041 in-memory
// strategy pipeline under core/agentgraph/compaction.
// agentgraph-total-convergence-01PMGX01 WP10a merged them, so the
// package this wires now lives at core/agentgraph/compaction and this
// one moved with it. Nothing about the adapters changed — the move is
// structural.
//
// All adapters here are thin: they translate between the engine's
// interface shapes and the production subsystems' concrete APIs. No
// algorithm lives in this file — every behavior decision is the
// engine's. (mission compaction-strategy-ui-01KQ8TDI WP08)
package wiring

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// MessageStore adapts the session.Store onto the compaction.SessionMessageStore
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

// ListActiveMessages translates session.Message → compaction.SessionMessage.
// The engine works with a narrower shape (just id, role, content,
// sequence, tool-pair markers, created-at), so this adapter projects
// the persistence shape onto the algorithm shape.
func (a *MessageStore) ListActiveMessages(ctx context.Context, sessionID string) ([]compaction.SessionMessage, error) {
	if a == nil {
		return nil, nil
	}
	src, err := a.store.ListMessagesActive(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]compaction.SessionMessage, 0, len(src))
	for _, m := range src {
		out = append(out, compaction.SessionMessage{
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
// engine's compaction.SessionMessage fields onto a session.Message so the
// store can persist it with the canonical content_json shape.
func (a *MessageStore) ApplyCompaction(ctx context.Context, sessionID string, summary compaction.SessionMessage,
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

// toolUseID returns the tool_use id this row OPENED, or "" when the row
// is not the open half of a pair.
//
// THE MOVE KIND IS THE DISCRIMINATOR, NOT THE ROLE
// (model-moves-transcript-01PMCH01 WP06).
//
// Before the moves mission a tool_use could only ever arrive as an
// assistant row carrying ToolCalls, so `m.Role == RoleAssistant` was a
// sufficient test. WP02 changed the vocabulary underneath this function
// without changing this function: a `tool_call` move persists with
// Role=RoleTool (see core/session/moves.go — "moves use RoleAssistant
// for assistant_move / final and RoleTool for tool_call / tool_result").
//
// The consequence was not a compile error and not a wrong answer at this
// call site — it was silence three layers down. Every move-borne
// tool_call reported ToolUseID="", so snapBoundaryForToolPairs built an
// EMPTY openers map, so both of its clamp cases were unreachable, so the
// span boundary was free to fall between a tool_call and its
// tool_result on both the threshold and the rolling flow. The persisted
// half-pair then survived until composeModelHistory's sweep dropped the
// surviving half, which is history loss the user never sees rather than
// the provider 400 the sweep exists to prevent. FR-005 asks that moves
// be first-class compaction inputs; a marker function that cannot see
// them is the whole feature quietly not applying.
//
// A tool_call move is EXCLUSIVELY an opener and a tool_result move is
// EXCLUSIVELY a closer — reporting one row as both (which the old
// role-only pair of helpers did for every move row, since both are
// RoleTool with ToolCalls) would make the snap's openers map claim a
// call answers itself.
//
// Multi-call rows report the first id: the snap extends the boundary to
// cover the whole row either way, so the first id is sufficient.
func toolUseID(m session.Message) string {
	if len(m.ToolCalls) == 0 {
		return ""
	}
	switch m.MoveKind() {
	case session.MoveKindToolCall:
		return m.ToolCalls[0].ID
	case session.MoveKindToolResult:
		return ""
	}
	// Classic (pre-mission) row: the assistant turn carries the tool_use.
	if m.Role == session.RoleAssistant {
		return m.ToolCalls[0].ID
	}
	return ""
}

// toolResultForID returns the tool_use id this row CLOSED, or "" when
// the row is not the answering half of a pair. Mirror of toolUseID; read
// its comment for why the move kind and not the role decides.
func toolResultForID(m session.Message) string {
	if len(m.ToolCalls) == 0 {
		return ""
	}
	switch m.MoveKind() {
	case session.MoveKindToolResult:
		return m.ToolCalls[0].ID
	case session.MoveKindToolCall:
		return ""
	}
	// Classic (pre-mission) row.
	if m.Role == session.RoleTool {
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
// adapter; callers MUST nil-check before binding to compaction.SweepScheduler.
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
