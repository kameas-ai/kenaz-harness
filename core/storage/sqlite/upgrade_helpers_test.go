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

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

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

// referenceLedgerHashes returns version -> content_hash as HEAD's
// registry computes it, by opening a fresh, empty database through the
// production path and reading the rows Apply() wrote. Every hash is
// HashSQL(m.UpSource) (see migrations.Register).
//
// WHY THIS EXISTS (upgrade-path-coverage-01PMUG01 review follow-up).
// spec.md §4 states UpSource is a released migration's content hash and
// "must never change", and §6.3 claims the mutation "edit UpSource
// instead of the executed statements -> every snapshot fails with
// ErrLedgerHashMismatch, which is the gate doing its job".
//
// That claim was FALSE when the mission landed. migrations.VerifyLedger
// -- the function that returns ErrLedgerHashMismatch -- has no
// production caller anywhere in the tree (Open calls EnsureLedger,
// Apply and verifyFullyApplied, not VerifyLedger), so editing a shipped
// migration's SQL changed nothing observable: the mutation was
// performed during review and every snapshot still passed. Nothing in
// the repo enforced the constraint the 0327 fix was carefully written
// to respect.
//
// assertLedgerHashesUnchanged closes that hole where the constraint is
// actually decidable: a committed snapshot carries the FROZEN hashes a
// previous release wrote, so comparing them against HEAD's registered
// hashes detects any edit to an already-shipped UpSource.
func referenceLedgerHashes(t *testing.T) map[int]string {
	t.Helper()
	ctx := context.Background()
	db, err := storagesqlite.Open(newConfig(t.TempDir()))
	if err != nil {
		t.Fatalf("reference Open (fresh, empty): %v", err)
	}
	defer func() { _ = db.Close(ctx) }()

	rows, err := db.Reader().Query(ctx, "SELECT version, content_hash FROM harness_migrations WHERE action='applied'")
	if err != nil {
		t.Fatalf("read reference ledger: %v", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int]string{}
	for rows.Next() {
		var v int
		var h string
		if err := rows.Scan(&v, &h); err != nil {
			t.Fatalf("scan reference ledger: %v", err)
		}
		out[v] = h
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reference ledger rows: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("reference ledger is empty — a fresh Open applied nothing")
	}
	return out
}

// assertLedgerHashesUnchanged compares the frozen content_hash values a
// committed snapshot carries against HEAD's registered hashes, for
// every version the snapshot's ledger already has applied. A mismatch
// means a SHIPPED migration's UpSource was edited (spec §4): every
// existing user's ledger row would disagree with the new registered
// hash. The 0327 repair is the worked example of the right way to
// change a shipped migration's BEHAVIOUR without touching its SQL text.
func assertLedgerHashesUnchanged(t *testing.T, ctx context.Context, raw *sql.DB, tag string) {
	t.Helper()
	want := referenceLedgerHashes(t)

	rows, err := raw.QueryContext(ctx,
		"SELECT version, id, content_hash FROM harness_migrations WHERE action='applied' ORDER BY version")
	if err != nil {
		t.Fatalf("read snapshot ledger: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var checked int
	for rows.Next() {
		var v int
		var id, got string
		if err := rows.Scan(&v, &id, &got); err != nil {
			t.Fatalf("scan snapshot ledger: %v", err)
		}
		expect, ok := want[v]
		if !ok {
			// A version in the snapshot with no registered migration at
			// HEAD is a REMOVED migration, not an edited one. VerifyLedger
			// would call that ErrSchemaGap; it is out of this assertion's
			// scope and is not decidable as "someone edited UpSource".
			continue
		}
		checked++
		if got != expect {
			t.Errorf("snapshot %s: migration %d (%s) content_hash %s, but HEAD registers %s.\n\n"+
				"A SHIPPED migration's UpSource was edited. UpSource is the content hash "+
				"(migrations.Register / HashSQL); every install that already ran this migration "+
				"has the OLD hash in its ledger (spec.md §4). Change the executed statement list "+
				"and leave UpSource alone — see core/session/migrations_source_model_output.go "+
				"(0327) for the worked example.", tag, v, id, got, expect)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot ledger rows: %v", err)
	}
	if checked == 0 {
		t.Errorf("snapshot %s: no ledger row was hash-checked — the assertion is vacuous", tag)
	}
}
