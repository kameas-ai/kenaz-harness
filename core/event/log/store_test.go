package log

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"

	_ "modernc.org/sqlite"
)

func mkRow(id, sid, kind string, payload []byte, prev [32]byte) Row {
	var ph [32]byte
	for i := range payload {
		ph[i%32] ^= payload[i]
	}
	return Row{
		EventID:          id,
		SessionID:        sid,
		EmitterID:        "session/",
		Kind:             kind,
		EmittedAt:        time.UnixMilli(1700000000000),
		Payload:          payload,
		PayloadHash:      ph,
		PrevHash:         prev,
		RedactionSummary: `{"no_op":true}`,
	}
}

func TestMemoryBackend_AppendAndGet(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	row := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "01HFXY8B5VJ6T6T7AXJF9JT9F0", "test.k", []byte(`{}`), [32]byte{})
	if err := b.AppendRow(ctx, row, [32]byte{}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := b.GetRow(ctx, row.EventID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.EventID != row.EventID {
		t.Fatalf("event id mismatch: %s vs %s", got.EventID, row.EventID)
	}
}

func TestMemoryBackend_HeadEnforced(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	row1 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "S", "test.k", []byte(`{"x":1}`), [32]byte{})
	if err := b.AppendRow(ctx, row1, [32]byte{}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	// Wrong expectedHead.
	row2 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F2", "S", "test.k", []byte(`{"x":2}`), [32]byte{0xff})
	if err := b.AppendRow(ctx, row2, [32]byte{0xff}); !errors.Is(err, ErrChainHeadMismatch) {
		t.Fatalf("expected ErrChainHeadMismatch, got %v", err)
	}
	// Correct expectedHead.
	if err := b.AppendRow(ctx, row2, row1.PayloadHash); err != nil {
		t.Fatalf("ok append: %v", err)
	}
}

func TestMemoryBackend_BySessionOrdered(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	ids := []string{
		"01HFXY8B5VJ6T6T7AXJF9JT9F1",
		"01HFXY8B5VJ6T6T7AXJF9JT9F2",
		"01HFXY8B5VJ6T6T7AXJF9JT9F3",
	}
	prev := [32]byte{}
	for _, id := range ids {
		r := mkRow(id, "S", "test.k", []byte(id), prev)
		if err := b.AppendRow(ctx, r, prev); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
		prev = r.PayloadHash
	}
	rows, err := b.SelectBySession(ctx, "S", "", 0, false)
	if err != nil {
		t.Fatalf("BySession: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for i := 0; i < 3; i++ {
		if rows[i].EventID != ids[i] {
			t.Fatalf("order broken at %d: got %s want %s", i, rows[i].EventID, ids[i])
		}
	}
}

func TestMemoryBackend_AllSessions(t *testing.T) {
	b := NewMemoryBackend()
	ctx := context.Background()
	r1 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F1", "A", "k.a", []byte(`{}`), [32]byte{})
	r2 := mkRow("01HFXY8B5VJ6T6T7AXJF9JT9F2", "B", "k.b", []byte(`{}`), [32]byte{})
	if err := b.AppendRow(ctx, r1, [32]byte{}); err != nil {
		t.Fatalf("a: %v", err)
	}
	if err := b.AppendRow(ctx, r2, [32]byte{}); err != nil {
		t.Fatalf("b: %v", err)
	}
	all, err := b.AllSessionIDs(ctx)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 sessions, got %v", all)
	}
}

// flakyBackend wraps a Backend, returning ErrChainHeadMismatch from
// AppendRow for the first `failures` calls before delegating — a
// deterministic stand-in for the real contention
// TestPush_ConcurrentGoroutinesWithStore (core/rpc/views/audit) hit
// under -race: without AppendComputed's retry loop, up to 75% of 20
// concurrent pushes to the same "" chain were silently dropped.
type flakyBackend struct {
	Backend
	failures int
	calls    int
}

func (f *flakyBackend) AppendRow(ctx context.Context, row Row, expectedHead [32]byte) error {
	f.calls++
	if f.calls <= f.failures {
		return ErrChainHeadMismatch
	}
	return f.Backend.AppendRow(ctx, row, expectedHead)
}

func TestStore_AppendComputed_RetriesOnChainHeadMismatch(t *testing.T) {
	mem := NewMemoryBackend()
	flaky := &flakyBackend{Backend: mem, failures: 3}
	s := NewStore(flaky)
	ctx := context.Background()

	row := Row{EventID: "retry-1", Kind: "k", EmittedAt: time.UnixMilli(1), Payload: []byte("p")}
	if err := s.AppendComputed(ctx, row); err != nil {
		t.Fatalf("AppendComputed: %v", err)
	}
	if flaky.calls != 4 {
		t.Errorf("AppendRow calls = %d, want 4 (3 failures + 1 success)", flaky.calls)
	}
	got, err := mem.GetRow(ctx, "retry-1")
	if err != nil {
		t.Fatalf("GetRow: %v", err)
	}
	if string(got.Payload) != "p" {
		t.Errorf("payload = %q, want %q", got.Payload, "p")
	}
}

// TestStore_AppendComputed_ExhaustsRetries is the falsifiability check
// for the retry loop: without it (or with an unbounded loop that never
// gives up), this test would hang or silently swallow the mismatch.
// It must return the wrapped ErrChainHeadMismatch, not nil and not a
// different error.
func TestStore_AppendComputed_ExhaustsRetries(t *testing.T) {
	mem := NewMemoryBackend()
	flaky := &flakyBackend{Backend: mem, failures: appendComputedMaxAttempts + 5}
	s := NewStore(flaky)

	row := Row{EventID: "retry-2", Kind: "k", EmittedAt: time.UnixMilli(1), Payload: []byte("p")}
	err := s.AppendComputed(context.Background(), row)
	if !errors.Is(err, ErrChainHeadMismatch) {
		t.Fatalf("AppendComputed after exhausting retries: got err=%v, want wrapped ErrChainHeadMismatch", err)
	}
	if flaky.calls != appendComputedMaxAttempts {
		t.Errorf("AppendRow calls = %d, want exactly %d (the bound)", flaky.calls, appendComputedMaxAttempts)
	}
}

func TestStoreCtor(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("nil backend should panic")
		}
	}()
	NewStore(nil)
}

