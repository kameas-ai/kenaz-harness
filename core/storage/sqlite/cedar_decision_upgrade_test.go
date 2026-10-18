package sqlite_test

// finding-58-cedar-decision-persistence: the Cedar engine's
// audit-decision log gained a durable backing table (migration
// cedar-policy/1300-policy-decisions, core/policy/cedar/migrations.go).
//
// CLAUDE.md blind spot #3: every test starting from an EMPTY database
// cannot see migration-selection defects — the migration high-water
// mark starts at 0 and everything applies in one ascending pass, which
// is exactly the condition under which the v0.63.0 P0 was invisible.
// This test boots a database a PREVIOUS RELEASE actually produced
// (testdata/upgrade/v0.77.1/dump.sql — the newest committed snapshot at
// the time this migration was written) through the production Open
// path, so migration 1300 is proven to apply over a database with
// dozens of prior migrations already in its ledger, not just a fresh
// one. It then proves the resulting table is not just PRESENT but
// USABLE: install a decision, close, reopen from the SAME upgraded
// file, read it back — mirroring
// TestSQLiteAnchorStore_AC004_AppliesOverPreviousReleaseSnapshot in
// core/trust/anchor_sqlite_test.go.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestCedarDecisionStore_AppliesOverPreviousReleaseSnapshot(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("testdata", "upgrade", "v0.77.1", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.77.1 snapshot not available at %s: %v", dumpPath, err)
	}

	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+rawPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.77.1 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialize: %v", err)
	}

	// ---- Open through the production path: applies migration 1300
	// (and every other migration newer than the snapshot) against the
	// upgraded database. ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.77.1 snapshot failed: %v", err)
	}

	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := db.(sqlHandle)
	if !ok {
		t.Fatal("storage.DB does not expose SQL() *sql.DB")
	}
	rawDB := h.SQL()

	store, err := cedar.NewSQLDecisionStore(rawDB)
	if err != nil {
		t.Fatalf("NewSQLDecisionStore over upgraded db: %v", err)
	}
	store.Append(cedar.Decision{
		Outcome:  cedar.Allow,
		Action:   "tool_exec",
		Resource: "kenaz__bash",
	})
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// ---- Reopen from the SAME upgraded file and read the decision
	// back — proves the table is usable, not merely present. ----
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	h2, ok := db2.(sqlHandle)
	if !ok {
		t.Fatal("reopened storage.DB does not expose SQL() *sql.DB")
	}
	store2, err := cedar.NewSQLDecisionStore(h2.SQL())
	if err != nil {
		t.Fatalf("NewSQLDecisionStore on reopen: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })

	recent := store2.Recent(10)
	found := false
	for _, d := range recent {
		if d.Action == "tool_exec" && d.Resource == "kenaz__bash" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("decision written before upgrade-path reopen not found: got %+v", recent)
	}
}
