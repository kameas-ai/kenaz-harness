package session_test

// chat-turn-integrity-01PMZ606 WP02 — the stream_checkpoints table,
// store methods and migration 0336.
//
// Per spec.md §8 rule 1 / CLAUDE.md blind spot #2, every assertion here
// drives real sqlite (session.NewSQLStore over storagesqlite.Open), not
// session.NewMemoryStore — a memory store skips SQL encode/decode
// entirely and is exactly what hid this mission's own P0 (§1.1.2).

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// newCheckpointSQLManager opens a real sqlite database (so migration
// 0336 is exercised, not stubbed), wraps it in the session SQL store,
// and returns a manager plus a created session id.
func newCheckpointSQLManager(t *testing.T) (*session.Manager, string) {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	rec, err := mgr.Create(context.Background(), "checkpoints")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return mgr, rec.ID
}

// TestMigration0336_TableExistsAfterOpen verifies migration 0336 creates
// the stream_checkpoints table with the columns the store methods rely
// on.
func TestMigration0336_TableExistsAfterOpen(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	ctx := context.Background()

	for _, col := range []string{"session_id", "sub_id", "text", "has_tool", "updated_at"} {
		var n int
		row := db.Reader().QueryRow(ctx,
			"SELECT COUNT(*) FROM pragma_table_info('stream_checkpoints') WHERE name = ?", col)
		if err := row.Scan(&n); err != nil {
			t.Fatalf("pragma_table_info(%s): %v", col, err)
		}
		if n != 1 {
			t.Errorf("column %s missing from stream_checkpoints (count=%d)", col, n)
		}
	}
}

// TestStreamCheckpoint_UpsertRoundTrip is AC-002's store-level half:
// upserting twice for one (session, sub) leaves exactly one row with
// the latest text, and has_tool round-trips both ways. Drives sqlStore
// via the real Manager, not memStore.
func TestStreamCheckpoint_UpsertRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sessionID := newCheckpointSQLManager(t)

	if err := mgr.UpsertStreamCheckpoint(ctx, sessionID, "sub-1", "hello", false); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	got, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-1")
	if err != nil {
		t.Fatalf("GetStreamCheckpoint: %v", err)
	}
	if !ok {
		t.Fatal("expected a checkpoint after first upsert, got none")
	}
	if got.Text != "hello" || got.HasTool {
		t.Errorf("after first upsert: text=%q hasTool=%v, want text=%q hasTool=false", got.Text, got.HasTool, "hello")
	}

	// Second upsert on the SAME (session, sub) must overwrite in
	// place, not append — this is the write-amplification fix the
	// migration's whole reason for existing.
	if err := mgr.UpsertStreamCheckpoint(ctx, sessionID, "sub-1", "hello world", true); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	got, ok, err = mgr.GetStreamCheckpoint(ctx, sessionID, "sub-1")
	if err != nil {
		t.Fatalf("GetStreamCheckpoint after second upsert: %v", err)
	}
	if !ok {
		t.Fatal("expected a checkpoint after second upsert, got none")
	}
	if got.Text != "hello world" {
		t.Errorf("text = %q, want %q", got.Text, "hello world")
	}
	if !got.HasTool {
		t.Error("has_tool = false, want true (round-trip of the second upsert's argument)")
	}
	if got.SessionID != sessionID || got.SubID != "sub-1" {
		t.Errorf("SessionID/SubID = %q/%q, want %q/%q", got.SessionID, got.SubID, sessionID, "sub-1")
	}

	// The delete half of the round trip: after Delete, Get reports no
	// checkpoint. Combined with the two upserts above landing on the
	// SAME key (the composite primary key is what makes this an
	// UPSERT rather than an append), this pins "exactly one row per
	// (session, sub), overwritten in place."
	if err := mgr.DeleteStreamCheckpoint(ctx, sessionID, "sub-1"); err != nil {
		t.Fatalf("DeleteStreamCheckpoint: %v", err)
	}
	_, ok, err = mgr.GetStreamCheckpoint(ctx, sessionID, "sub-1")
	if err != nil {
		t.Fatalf("GetStreamCheckpoint after delete: %v", err)
	}
	if ok {
		t.Error("expected no checkpoint after delete, got one")
	}
}

