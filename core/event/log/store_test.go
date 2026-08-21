// AC-PI-2 (audit-that-tells-the-truth-01PMZA10 WP-PI): this file drives
// NewMemoryBackend() deliberately — it tests the in-memory REFERENCE
// implementation itself (the thing WP03's differential test compares
// SQLBackend against), not a persistence claim. sqlbackend_test.go is
// where the real-sqlite assertions live.
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
	if len(got) != 7 {
		t.Fatalf("expected 7 migrations, got %d", len(got))
	}
	wantIDs := []string{
		"event-log/0100-events",
		"event-log/0101-event-chain-heads",
		"event-log/0102-redaction-rules",
		"event-log/0103-retention-config",
		"event-log/0104-schema-version",
		"event-log/0105-saved-audit-queries",
		"event-log/0106-events-fts-sync",
	}
	wantVersions := []int{100, 101, 102, 103, 104, 105, 106}
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

// TestSplitTrailingTriggers pins the parser migration0106Up depends on:
// migrations/0007_events_fts_sync.up.sql is one plain UPDATE statement
// followed by TWO CREATE TRIGGER ... END; blocks. splitTrailingTrigger
// (singular) only isolates one trailing trigger and silently drops
// anything after it — this must isolate both.
func TestSplitTrailingTriggers(t *testing.T) {
	head, triggers := splitTrailingTriggers(migration0106UpSource)
	if len(head) != 1 {
		t.Fatalf("head statement count = %d, want 1 (the UPDATE): %v", len(head), head)
	}
	if !strings.Contains(strings.ToUpper(head[0]), "UPDATE RETENTION_CONFIG") {
		t.Errorf("head[0] does not look like the retention_config UPDATE: %q", head[0])
	}
	if len(triggers) != 2 {
		t.Fatalf("trigger count = %d, want 2 (events_fts_au, events_fts_ad): %v", len(triggers), triggers)
	}
	if !strings.Contains(triggers[0], "events_fts_au") {
		t.Errorf("triggers[0] = %q, want events_fts_au first (source order)", triggers[0])
	}
	if !strings.Contains(triggers[1], "events_fts_ad") {
		t.Errorf("triggers[1] = %q, want events_fts_ad second", triggers[1])
	}
	for i, trig := range triggers {
		if !strings.HasSuffix(strings.TrimSpace(trig), "END;") {
			t.Errorf("triggers[%d] must end at END;, got %q", i, trig)
		}
	}
}

