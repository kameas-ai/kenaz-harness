package sqlite_test

import (
	"context"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// TestMigration1601_BackfillsRevisionOnPopulatedLabels pins the row-level
// contract of migration laya-advisors/1601-advice-labels-revision
// (laya-advisors-01LAYA001 WP14): applied against an advice_labels table
// that ALREADY holds 1600-era rows (no revision column), it must keep
// every row and backfill revision = id, so the table-global revision
// counter stays monotonic and the next production insert lands above
// every backfilled row.
//
// Drives the PRODUCTION Open path against a database rewound to just
// before 1601 (its ledger row deleted, its schema effects undone), with
// rows seeded through the 1600 schema — not a hand-built fixture.
func TestMigration1601_BackfillsRevisionOnPopulatedLabels(t *testing.T) {
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
	for _, stmt := range []string{
		"DELETE FROM harness_migrations WHERE owning_mission='laya-advisors' AND version = 1601",
		"DROP TABLE advice_label_push_cursor",
		"DROP INDEX idx_advice_labels_kind_revision",
		"DROP INDEX idx_advice_labels_revision",
		"ALTER TABLE advice_labels DROP COLUMN revision",
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("rewind %q: %v", stmt, err)
		}
	}
	for i, hash := range []string{"h1", "h2", "h3"} {
		if _, err := raw.ExecContext(ctx, `INSERT INTO advice_labels
		    (kind, prompt_version, features_hash, features_json, model_id, rung, decision, confidence, shown, session_id, created_at)
		    VALUES ('branch_now','v1',?,'{"k":1}','heuristic/x-v1','heuristic',1,81,1,'s',?)`, hash, 1000+i); err != nil {
			t.Fatalf("seed %s: %v", hash, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen (runs 1601): %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	var n, mismatched int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM advice_labels").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("advice_labels rows = %d after 1601, want 3 (the migration must keep every row)", n)
	}
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM advice_labels WHERE revision <> id OR revision < 1").Scan(&mismatched); err != nil {
		t.Fatalf("revision check: %v", err)
	}
	if mismatched != 0 {
		t.Errorf("%d rows have a revision other than their id after backfill", mismatched)
	}
	var content string
	if err := db.Reader().QueryRow(ctx, "SELECT features_json FROM advice_labels WHERE features_hash='h2'").Scan(&content); err != nil || content != `{"k":1}` {
		t.Errorf("row content not preserved: %q, %v", content, err)
	}
	var cursorTable int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='advice_label_push_cursor'").Scan(&cursorTable); err != nil || cursorTable != 1 {
		t.Errorf("advice_label_push_cursor missing after 1601: %d, %v", cursorTable, err)
	}
}
