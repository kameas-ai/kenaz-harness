package sqlite_test

import (
	"context"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestMigration0304_PreservesArtifactRows pins the row-level contract of
// migration sessions/0304-artifacts-promote (I14 coverage, spec.md FR-2 —
// upgrade-path-coverage-01PMUG01 WP03).
//
// 0304 rebuilds `artifacts` via the same create/copy/drop/rename recipe as
// 0327/0332, but with NO scratch-table protection for artifact_versions.
// Spec §1.2 records why that has never been a live hazard: 0304 (version
// 304) ships and applies BEFORE migration 0324 creates artifact_versions —
// in every real chronological timeline this codebase's own migrations.go
// comment history documents, no install has ever had artifact_versions rows
// while 0304 was still pending. That is "an ordering accident nothing
// checks," per spec, which is why this test exists — but it is important to
// be precise about WHAT it can and cannot check.
//
// THIS TEST DOES NOT SEED artifact_versions ROWS. Doing so via this
// package's usual "rewind one ledger row, reopen" technique would test an
// IMPOSSIBLE scenario: with only migration 304's ledger row removed (and
// 324's left alone, since 324 already ran for real when the fixture was
// first built), artifact_versions and its ON DELETE CASCADE FK already
// exist in the schema by the time 304 re-runs — a shape no real install can
// reach, because 324 cannot apply before 304 in any actual deployment
// (304 < 324, and nothing in this sessions block has ever shipped out of
// version order the way the units/sessions cross-block P0 did). Seeding
// artifact_versions here would produce a test that fails for a reason that
// says nothing about 0304's real-world safety — a false positive in the
// direction of alarm, not silence, but still not what "populated-table
// test" means for this migration.
//
// What IS real, and what this test asserts: 0304's own target table,
// `artifacts`, must survive its rebuild with rows in place — the same
// create/copy/drop/rename correctness question every migration in this
// family has to answer for its own table, independent of the
// artifact_versions question above.
func TestMigration0304_PreservesArtifactRows(t *testing.T) {
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
	if _, err := raw.ExecContext(ctx,
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version = 304"); err != nil {
		t.Fatalf("rewind 304: %v", err)
	}
	if _, err := raw.ExecContext(ctx,
		`INSERT INTO artifacts (id, session_id, project_id, title, mime_type,
             content_hash, byte_size, source, source_ref_json, scope_kind, created_at)
         VALUES ('art-0304', NULL, NULL, 'pre-304 artifact', 'text/plain', 'h1', 11, 'user_pin', '{}', 'session', 1)`); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen (runs 304): %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	var n int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM artifacts WHERE id='art-0304'").Scan(&n); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if n != 1 {
		t.Errorf("artifacts = %d after the 0304 rebuild, want 1", n)
	}

	var title string
	if err := db.Reader().QueryRow(ctx, "SELECT title FROM artifacts WHERE id='art-0304'").Scan(&title); err != nil {
		t.Fatalf("read restored artifact: %v", err)
	}
	if title != "pre-304 artifact" {
		t.Errorf("restored artifact title = %q, want %q", title, "pre-304 artifact")
	}
}
