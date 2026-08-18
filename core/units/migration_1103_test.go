package units_test

// TestMigration1103_PreservesUnitEdgeRows pins the row-level contract of
// migration units/1103-conflict-edge (I14 coverage, spec.md FR-2 —
// upgrade-path-coverage-01PMUG01 WP03).
//
// 1103 widens unit_edges.kind's CHECK constraint via the standard SQLite
// create/copy/drop/rename recipe. Its own doc comment
// (core/units/migrations.go) ASSERTS FK-safety: "unit_edges is only ever a
// child of units (no other table references it), so the DROP/RENAME is
// FK-safe without toggling the foreign_keys pragma." This test makes that
// assertion checkable rather than merely stated — the exact gap spec §1.3
// identifies in the 0327/0332 cascade class (a comment asserting an
// invariant nothing enforces).
//
// Unlike the artifacts-table migrations, unit_edges has no CASCADE-exposed
// child of its own, so the hazard here is narrower: this test exists to
// confirm the rebuild preserves row content and does not silently corrupt
// or drop edges, not to catch a cascade (there is none to catch).

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func TestMigration1103_PreservesUnitEdgeRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err0 := storagesqlite.Open(cfg)
	if err0 != nil {
		t.Fatalf("initial Open: %v", err0)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	sqlDB, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage.DB does not expose SQL()")
	}
	raw := sqlDB.SQL()

	now := time.Now().UTC()
	st := units.NewSQLStore(db)
	u1, err := st.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "sess-1103",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "edge source", Body: "a", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create u1: %v", err)
	}
	u2, err := st.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "sess-1103",
		Classification: units.ClassPersonal, LoadPolicy: units.LoadAlways,
		Title: "edge target", Body: "b", CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("create u2: %v", err)
	}

	if _, err := raw.ExecContext(ctx,
		`INSERT INTO unit_edges (id, from_id, to_id, kind, version, created_at)
         VALUES ('edge-1103', ?, ?, 'references', 1, ?)`,
		u1.ID, u2.ID, now.Unix()); err != nil {
		t.Fatalf("seed edge: %v", err)
	}

	// Rewind ONLY migration 1103's own ledger row — set-membership
	// selection (registry.go) means this alone is sufficient for Apply()
	// to re-run just that migration.
	if _, err := raw.ExecContext(ctx,
		"DELETE FROM harness_migrations WHERE owning_mission='units' AND version = 1103"); err != nil {
		t.Fatalf("rewind 1103: %v", err)
	}

	if err := db.Close(ctx); err != nil {
		t.Fatalf("close before reopen: %v", err)
	}

	reopened, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("reopen (runs 1103): %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close(context.Background()) })
	pending, err := reopened.Migrations().Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("Pending() after reopen = %d, want 0 (1103 should have re-applied)", len(pending))
	}

	var count int
	if err := reopened.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM unit_edges WHERE id='edge-1103'").Scan(&count); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if count != 1 {
		t.Fatalf("unit_edges = %d after the 1103 rebuild, want 1", count)
	}

	var fromID, toID, kind string
	if err := reopened.Reader().QueryRow(ctx,
		"SELECT from_id, to_id, kind FROM unit_edges WHERE id='edge-1103'").Scan(&fromID, &toID, &kind); err != nil {
		t.Fatalf("read restored edge: %v", err)
	}
	if fromID != u1.ID || toID != u2.ID || kind != "references" {
		t.Errorf("restored edge = (%s,%s,%s), want (%s,%s,references)", fromID, toID, kind, u1.ID, u2.ID)
	}

	// The widened CHECK is in place — 'conflicts_with' must now be
	// insertable, which is the entire point of 1103.
	rawSQL, ok := reopened.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("reopened storage.DB does not expose SQL()")
	}
	if _, err := rawSQL.SQL().ExecContext(ctx,
		`INSERT INTO unit_edges (id, from_id, to_id, kind, version, created_at)
         VALUES ('edge-1103-conflict', ?, ?, 'conflicts_with', 1, ?)`, u1.ID, u2.ID, now.Unix()); err != nil {
		t.Errorf("insert with 'conflicts_with' after 1103 rebuild: %v (the widened CHECK is not in effect)", err)
	}
}
