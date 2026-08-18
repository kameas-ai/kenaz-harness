package sqlite_test

// Shared rewind/inspection helpers for the upgrade-path test suite
// (upgrade-path-coverage-01PMUG01, spec.md plan.md: "A shared rewind
// helper for WP03, so two agents do not invent two.").
//
// These were previously defined inline in repair_upgrade_test.go and
// duplicated (with drift risk) in artifacts_rebuild_test.go. Extracted
// here, in their own file, DELIBERATELY: repair_upgrade_test.go is one
// of the two tests the mission's falsifiability run (spec §6.1) deletes
// to prove TestUpgradePath (upgrade_path_test.go) catches the v0.63.0
// bug independently. If these helpers had stayed inside
// repair_upgrade_test.go, deleting that file to run the falsifiability
// check would ALSO delete openRaw/columnExists out from under every
// other test in this package that needs them (artifacts_rebuild_test.go,
// open_refuses_partial_schema_test.go, and now upgrade_path_test.go's
// own sibling helpers) — a build failure that looks like "the mutation
// broke everything" when the real signal is buried under an unrelated
// compile error. Keeping the shared surface in its own file means
// deleting repair_upgrade_test.go removes exactly one test and nothing
// else.

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openRaw opens the data.db written by Open directly, so a test can
// rewind the schema behind the harness's back.
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

// rewindLateSessionsSchema deletes the ledger rows and drops the
// columns migrations 332-335 add, on a database already fully migrated
// through HEAD — reproducing the exact shape a real upgraded install
// was in the instant before those four migrations landed (the same
// recipe used to build the committed v0.63.0 genesis snapshot; see
// core/storage/sqlite/testdata/upgrade/v0.63.0/PROVENANCE.md). Shared
// by any test that needs a pre-332 database without going through the
// snapshot chain (e.g. a test that wants to seed rows into the schema
// as it existed at a specific migration boundary).
func rewindLateSessionsSchema(t *testing.T, ctx context.Context, raw *sql.DB) {
	t.Helper()
	stmts := []string{
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332",
		"ALTER TABLE session_messages DROP COLUMN kind",
		"ALTER TABLE session_messages DROP COLUMN move_index",
		"ALTER TABLE session_messages DROP COLUMN turn_span_id",
		"ALTER TABLE session_messages DROP COLUMN model_tool_args",
		"ALTER TABLE sessions DROP COLUMN move_history_mode",
	}
	for _, stmt := range stmts {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
}
