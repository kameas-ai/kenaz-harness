package sqlite_test

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestOpen_RepairsDatabaseMissingLateSessionsMigrations reproduces the
// v0.63.0 P0 end-to-end against a real data.db and proves the repair.
//
// THE SHIPPED FAILURE. The migration version space is shared across
// missions. units/1100..1103 landed in June 2026; sessions/0332..0335 were
// written afterwards. Pending() selected on `Version > max(applied)`, and
// max(applied) is global — so on any database carrying the units rows,
// every sessions migration numbered 332+ was permanently invisible to the
// runner. sessions.move_history_mode (migration 0334) never appeared, and
// the session INSERT in core/session/store.go died with
// "no such column: move_history_mode" the moment the user clicked
// Start session. Fresh installs were fine (max starts at 0), which is why
// it shipped.
//
// This test builds that exact database — a real, fully-migrated data.db
// rewound so the late sessions migrations are unapplied while the
// high-numbered units rows stay in the ledger — reopens it through the
// production Open path, and requires the four migrations to land and a
// session INSERT to succeed.
//
// Verified equivalently against a copy of the live dev database
// (~/.kenaz/harness/dev/data.db) during the fix.
func TestOpen_RepairsDatabaseMissingLateSessionsMigrations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()

	// 1. A healthy, fully-migrated database.
	first, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 2. Rewind it to the pre-0332 shape: drop the ledger rows for the late
	//    sessions migrations and the columns 0333/0334 added. The units
	//    rows (1100-1103) stay — they are what made max(applied) 1103.
	raw := openRaw(t, dir)
	rewind := []string{
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332",
		"ALTER TABLE session_messages DROP COLUMN kind",
		"ALTER TABLE session_messages DROP COLUMN move_index",
		"ALTER TABLE session_messages DROP COLUMN turn_span_id",
		"ALTER TABLE session_messages DROP COLUMN model_tool_args",
		"ALTER TABLE sessions DROP COLUMN move_history_mode",
	}
	for _, stmt := range rewind {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	// Sanity: the rewound database really is broken, and really does carry a
	// units row numbered far above the missing sessions migrations.
	if columnExists(t, raw, "sessions", "move_history_mode") {
		t.Fatal("rewind did not remove sessions.move_history_mode")
	}
	var maxApplied int
	if err := raw.QueryRowContext(ctx,
		"SELECT MAX(version) FROM harness_migrations WHERE action='applied'").Scan(&maxApplied); err != nil {
		t.Fatalf("max(version): %v", err)
	}
	if maxApplied < 1100 {
		t.Fatalf("max applied version = %d, want >= 1100 (the units block is what triggers the bug)", maxApplied)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// 3. Boot the harness against the broken database.
	repaired, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("repair Open: %v", err)
	}
	t.Cleanup(func() { _ = repaired.Close(context.Background()) })

	// 4. The four missing migrations must be applied.
	rows, err := repaired.Reader().Query(ctx,
		"SELECT version FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332 AND action='applied' ORDER BY version")
	if err != nil {
		t.Fatalf("ledger query: %v", err)
	}
	var got []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	rows.Close()
	want := []int{332, 333, 334, 335}
	if len(got) != len(want) {
		t.Fatalf("re-applied sessions migrations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("re-applied sessions migrations = %v, want %v", got, want)
		}
	}

	// 5. Nothing is left pending.
	pending, err := repaired.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() after repair = %d entries, want 0", len(pending))
	}

	// 6. The column exists and — the acceptance criterion that matters — a
	//    real session INSERT through the production store succeeds.
	var n int
	if err := repaired.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='move_history_mode'").Scan(&n); err != nil {
		t.Fatalf("pragma_table_info: %v", err)
	}
	if n != 1 {
		t.Fatal("sessions.move_history_mode still missing after repair")
	}

	store := session.NewSQLStore(session.NewStorageDB(repaired))
	now := time.Now().UTC()
	if err := store.Create(ctx, session.Record{
		ID: "repair-1", Name: "after repair", CreatedAt: now, UpdatedAt: now,
		LastActiveAt: now, Position: 0, ContextKind: session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("session INSERT after repair: %v", err)
	}
}

// openRaw opens the data.db written by Open directly, so a test can rewind
// the schema behind the harness's back.
func openRaw(t *testing.T, dir string) *sql.DB {
	t.Helper()
	dsn := "file:" + url.PathEscape(filepath.Join(dir, "data.db")) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func columnExists(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&n); err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	return n > 0
}
