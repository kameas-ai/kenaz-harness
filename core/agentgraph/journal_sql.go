package agentgraph

import (
	"context"
	"fmt"
)

// SQLJournalWriter persists memory-hook journal entries into the
// memory_hook_journal table created by migration sessions/0308. The
// writer is naive (one INSERT per call) which is sufficient for the
// expected fire rate (a handful per kernel node boundary) without
// adding a batching layer.
//
// IMPORTANT: this writer assumes migration 0308 has been applied. The
// constructor does not validate the schema; the chassis owns
// migration ordering.
type SQLJournalWriter struct {
	db SQLDB
}

// NewSQLJournalWriter wraps a SQLDB into a JournalWriter. Returns
// nil when db is nil so the chassis can wire defensively.
func NewSQLJournalWriter(db SQLDB) *SQLJournalWriter {
	if db == nil {
		return nil
	}
	return &SQLJournalWriter{db: db}
}

// WriteJournalEntry implements JournalWriter. The INSERT is fire-and-
// (caller-)forget; a non-nil error tells the chassis-side code to log
// at warn but does not surface to the kernel.
func (w *SQLJournalWriter) WriteJournalEntry(ctx context.Context, e JournalEntry) error {
	if w == nil || w.db == nil {
		return nil
	}
	if e.ID == "" {
		return fmt.Errorf("agentgraph: journal entry id required")
	}
	written := 0
	if e.Written {
		written = 1
	}
	deduped := 0
	if e.Deduped {
		deduped = 1
	}
	skipped := 0
	if e.Skipped {
		skipped = 1
	}
	tsNS := e.Timestamp.UnixNano()
	_, err := w.db.ExecContext(ctx,
		`INSERT INTO memory_hook_journal
		    (id, run_id, session_id, node_id, boundary, scope, scope_id,
		     chunk_id, written, deduped, skipped, skip_reason,
		     content_hash, ts_ns)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.RunID, e.SessionID, e.NodeID, string(e.Boundary), e.Scope,
		e.ScopeID, e.ChunkID, written, deduped, skipped, e.SkipReason,
		e.ContentHash, tsNS)
	if err != nil {
		return fmt.Errorf("agentgraph: journal insert: %w", err)
	}
	return nil
}

// Compile-time witness.
var _ JournalWriter = (*SQLJournalWriter)(nil)