// TestStreamCheckpoint_DistinctSubsDoNotCollide verifies the primary
// key is (session_id, sub_id), not session_id alone — two live
// subscriptions on the same session (e.g. a branch redrive racing the
// parent) get independent checkpoint rows.
func TestStreamCheckpoint_DistinctSubsDoNotCollide(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sessionID := newCheckpointSQLManager(t)

	if err := mgr.UpsertStreamCheckpoint(ctx, sessionID, "sub-a", "from a", false); err != nil {
		t.Fatalf("upsert sub-a: %v", err)
	}
	if err := mgr.UpsertStreamCheckpoint(ctx, sessionID, "sub-b", "from b", true); err != nil {
		t.Fatalf("upsert sub-b: %v", err)
	}

	a, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-a")
	if err != nil || !ok {
		t.Fatalf("GetStreamCheckpoint(sub-a): ok=%v err=%v", ok, err)
	}
	if a.Text != "from a" || a.HasTool {
		t.Errorf("sub-a = %+v, want text=%q hasTool=false", a, "from a")
	}

	b, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-b")
	if err != nil || !ok {
		t.Fatalf("GetStreamCheckpoint(sub-b): ok=%v err=%v", ok, err)
	}
	if b.Text != "from b" || !b.HasTool {
		t.Errorf("sub-b = %+v, want text=%q hasTool=true", b, "from b")
	}

	// Deleting one sub's checkpoint must not touch the other.
	if err := mgr.DeleteStreamCheckpoint(ctx, sessionID, "sub-a"); err != nil {
		t.Fatalf("delete sub-a: %v", err)
	}
	if _, ok, _ := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-a"); ok {
		t.Error("sub-a checkpoint survived its own delete")
	}
	if _, ok, _ := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-b"); !ok {
		t.Error("sub-b checkpoint was deleted by sub-a's delete — PK is not (session_id, sub_id)")
	}
}

// TestStreamCheckpoint_GetMissing_NotAnError verifies the "no
// checkpoint yet" steady state (before the first tick, and after a
// clean/error close deletes the row) is NOT surfaced as an error — the
// design note in store.go this test pins.
func TestStreamCheckpoint_GetMissing_NotAnError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sessionID := newCheckpointSQLManager(t)

	got, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, "never-flushed")
	if err != nil {
		t.Fatalf("GetStreamCheckpoint on a never-written sub: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for a never-written checkpoint, got %+v", got)
	}
}

// TestStreamCheckpoint_DeleteIdempotent verifies deleting an
// already-absent checkpoint is a no-op, not an error — both the
// clean-close and error-close call sites in driveRun call Delete
// unconditionally and must not fail when there is nothing to delete.
func TestStreamCheckpoint_DeleteIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sessionID := newCheckpointSQLManager(t)

	if err := mgr.DeleteStreamCheckpoint(ctx, sessionID, "never-existed"); err != nil {
		t.Fatalf("DeleteStreamCheckpoint on an absent row: %v", err)
	}
	// Twice, for good measure — idempotence means calling it again
	// after it is already gone is also fine.
	if err := mgr.DeleteStreamCheckpoint(ctx, sessionID, "never-existed"); err != nil {
		t.Fatalf("second DeleteStreamCheckpoint on an absent row: %v", err)
	}
}

// TestStreamCheckpoint_UpdatedAtAdvances is a light sanity check that
// UpdatedAt is stamped from the manager's clock, not left zero — a
// future boot-recovery reader (E-006, explicitly deferred) will need
// this to reason about staleness.
func TestStreamCheckpoint_UpdatedAtAdvances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr, sessionID := newCheckpointSQLManager(t)

	before := time.Now()
	if err := mgr.UpsertStreamCheckpoint(ctx, sessionID, "sub-1", "x", false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	after := time.Now()

	got, ok, err := mgr.GetStreamCheckpoint(ctx, sessionID, "sub-1")
	if err != nil || !ok {
		t.Fatalf("GetStreamCheckpoint: ok=%v err=%v", ok, err)
	}
	if got.UpdatedAt.Before(before.Add(-time.Second)) || got.UpdatedAt.After(after.Add(time.Second)) {
		t.Errorf("UpdatedAt = %v, want within [%v, %v]", got.UpdatedAt, before, after)
	}
}
