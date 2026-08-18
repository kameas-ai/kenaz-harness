package sqlite_test

import (
	"context"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestMigration0327_PreservesArtifactVersionRows pins the row-level
// contract of migration sessions/0327-source-model-output
// (upgrade-path-coverage-01PMUG01 WP03, spec.md §8 / §1.3).
//
// THE HAZARD. 0327 carries the identical DROP TABLE artifacts +
// ALTER TABLE artifacts_new RENAME TO artifacts recipe as 0332 — but
// with NO scratch-table protection for artifact_versions. 0327 sits at
// version 327, ABOVE 324 (the migration that creates artifact_versions
// with `artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE
// CASCADE`). The production DSN always sets `_pragma=foreign_keys(1)`,
// so `DROP TABLE artifacts` performs an implicit `DELETE FROM
// artifacts`, which fires the CASCADE and empties artifact_versions —
// silently: foreign_key_check and integrity_check both stay clean.
//
// UNLIKE 0332 (which was invisible to the runner on every upgraded
// install until the v0.63.1 selection fix, and therefore never ran on
// populated tables until now), 0327 shipped BEFORE the units block
// existed and ran on every install that had artifact_versions rows at
// the moment it upgraded through 327. Spec §8 records that historical
// damage as real and unrecoverable — this test is about the FORWARD
// risk: any database whose ledger stops at <=326 still has 0327
// pending, and after the v0.63.1 selection repair it WILL run again.
//
// This test drives the PRODUCTION Open path against a database rewound
// to just before 0327, with rows in artifact_versions already present —
// not a hand-built fixture, and not core/session's migFakeDB (no row
// store, cannot see a cascade at all).
func TestMigration0327_PreservesArtifactVersionRows(t *testing.T) {
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
	// Rewind ONLY migration 327's own ledger row — not everything above
	// it. Pending() selects by set membership (registry.go), so marking
	// exactly one arbitrary version unapplied is sufficient to make
	// Apply() re-run JUST that migration; deleting a wider range (e.g.
	// "version >= 327") would also re-run 328's ADD COLUMN, which is
	// not idempotent against a schema that already has that column and
	// fails with "duplicate column name" — confirmed by running this
	// exact mistake first.
	if _, err := raw.ExecContext(ctx,
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version = 327"); err != nil {
		t.Fatalf("rewind 327: %v", err)
	}
	seedRows := []string{
		`INSERT INTO artifacts (id, session_id, project_id, title, mime_type,
             content_hash, byte_size, source, source_ref_json, scope_kind, created_at)
         VALUES ('art-0327', NULL, NULL, 'pre-327 artifact', 'text/plain', 'h1', 11, 'user_pin', '{}', 'session', 1)`,
		`INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size,
             mime_type, summary, path, created_at)
         VALUES ('art-0327', 1, 'h1', 11, 'text/plain', 'v1', '/p/1', 1)`,
		`INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size,
             mime_type, summary, path, created_at)
         VALUES ('art-0327', 2, 'h2', 12, 'text/plain', 'v2', '/p/2', 2)`,
	}
	for _, stmt := range seedRows {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen (runs 0327+): %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	var artifacts, versions int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM artifacts WHERE id='art-0327'").Scan(&artifacts); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM artifact_versions WHERE artifact_id='art-0327'").Scan(&versions); err != nil {
		t.Fatalf("count artifact_versions: %v", err)
	}
	if artifacts != 1 {
		t.Errorf("artifacts = %d after the 0327 rebuild, want 1", artifacts)
	}
	if versions != 2 {
		t.Errorf("artifact_versions = %d after the 0327 rebuild, want 2 — "+
			"DROP TABLE artifacts cascade-deleted the child rows (spec §8's forward risk)", versions)
	}

	// Content, not just cardinality.
	var hash, summary, path string
	var byteSize, createdAt int64
	if err := db.Reader().QueryRow(ctx,
		`SELECT content_hash, summary, path, byte_size, created_at
         FROM artifact_versions WHERE artifact_id='art-0327' AND version=2`).
		Scan(&hash, &summary, &path, &byteSize, &createdAt); err != nil {
		t.Fatalf("read restored version row: %v (0 rows if the cascade fired)", err)
	}
	if hash != "h2" || summary != "v2" || path != "/p/2" || byteSize != 12 || createdAt != 2 {
		t.Errorf("restored artifact_versions row = (%s,%s,%s,%d,%d), want (h2,v2,/p/2,12,2)",
			hash, summary, path, byteSize, createdAt)
	}

	// foreign_key_check / integrity_check stay clean either way — spec
	// §1.3's whole point — asserted anyway as the secondary check.
	rows, err := db.Reader().Query(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	var violations int
	for rows.Next() {
		violations++
	}
	_ = rows.Close()
	if violations > 0 {
		t.Errorf("foreign_key_check reported %d violation(s)", violations)
	}

	// No scratch table leaked, once 0327 is fixed to use one.
	var scratch int
	if err := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%_0327_backup'").Scan(&scratch); err != nil {
		t.Fatalf("scratch probe: %v", err)
	}
	if scratch != 0 {
		t.Error("a 0327 scratch/backup table leaked past the migration")
	}
}
