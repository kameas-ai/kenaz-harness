package sqlite_test

// model-scheduled-jobs-01PMSJ01 WP06: the blocked_permission_requests
// table. Migration sessions/0338-blocked-permission-requests CREATEs the
// table (no ALTER on any existing table, unlike WP08's 0339 sibling).
//
// CLAUDE.md blind spot #3, and the SAME below-high-water-mark shape as
// migration 0339 (see scheduled_chat_trigger_upgrade_test.go's header for
// the full argument): this migration is numbered 338, below the
// already-applied 0340 (WP09) on any install produced by a release after
// v0.71.0. Boots testdata/upgrade/v0.78.1 (already carrying 0340) through
// the production Open path and proves 0338 still applies.
//
// Mirrors TestCedarDecisionStore_AppliesOverPreviousReleaseSnapshot
// (cedar_decision_upgrade_test.go): open, write through the upgraded
// schema, close, reopen, read back.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestMigration0338_AppliesOverPreviousReleaseSnapshotAlreadyAt0340(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("testdata", "upgrade", "v0.78.1", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.78.1 snapshot not available at %s: %v", dumpPath, err)
	}

	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.78.1 snapshot: %v", err)
	}
	// Confirm the table does NOT exist pre-Open — proves this test really
	// exercises 0338's CREATE TABLE.
	var tableCount int
	if err := raw.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='blocked_permission_requests'").
		Scan(&tableCount); err != nil {
		t.Fatalf("pre-Open table check: %v", err)
	}
	if tableCount != 0 {
		t.Fatal("v0.78.1 snapshot already has blocked_permission_requests — this test no longer proves what it claims")
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialize: %v", err)
	}

	// ---- Open through the production path: applies migration 0338
	// against the upgraded database. ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.78.1 snapshot failed: %v", err)
	}

	store := blockedrequests.NewSQLiteStore(db)
	if err := store.Create(ctx, blockedrequests.Record{
		ID:        "upgrade-probe",
		Origin:    "scheduled_chat_run",
		OriginID:  "some-chat-run-id",
		SessionID: "some-session-id",
		Family:    "fs",
		Action:    "write_filesystem",
		Resource:  "/etc/passwd",
		Reason:    "no policy permits this write",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Create on the upgraded schema: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// ---- Reopen from the SAME upgraded file and read the row back. ----
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2 := blockedrequests.NewSQLiteStore(db2)
	got, err := store2.Get(ctx, "upgrade-probe")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Status != blockedrequests.StatusPending {
		t.Errorf("Status = %q, want pending", got.Status)
	}
	if got.Origin != "scheduled_chat_run" || got.OriginID != "some-chat-run-id" {
		t.Errorf("Origin/OriginID = %q/%q, want scheduled_chat_run/some-chat-run-id", got.Origin, got.OriginID)
	}

	// ---- AC-007: no FK to scheduled_chat_runs. Deleting an (unrelated,
	// nonexistent) scheduled_chat_runs row must not cascade-delete this
	// record — proven here by asserting the schema itself carries no FK
	// declaration, which is the structural guarantee AC-007's behavioural
	// test (core/policy/blockedrequests, once the recording prompter is
	// wired end to end) depends on. ----
	rawCheck := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	defer func() { _ = rawCheck.Close() }()
	var fkCount int
	if err := rawCheck.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM pragma_foreign_key_list('blocked_permission_requests')").
		Scan(&fkCount); err != nil {
		t.Fatalf("pragma_foreign_key_list: %v", err)
	}
	if fkCount != 0 {
		t.Errorf("blocked_permission_requests has %d foreign key(s), want 0 (AC-007: deleting a job must not destroy the record)", fkCount)
	}
}
