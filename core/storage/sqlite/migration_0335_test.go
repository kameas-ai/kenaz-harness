package sqlite_test

import (
	"context"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestMigration0335_PurgesToolRowsFromFTS pins the row-level contract of
// migration sessions/0335-search-fts-exclude-tool-rows (I14 coverage,
// spec.md FR-2 — upgrade-path-coverage-01PMUG01 WP03).
//
// 0335 drops 0312's unguarded FTS5 sync triggers, replaces them with
// role-guarded versions, and purges already-indexed role='tool' rows from
// the FTS index (session_messages rows themselves are untouched — only the
// FTS shadow index changes).
//
// docs/unwired-ledger.md already records that 0335's tool-row purge is NOT
// idempotent — see this file's second test,
// TestMigration0335_SecondApplyIsNotWellDefined, which documents that
// finding directly rather than silently working around it. Per spec §6.3
// / tasks.md WP03: report it as a separate finding, do not fold an
// idempotence fix into this coverage WP.
func TestMigration0335_PurgesToolRowsFromFTS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	first, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("initial Open: %v", err)
	}
	if err := first.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	raw := openRaw(t, dir)
	// A FRESH Open already applies 335, so the live triggers are
	// already role-guarded by the time this test seeds rows — a tool
	// row inserted now would never be indexed in the first place, which
	// would make the purge step a silent no-op and prove nothing. Revert
	// the LIVE TRIGGERS (not just the ledger row) to 0312's original
	// unguarded shape first, so the seeded tool row actually lands in
	// the FTS index the way a real pre-0335 install's did.
	revertTriggers := []string{
		"DROP TRIGGER IF EXISTS messages_fts_ai",
		"DROP TRIGGER IF EXISTS messages_fts_au",
		"DROP TRIGGER IF EXISTS messages_fts_ad",
		`CREATE TRIGGER messages_fts_ai
		    AFTER INSERT ON session_messages BEGIN
		        INSERT INTO messages_fts(rowid, content)
		            VALUES (new.rowid, new.content);
		    END`,
		`CREATE TRIGGER messages_fts_au
		    AFTER UPDATE ON session_messages BEGIN
		        INSERT INTO messages_fts(messages_fts, rowid, content)
		            VALUES ('delete', old.rowid, old.content);
		        INSERT INTO messages_fts(rowid, content)
		            VALUES (new.rowid, new.content);
		    END`,
		`CREATE TRIGGER messages_fts_ad
		    AFTER DELETE ON session_messages BEGIN
		        INSERT INTO messages_fts(messages_fts, rowid, content)
		            VALUES ('delete', old.rowid, old.content);
		    END`,
	}
	for _, stmt := range revertTriggers {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("revert trigger %q: %v", stmt, err)
		}
	}

	seed := []string{
		`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position, draft,
             scroll_position, archived_at, system_prompt, context_kind, project_id)
         VALUES ('sess-0335', 'seed', 1, 1, 1, 0, '', 0, NULL, '', 'system', NULL)`,
		`INSERT INTO session_messages (id, session_id, sequence, role, content, tool_calls, created_at)
         VALUES ('msg-user-0335', 'sess-0335', 0, 'user', 'find the kameasfindable term', NULL, 1)`,
		`INSERT INTO session_messages (id, session_id, sequence, role, content, tool_calls, created_at)
         VALUES ('msg-tool-0335', 'sess-0335', 1, 'tool', 'kameasfindable raw tool dump output', NULL, 2)`,
	}
	for _, stmt := range seed {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	// Sanity: under the reverted 0312 unguarded triggers, BOTH rows are
	// indexed.
	var preCount int
	if err := raw.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'kameasfindable'`).Scan(&preCount); err != nil {
		t.Fatalf("pre-purge FTS count: %v", err)
	}
	if preCount != 2 {
		t.Fatalf("pre-purge FTS hits for 'kameasfindable' = %d, want 2 (both rows indexed before 0335 re-runs)", preCount)
	}

	if _, err := raw.ExecContext(ctx,
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version = 335"); err != nil {
		t.Fatalf("rewind 335: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen (runs 335): %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	// The underlying rows are untouched — 335 only changes the index.
	var msgCount int
	if err := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM session_messages WHERE id IN ('msg-user-0335','msg-tool-0335')").Scan(&msgCount); err != nil {
		t.Fatalf("count session_messages: %v", err)
	}
	if msgCount != 2 {
		t.Errorf("session_messages = %d after 0335, want 2 (335 must not delete rows, only purge the FTS index)", msgCount)
	}

	var postCount int
	if err := db.Reader().QueryRow(ctx,
		`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'kameasfindable'`).Scan(&postCount); err != nil {
		t.Fatalf("post-purge FTS count: %v", err)
	}
	if postCount != 1 {
		t.Errorf("post-purge FTS hits for 'kameasfindable' = %d, want 1 (only the user row; the tool row must be purged)", postCount)
	}

	var rowid int
	err = db.Reader().QueryRow(ctx,
		`SELECT rowid FROM messages_fts WHERE messages_fts MATCH 'kameasfindable'`).Scan(&rowid)
	if err != nil {
		t.Fatalf("read remaining FTS hit: %v", err)
	}
	var role string
	if err := db.Reader().QueryRow(ctx, "SELECT role FROM session_messages WHERE rowid = ?", rowid).Scan(&role); err != nil {
		t.Fatalf("read role of remaining FTS hit: %v", err)
	}
	if role != "user" {
		t.Errorf("the surviving FTS hit belongs to role=%q, want role=user", role)
	}
}

// TestMigration0335_SecondApplyIsNotWellDefined records, as a test rather
// than only a doc comment, the non-idempotence docs/unwired-ledger.md
// already flags: migration 0335's purge step
// (sqlPurgeToolRowsFromFTS — an FTS5 'delete' command) is written to run
// exactly once, driven by the ledger's own single-application guarantee,
// not by re-derivable state. This test does NOT attempt to force a second
// application through the production Open path (Apply() will not re-run an
// already-ledgered migration, by design) — it instead documents, at the SQL
// level, why a raw re-execution of the purge statement is unsafe: FTS5's
// 'delete' command subtracts term counts, and issuing it twice for the same
// (rowid, content) pair drives a term count negative.
//
// THIS IS A SEPARATE FINDING FROM THE POPULATED-TABLE COVERAGE ABOVE, not a
// fix. Spec §6.3 / tasks.md WP03 say explicitly: report it, do not fold an
// idempotence fix into this coverage WP.
func TestMigration0335_SecondApplyIsNotWellDefined(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	raw := openRaw(t, dir)
	defer raw.Close()

	if _, err := raw.ExecContext(ctx,
		`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position, draft,
             scroll_position, archived_at, system_prompt, context_kind, project_id)
         VALUES ('sess-0335b', 'seed', 1, 1, 1, 0, '', 0, NULL, '', 'system', NULL)`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO session_messages (id, session_id, sequence, role, content, tool_calls, created_at)
         VALUES ('msg-tool-0335b', 'sess-0335b', 0, 'tool', 'already purged once', NULL, 1)`); err != nil {
		t.Fatalf("seed tool row: %v", err)
	}
	// The AFTER INSERT trigger (post-0335, since this Open already
	// applied everything through 335) is role-guarded, so the tool row
	// was never indexed in the first place — the purge already ran
	// (implicitly, via the guard) for any row inserted after 0335.
	// Issuing the SAME purge statement a second time, directly, against
	// a row it has already (in effect) excluded, is exactly the
	// unsafe-negative-count operation docs/unwired-ledger.md records.
	_, err = raw.ExecContext(ctx,
		`INSERT INTO messages_fts(messages_fts, rowid, content)
         SELECT 'delete', rowid, content FROM session_messages WHERE role = 'tool'`)
	if err == nil {
		t.Log("second purge did not error in this run — SQLite's negative-count " +
			"failure is state-dependent (spec: 'database disk image is malformed' " +
			"under specific term-count conditions), not guaranteed on every input. " +
			"Recorded as a finding regardless: the operation has no defined safe " +
			"re-run semantics, and this test exists so a future safe-guard change " +
			"has something to green.")
	} else {
		t.Logf("second purge failed as docs/unwired-ledger.md predicts: %v", err)
	}
}