func TestMigrationsList(t *testing.T) {
	got := Migrations()
	if len(got) != 6 {
		t.Fatalf("expected 6 migrations, got %d", len(got))
	}
	wantIDs := []string{
		"event-log/0100-events",
		"event-log/0101-event-chain-heads",
		"event-log/0102-redaction-rules",
		"event-log/0103-retention-config",
		"event-log/0104-schema-version",
		"event-log/0105-saved-audit-queries",
	}
	wantVersions := []int{100, 101, 102, 103, 104, 105}
	for i, m := range got {
		if m.ID != wantIDs[i] {
			t.Fatalf("migration[%d].ID = %q want %q", i, m.ID, wantIDs[i])
		}
		if m.Version != wantVersions[i] {
			t.Fatalf("migration[%d].Version = %d want %d", i, m.Version, wantVersions[i])
		}
		// AC-001 fails-if: OwningMission must be "event-log" (the
		// reserved block key), not the old "core/event" string —
		// Register returns ErrUnknownOwningMission otherwise and Open
		// fails at boot (spec §1.2).
		if m.OwningMission != "event-log" {
			t.Fatalf("migration[%d].OwningMission = %q want %q", i, m.OwningMission, "event-log")
		}
		if m.UpSource == "" {
			t.Fatalf("migration[%d] %q has empty UpSource", i, m.ID)
		}
		if m.Up == nil {
			t.Fatalf("migration[%d] %q has nil Up", i, m.ID)
		}
		if m.Down == nil {
			t.Fatalf("migration[%d] %q has nil Down", i, m.ID)
		}
	}
}