// TestMigration0106Up_FixesTheFTSDeleteLeak is the direct executed
// proof for spec §1.6 / [RAN 2026-08-19]: before this migration, a
// DELETE from events left the row's term matchable in events_fts AND
// made a subsequent SearchFTS query for it error with "fts5: missing
// row N from content table". Drives migration0100Up (creates events +
// events_fts + the INSERT trigger) then migration0106Up (adds the
// DELETE/UPDATE triggers this test exists to prove) against a real
// sqlite3 connection, inserts a row, deletes it, and asserts BOTH
// halves of the fix: the term is gone from the FTS index (not just
// absent from a row-count query) AND a subsequent MATCH query succeeds
// without the "missing row from content table" error.
func TestMigration0106Up_FixesTheFTSDeleteLeak(t *testing.T) {
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
	// migration0106Up also runs the retention_config UPDATE (the other
	// half of the same migration) — real ordering (103 before 106)
	// guarantees the table exists; replicate that minimally here.
	if _, err := tx.ExecContext(ctx, `CREATE TABLE retention_config (
		version INTEGER PRIMARY KEY, policy TEXT NOT NULL, effective_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create retention_config: %v", err)
	}
	if err := migration0106Up(ctx, wtx); err != nil {
		t.Fatalf("migration0106Up: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Both new triggers must exist.
	for _, name := range []string{"events_fts_au", "events_fts_ad"} {
		var n int
		if err := db.QueryRowContext(ctx,
			"SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?", name).Scan(&n); err != nil {
			t.Fatalf("query trigger %s: %v", name, err)
		}
		if n != 1 {
			t.Errorf("trigger %s not found after migration0106Up", name)
		}
	}

	if _, err := db.ExecContext(ctx, `INSERT INTO events
		(event_id, session_id, emitter_id, kind, emitted_at, payload, payload_hash, prev_hash, redaction_summary)
		VALUES ('e1','s1','em1','k1',1,'ZORKMIDPAYLOAD',x'00',x'00','none')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var before int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM events_fts WHERE events_fts MATCH 'ZORKMIDPAYLOAD'").Scan(&before); err != nil {
		t.Fatalf("pre-delete fts query: %v", err)
	}
	if before != 1 {
		t.Fatalf("pre-delete match count = %d, want 1", before)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM events WHERE event_id='e1'`); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// The defect this migration exists to fix (spec §1.6, [RAN]): a
	// query that ignores the error and only checks the result set
	// passes on the broken tree, because the row-count would read 0
	// from an ERRORED query with a zero-value Scan destination. This
	// assertion must see the error explicitly.
	var after int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM events_fts WHERE events_fts MATCH 'ZORKMIDPAYLOAD'").Scan(&after)
	if err != nil {
		t.Fatalf("post-delete fts query errored (the exact pre-fix defect — "+
			"'fts5: missing row N from content table'): %v", err)
	}
	if after != 0 {
		t.Errorf("post-delete match count = %d, want 0 (term must be gone from the index, not just the row)", after)
	}

	// Availability half: a query for an unrelated, never-indexed term
	// must also succeed cleanly (sanity check that the table itself is
	// not corrupted).
	var unrelated int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM events_fts WHERE events_fts MATCH 'NEVERINDEXEDTERM'").Scan(&unrelated); err != nil {
		t.Fatalf("unrelated-term fts query: %v", err)
	}
	if unrelated != 0 {
		t.Errorf("unrelated-term match count = %d, want 0", unrelated)
	}
}

// TestMigration0106Up_FixesRetentionConfigSeed proves the second half
// of migration 106: event-log/0103-retention-config's shipped seed
// ('{"kind":"keep_all"}', not a valid RetentionStrategy value) is
// corrected to '{"kind":"keep_forever"}' by the UPDATE statement, and —
// separately — that the fix is scoped to the known-bad literal: a row
// already holding a different value is left untouched.
func TestMigration0106Up_FixesRetentionConfigSeed(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `CREATE TABLE retention_config (
		version INTEGER PRIMARY KEY, policy TEXT NOT NULL, effective_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create retention_config: %v", err)
	}
	// Row 1: the exact shipped-bad seed (event-log/0103's real
	// behaviour). Row 2: a different version already holding a real
	// strategy, standing in for an install where a later write already
	// happened — must survive untouched.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO retention_config (version, policy, effective_at) VALUES
			(1, '{"kind":"keep_all"}', 0),
			(2, '{"kind":"delete_after_window","window_days":30}', 1000)`); err != nil {
		t.Fatalf("seed retention_config: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	wtx := &fakeWriteTx{tx: tx}
	// migration0106Up also creates the events_fts_au/_ad triggers,
	// which require events/events_fts to already exist — real migration
	// ordering (100 before 106) guarantees that; replicate it here
	// rather than narrowing the schema this test drives.
	if err := migration0100Up(ctx, wtx); err != nil {
		t.Fatalf("migration0100Up: %v", err)
	}
	if err := migration0106Up(ctx, wtx); err != nil {
		t.Fatalf("migration0106Up: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var policy1, policy2 string
	if err := db.QueryRowContext(ctx, "SELECT policy FROM retention_config WHERE version=1").Scan(&policy1); err != nil {
		t.Fatalf("read version 1: %v", err)
	}
	if policy1 != `{"kind":"keep_forever"}` {
		t.Errorf("version 1 policy = %q, want %q", policy1, `{"kind":"keep_forever"}`)
	}
	if err := db.QueryRowContext(ctx, "SELECT policy FROM retention_config WHERE version=2").Scan(&policy2); err != nil {
		t.Fatalf("read version 2: %v", err)
	}
	if policy2 != `{"kind":"delete_after_window","window_days":30}` {
		t.Errorf("version 2 policy was touched: got %q, want it left alone", policy2)
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
