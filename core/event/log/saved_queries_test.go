package log_test

import (
	"context"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// TestSavedQueryStore_RoundTrip proves the basic Save/List/Delete cycle
// against real sqlite, including that a two-kind, two-actor query
// keeps every term — the shape AC-010 needs.
func TestSavedQueryStore_RoundTrip(t *testing.T) {
	db := openTestDB(t)
	s := eventlog.NewSavedQueryStore(db)
	ctx := context.Background()

	q := eventlog.SavedQuery{
		ID:   "sq-1",
		Name: "multi-term query",
		Query: eventlog.FilterQuery{
			Kinds:     []string{"llm.request.started", "llm.response.completed"},
			ActorIDs:  []string{"actor-a", "actor-b"},
			FreeText:  "needle",
			Verbose:   true,
		},
		UserID: "user-1",
	}
	if err := s.Save(ctx, q); err != nil {
		t.Fatalf("Save: %v", err)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d, want 1", len(list))
	}
	got := list[0]
	if len(got.Query.Kinds) != 2 || got.Query.Kinds[0] != "llm.request.started" || got.Query.Kinds[1] != "llm.response.completed" {
		t.Errorf("Kinds = %v, want both terms preserved", got.Query.Kinds)
	}
	if len(got.Query.ActorIDs) != 2 || got.Query.ActorIDs[0] != "actor-a" || got.Query.ActorIDs[1] != "actor-b" {
		t.Errorf("ActorIDs = %v, want both terms preserved", got.Query.ActorIDs)
	}
	if got.Name != q.Name || got.UserID != q.UserID {
		t.Errorf("Name/UserID = %q/%q, want %q/%q", got.Name, got.UserID, q.Name, q.UserID)
	}

	// Upsert: saving the same ID again with a different name must not
	// create a second row.
	q.Name = "renamed"
	if err := s.Save(ctx, q); err != nil {
		t.Fatalf("Save (upsert): %v", err)
	}
	list2, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List (after upsert): %v", err)
	}
	if len(list2) != 1 {
		t.Fatalf("List (after upsert) returned %d, want 1 (upsert, not append)", len(list2))
	}
	if list2[0].Name != "renamed" {
		t.Errorf("Name after upsert = %q, want %q", list2[0].Name, "renamed")
	}

	if err := s.Delete(ctx, "sq-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list3, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List (after delete): %v", err)
	}
	if len(list3) != 0 {
		t.Errorf("List (after delete) returned %d, want 0", len(list3))
	}
}

// TestSavedQueryStore_SurvivesReopen is the direct persistence proof:
// close the database, reopen from the same directory, and read the
// saved query back.
func TestSavedQueryStore_SurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}

	db1, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}
	s1 := eventlog.NewSavedQueryStore(db1)
	if err := s1.Save(ctx, eventlog.SavedQuery{
		ID: "sq-reopen", Name: "reopen test",
		Query:     eventlog.FilterQuery{Kinds: []string{"a", "b"}},
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	s2 := eventlog.NewSavedQueryStore(db2)
	list, err := s2.List(ctx)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(list) != 1 || list[0].ID != "sq-reopen" {
		t.Fatalf("List after reopen = %+v, want the one saved query", list)
	}
	if len(list[0].Query.Kinds) != 2 {
		t.Errorf("Kinds after reopen = %v, want 2 terms", list[0].Query.Kinds)
	}
}
