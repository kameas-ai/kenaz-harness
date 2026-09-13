package blockedrequests_test

// Real-sqlite tests for the blocked_permission_requests store
// (model-scheduled-jobs-01PMSJ01 WP06). CLAUDE.md blind spot #2: an
// in-memory map fixture would skip SQL encode/decode entirely and has
// hidden production defects before — every assertion here drives a real
// database through the production migration path.
//
// This file exercises NEW store logic (CRUD + status transitions), not
// migration selection or schema evolution across a previously-shipped
// schema, so booting a fresh DB through the current migration set is the
// correct fixture — the upgrade-snapshot proof for migration
// sessions/0338 itself lives in
// core/storage/sqlite/blocked_permission_requests_upgrade_test.go
// (CLAUDE.md blind spot #3).

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) blockedrequests.Store {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return blockedrequests.NewSQLiteStore(db)
}

func TestSQLiteStore_CreateAndGet(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rec := blockedrequests.Record{
		ID:        "req-1",
		Origin:    blockedrequests.StatusPending, // placeholder, overwritten below
		OriginID:  "chatrun-1",
		SessionID: "sess-1",
		Family:    "fs",
		Action:    "write_filesystem",
		Resource:  "/etc/passwd",
		Reason:    "no policy permits this write",
		CreatedAt: time.Now().UTC(),
	}
	rec.Origin = "scheduled_chat_run"
	if err := store.Create(ctx, rec); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get(ctx, "req-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != blockedrequests.StatusPending {
		t.Errorf("Status = %q, want pending (the default)", got.Status)
	}
	if got.Origin != "scheduled_chat_run" || got.OriginID != "chatrun-1" {
		t.Errorf("Origin/OriginID = %q/%q, want scheduled_chat_run/chatrun-1", got.Origin, got.OriginID)
	}
	if got.Resource != "/etc/passwd" {
		t.Errorf("Resource = %q, want /etc/passwd", got.Resource)
	}
	if got.ResolvedAt != nil {
		t.Error("ResolvedAt should be nil for a freshly-created pending record")
	}
}

func TestSQLiteStore_GetNotFound(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Get(context.Background(), "does-not-exist")
	if !errors.Is(err, blockedrequests.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSQLiteStore_ListByStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	mustCreate(t, store, "req-pending-1", now)
	mustCreate(t, store, "req-pending-2", now.Add(time.Second))
	mustCreate(t, store, "req-granted", now.Add(2*time.Second))
	if err := store.SetStatus(ctx, "req-granted", blockedrequests.StatusGranted, now.Add(3*time.Second)); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	pending, err := store.ListByStatus(ctx, blockedrequests.StatusPending)
	if err != nil {
		t.Fatalf("ListByStatus(pending): %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending count = %d, want 2", len(pending))
	}
	// Newest first.
	if pending[0].ID != "req-pending-2" {
		t.Errorf("pending[0].ID = %q, want req-pending-2 (newest first)", pending[0].ID)
	}

	granted, err := store.ListByStatus(ctx, blockedrequests.StatusGranted)
	if err != nil {
		t.Fatalf("ListByStatus(granted): %v", err)
	}
	if len(granted) != 1 || granted[0].ID != "req-granted" {
		t.Fatalf("granted = %+v, want exactly [req-granted]", granted)
	}

	all, err := store.ListByStatus(ctx, "")
	if err != nil {
		t.Fatalf("ListByStatus(\"\"): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all count = %d, want 3", len(all))
	}
}

func TestSQLiteStore_SetStatusStampsResolvedAt(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	mustCreate(t, store, "req-x", time.Now().UTC())

	resolvedAt := time.Now().UTC().Add(time.Minute)
	if err := store.SetStatus(ctx, "req-x", blockedrequests.StatusGranted, resolvedAt); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := store.Get(ctx, "req-x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != blockedrequests.StatusGranted {
		t.Errorf("Status = %q, want granted", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Fatal("ResolvedAt was not stamped")
	}
	if got.ResolvedAt.Unix() != resolvedAt.Unix() {
		t.Errorf("ResolvedAt = %v, want %v", got.ResolvedAt, resolvedAt)
	}
}

func TestSQLiteStore_SetStatusNotFound(t *testing.T) {
	store := openTestStore(t)
	err := store.SetStatus(context.Background(), "nope", blockedrequests.StatusDismissed, time.Now())
	if !errors.Is(err, blockedrequests.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestSQLiteStore_SurvivesReopen proves persistence across a close/reopen
// cycle, not merely an in-process cache — the same shape as the mission's
// other store-level tests (scheduler.SQLiteChatStore's own tests, cedar's
// SQLDecisionStore upgrade test).
func TestSQLiteStore_SurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	store := blockedrequests.NewSQLiteStore(db)
	mustCreate(t, store, "req-persist", time.Now().UTC())
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2 := blockedrequests.NewSQLiteStore(db2)
	got, err := store2.Get(ctx, "req-persist")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.ID != "req-persist" {
		t.Errorf("ID = %q after reopen, want req-persist", got.ID)
	}
}

func mustCreate(t *testing.T, store blockedrequests.Store, id string, createdAt time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), blockedrequests.Record{
		ID:        id,
		Origin:    "interactive",
		SessionID: "sess-" + id,
		Family:    "fs",
		Action:    "write_filesystem",
		Resource:  "/tmp/" + id,
		Reason:    "test",
		CreatedAt: createdAt,
	}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}