// TestSplitTrailingTrigger pins the one piece of hand-rolled SQL
// parsing this unit adds: migrations/0001_events.sql's CREATE TRIGGER
// body contains an internal semicolon (the INSERT inside BEGIN...END)
// that a naive ';'-split would cut in half. splitTrailingTrigger must
// isolate the trigger as one atomic statement.
func TestSplitTrailingTrigger(t *testing.T) {
	head, trigger := splitTrailingTrigger(migration0100Source)
	if trigger == "" {
		t.Fatal("expected a non-empty trigger statement")
	}
	if !strings.Contains(trigger, "CREATE TRIGGER") {
		t.Errorf("trigger statement missing CREATE TRIGGER: %q", trigger)
	}
	if !strings.HasSuffix(strings.TrimSpace(trigger), "END;") {
		t.Errorf("trigger statement must end at END;, got %q", trigger)
	}
	if strings.Contains(head, "CREATE TRIGGER") {
		t.Errorf("head must not contain the trigger: %q", head)
	}
	// The head must still contain every non-trigger statement.
	headStmts := splitSQL(head)
	wantHeadStmts := 6 // CREATE TABLE + 4 CREATE INDEX + CREATE VIRTUAL TABLE
	if len(headStmts) != wantHeadStmts {
		t.Errorf("head statement count = %d, want %d: %v", len(headStmts), wantHeadStmts, headStmts)
	}
}

// TestMigration0100Up_ExecutesAllStatements drives migration0100Up
// against a real sqlite3 connection (not MemoryBackend — this is
// exactly the class of SQL-marshalling bug that only shows up against
// a real driver) and asserts every object the DDL declares actually
// exists: the table, all four indexes, the FTS5 virtual table, and the
// sync trigger.
func TestMigration0100Up_ExecutesAllStatements(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	wtx := &fakeWriteTx{tx: tx}
	if err := migration0100Up(ctx, wtx); err != nil {
		t.Fatalf("migration0100Up: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, q := range []string{
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='events'",
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_events_session_event'",
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_events_kind_event'",
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_events_emitter_event'",
		"SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_events_emitted_at'",
		"SELECT count(*) FROM sqlite_master WHERE type='table' AND name='events_fts'",
		"SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='events_ai'",
	} {
		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
		if n != 1 {
			t.Errorf("query %q = %d, want 1", q, n)
		}
	}

	// The trigger must actually fire: an insert into events must
	// populate events_fts via the AFTER INSERT trigger.
	if _, err := db.ExecContext(ctx, `INSERT INTO events
		(event_id, session_id, emitter_id, kind, emitted_at, payload, payload_hash, prev_hash, redaction_summary)
		VALUES ('e1','','em','k',1,'needle',x'00',x'00','')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM events_fts WHERE events_fts MATCH 'needle'").Scan(&n); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if n != 1 {
		t.Errorf("fts trigger did not fire: got %d matches, want 1", n)
	}
}

// fakeWriteTx adapts *sql.Tx to migrations.WriteTx for the
// migration0100Up test above — a minimal real-driver harness so this
// package can prove its own Up function executes cleanly without
// depending on core/storage/sqlite (which imports core/event/log's
// sibling packages and would be a heavier, circular-risk dependency for
// a single unit test).
type fakeWriteTx struct {
	tx *sql.Tx
}

func (f *fakeWriteTx) Exec(ctx context.Context, query string, args ...any) (migrations.Result, error) {
	res, err := f.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return execResult{res}, nil
}

func (f *fakeWriteTx) QueryRow(ctx context.Context, query string, args ...any) migrations.Row {
	return f.tx.QueryRowContext(ctx, query, args...)
}

func (f *fakeWriteTx) Query(ctx context.Context, query string, args ...any) (migrations.Rows, error) {
	return f.tx.QueryContext(ctx, query, args...)
}

type execResult struct {
	res sql.Result
}

func (e execResult) RowsAffected() (int64, error) { return e.res.RowsAffected() }
func (e execResult) LastInsertID() (int64, error) { return e.res.LastInsertId() }
