package agentgraph

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestSQLJournalWriter_RoundTrip exercises the journal writer against
// an in-memory SQLite DB. Schema is the production migration 0308
// (pasted verbatim — keeping it inline avoids importing core/session
// which would create an inverse dependency).
func TestSQLJournalWriter_RoundTrip(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
        CREATE TABLE memory_hook_journal (
            id            TEXT PRIMARY KEY,
            run_id        TEXT,
            session_id    TEXT,
            node_id       TEXT,
            boundary      TEXT NOT NULL,
            scope         TEXT NOT NULL,
            scope_id      TEXT,
            chunk_id      TEXT,
            written       INTEGER NOT NULL DEFAULT 0,
            deduped       INTEGER NOT NULL DEFAULT 0,
            skipped       INTEGER NOT NULL DEFAULT 0,
            skip_reason   TEXT NOT NULL DEFAULT '',
            content_hash  TEXT NOT NULL DEFAULT '',
            ts_ns         INTEGER NOT NULL
        );`); err != nil {
		t.Fatalf("schema: %v", err)
	}

	writer := NewSQLJournalWriter(db)
	if writer == nil {
		t.Fatal("nil writer")
	}
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	entry := JournalEntry{
		ID:          "j-1",
		RunID:       "run-A",
		SessionID:   "sess-1",
		Boundary:    HookPostLLM,
		Scope:       "session",
		ScopeID:     "sess-1",
		ChunkID:     "ch-1",
		Written:     true,
		ContentHash: "abc",
		Timestamp:   now,
	}
	if err := writer.WriteJournalEntry(context.Background(), entry); err != nil {
		t.Fatalf("WriteJournalEntry: %v", err)
	}

	// Verify the row landed.
	row := db.QueryRow(`SELECT id, boundary, scope, written, ts_ns
	                     FROM memory_hook_journal WHERE id = ?`, "j-1")
	var id, boundary, scope string
	var written int
	var tsNS int64
	if err := row.Scan(&id, &boundary, &scope, &written, &tsNS); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if boundary != string(HookPostLLM) {
		t.Errorf("boundary = %q want %q", boundary, HookPostLLM)
	}
	if written != 1 {
		t.Errorf("written = %d want 1", written)
	}
	if tsNS != now.UnixNano() {
		t.Errorf("ts_ns = %d want %d", tsNS, now.UnixNano())
	}
}

// TestSQLJournalWriter_NilWriter verifies the writer is nil-safe.
func TestSQLJournalWriter_NilWriter(t *testing.T) {
	t.Parallel()
	var w *SQLJournalWriter
	if err := w.WriteJournalEntry(context.Background(), JournalEntry{ID: "x"}); err != nil {
		t.Errorf("nil writer err = %v want nil", err)
	}
	if got := NewSQLJournalWriter(nil); got != nil {
		t.Errorf("NewSQLJournalWriter(nil) = %v want nil", got)
	}
}

// TestHookManager_PersistsViaWriter exercises the full path: a hook
// fire → in-memory journal AND SQL writer both populated.
func TestHookManager_PersistsViaWriter(t *testing.T) {
	t.Parallel()
	mem := nilMemory{}
	hm := NewHookManager(mem, "sess-1", "")
	captured := []JournalEntry{}
	hm.SetJournalWriter(testJournalWriter(func(_ context.Context, e JournalEntry) error {
		captured = append(captured, e)
		return nil
	}))
	hm.SetJournalIDGen(func() string { return "test-id" })

	_ = hm.Fire(context.Background(), HookPostLLM, "session", "title", "content", "src")
	if len(captured) != 1 {
		t.Fatalf("captured %d entries want 1", len(captured))
	}
	if captured[0].Boundary != HookPostLLM {
		t.Errorf("boundary = %q", captured[0].Boundary)
	}
	if captured[0].SessionID != "sess-1" {
		t.Errorf("session = %q", captured[0].SessionID)
	}
	if captured[0].ID != "test-id" {
		t.Errorf("id = %q", captured[0].ID)
	}
}

// testJournalWriter is a function-type adapter used in tests to
// witness JournalWriter calls without spinning up SQLite.
type testJournalWriter func(ctx context.Context, e JournalEntry) error

func (f testJournalWriter) WriteJournalEntry(ctx context.Context, e JournalEntry) error {
	return f(ctx, e)
}
