package sqlite_test

import (
	"context"
	"strings"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestMigration0332_PreservesArtifactVersionRows pins the row-level contract
// of the artifacts rebuild in migration sessions/0332-artifacts-global-scope
// (I14's populated-table-coverage requirement, spec.md FR-2 —
// upgrade-path-coverage-01PMUG01 WP03).
//
// THE HAZARD. 0332 changes the scope_kind CHECK, which SQLite cannot do with
// ALTER TABLE, so it uses the create/copy/drop/rename recipe. artifact_versions
// (migration 0324) declares
//
//	artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE
//
// and `DROP TABLE artifacts` performs an implicit `DELETE FROM artifacts`
// whenever foreign_keys is ON — which it always is; the production DSN in
// sqlite.go sets `_pragma=foreign_keys(1)`. That implicit delete fires the
// CASCADE and empties artifact_versions. Silently: integrity_check stays
// clean, foreign_key_check stays clean, the rows are simply gone.
//
// It could not bite before v0.63.1 because 0332 had never run on a populated
// database — the max(applied) selection bug hid it from the runner on every
// upgraded install, and on a fresh install both tables are empty when it
// executes. Repairing the selection is what aims this migration at real rows
// for the first time, which is why this test lives in the same change.
//
// The test drives the PRODUCTION Open path against a database rewound to the
// pre-0332 shape with artifact rows in place — not a hand-built fixture and
// not core/session's migFakeDB, which has no row store and therefore cannot
// see a cascade at all.
func TestMigration0332_PreservesArtifactVersionRows(t *testing.T) {
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
	// Rewind to the pre-0332 artifacts table (the narrower scope_kind CHECK)
	// and drop the ledger rows for 0332+, so the reopen re-runs the rebuild.
	rewind := []string{
		"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332",
		"ALTER TABLE session_messages DROP COLUMN kind",
		"ALTER TABLE session_messages DROP COLUMN move_index",
		"ALTER TABLE session_messages DROP COLUMN turn_span_id",
		"ALTER TABLE session_messages DROP COLUMN model_tool_args",
		"ALTER TABLE sessions DROP COLUMN move_history_mode",
		`INSERT INTO artifacts (id, session_id, project_id, title, mime_type,
             content_hash, byte_size, source, source_ref_json, scope_kind, created_at)
         VALUES ('art-keep', NULL, NULL, 'kept', 'text/plain', 'h1', 11, 'user_pin', '{}', 'session', 1)`,
		`INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size,
             mime_type, summary, path, created_at)
         VALUES ('art-keep', 1, 'h1', 11, 'text/plain', 'v1', '/p/1', 1)`,
		`INSERT INTO artifact_versions (artifact_id, version, content_hash, byte_size,
             mime_type, summary, path, created_at)
         VALUES ('art-keep', 2, 'h2', 12, 'text/plain', 'v2', '/p/2', 2)`,
	}
	for _, stmt := range rewind {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen (runs 0332): %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	var artifacts, versions int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM artifacts").Scan(&artifacts); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM artifact_versions").Scan(&versions); err != nil {
		t.Fatalf("count artifact_versions: %v", err)
	}
	if artifacts != 1 {
		t.Errorf("artifacts = %d after the 0332 rebuild, want 1", artifacts)
	}
	if versions != 2 {
		t.Errorf("artifact_versions = %d after the 0332 rebuild, want 2 — "+
			"DROP TABLE artifacts cascade-deleted the child rows", versions)
	}

	// Content, not just cardinality: the restored rows must be the originals.
	var hash, summary, path string
	var byteSize, createdAt int64
	if err := db.Reader().QueryRow(ctx,
		`SELECT content_hash, summary, path, byte_size, created_at
         FROM artifact_versions WHERE artifact_id='art-keep' AND version=2`).
		Scan(&hash, &summary, &path, &byteSize, &createdAt); err != nil {
		t.Fatalf("read restored version row: %v", err)
	}
	if hash != "h2" || summary != "v2" || path != "/p/2" || byteSize != 12 || createdAt != 2 {
		t.Errorf("restored artifact_versions row = (%s,%s,%s,%d,%d), want (h2,v2,/p/2,12,2)",
			hash, summary, path, byteSize, createdAt)
	}

	// The scratch table must not survive the migration.
	var scratch int
	if err := db.Reader().QueryRow(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='artifact_versions_0332_backup'").
		Scan(&scratch); err != nil {
		t.Fatalf("scratch probe: %v", err)
	}
	if scratch != 0 {
		t.Error("artifact_versions_0332_backup leaked past the migration")
	}

	// And the point of 0332 in the first place: the widened CHECK is in place.
	var check string
	if err := db.Reader().QueryRow(ctx,
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='artifacts'").Scan(&check); err != nil {
		t.Fatalf("read artifacts ddl: %v", err)
	}
	if !strings.Contains(check, "'global'") {
		t.Errorf("artifacts CHECK does not carry 'global' after 0332:\n%s", check)
	}
}
