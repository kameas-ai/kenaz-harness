package attachments_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/attachments"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

func openTestDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db
}

func TestSQLStore_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := attachments.NewSQLStore(db)

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	att := attachments.Attachment{
		ID:            "abcd1234",
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:hash",
		Content:       "hello world",
		Kind:          attachments.KindSystem,
		Position:      0,
		CreatedAt:     now,
	}
	if err := store.Add(ctx, att); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := store.Get(ctx, att.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "hello world" {
		t.Errorf("Content = %q", got.Content)
	}
	if got.ScopeKind != attachments.ScopeKindSession {
		t.Errorf("ScopeKind = %q", got.ScopeKind)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

func TestSQLStore_ListByScope(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := attachments.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC()

	// Two session-scope rows under s1, one under s2.
	for i, c := range []string{"a", "b"} {
		if err := store.Add(ctx, attachments.Attachment{
			ID:            "id-s1-" + c,
			ScopeKind:     attachments.ScopeKindSession,
			ScopeID:       "s1",
			ContentSource: "inline:" + c,
			Content:       c,
			Kind:          attachments.KindSystem,
			Position:      i,
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Add(ctx, attachments.Attachment{
		ID:            "id-s2",
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s2",
		ContentSource: "inline:other",
		Content:       "other",
		Kind:          attachments.KindSystem,
		Position:      0,
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   "s1",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 || got[0].Content != "a" || got[1].Content != "b" {
		t.Errorf("List = %+v", got)
	}
}

func TestSQLStore_RemoveAndUpdateContent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := attachments.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Add(ctx, attachments.Attachment{
		ID:            "id-x",
		ScopeKind:     attachments.ScopeKindGlobal,
		ContentSource: "library:foo.md",
		Content:       "stale",
		Kind:          attachments.KindSystem,
		CreatedAt:     now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateContent(ctx, "id-x", "fresh"); err != nil {
		t.Fatalf("UpdateContent: %v", err)
	}
	got, err := store.Get(ctx, "id-x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Content != "fresh" {
		t.Errorf("Content = %q, want fresh", got.Content)
	}
	if err := store.Remove(ctx, "id-x"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := store.Get(ctx, "id-x"); !errors.Is(err, attachments.ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	if err := store.Remove(ctx, "id-x"); !errors.Is(err, attachments.ErrNotFound) {
		t.Errorf("double Remove = %v, want ErrNotFound", err)
	}
	if err := store.UpdateContent(ctx, "ghost", "x"); !errors.Is(err, attachments.ErrNotFound) {
		t.Errorf("UpdateContent ghost = %v, want ErrNotFound", err)
	}
}

func TestSQLStore_Reorder(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := attachments.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	ids := []string{"r1", "r2", "r3"}
	for i, id := range ids {
		if err := store.Add(ctx, attachments.Attachment{
			ID:            id,
			ScopeKind:     attachments.ScopeKindGlobal,
			ContentSource: "inline:" + id,
			Content:       id,
			Kind:          attachments.KindSystem,
			Position:      i,
			CreatedAt:     now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	rev := []string{"r3", "r2", "r1"}
	if err := store.Reorder(ctx, attachments.ScopeKindGlobal, "", rev); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got, err := store.List(ctx, attachments.ScopeFilter{ScopeKind: attachments.ScopeKindGlobal})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for i, want := range rev {
		if got[i].ID != want {
			t.Errorf("List[%d].ID = %q, want %q", i, got[i].ID, want)
		}
		if got[i].Position != i {
			t.Errorf("List[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}

	if err := store.Reorder(ctx, attachments.ScopeKindGlobal, "", []string{"r1"}); err == nil {
		t.Errorf("expected partial-permutation error")
	}
}

// TestSQLStore_PersistsAcrossReopen verifies the table+rows survive a
// DB close/reopen cycle (i.e. migration 0301 wires real durability).
func TestSQLStore_PersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	store := attachments.NewSQLStore(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.Add(ctx, attachments.Attachment{
		ID:            "persist",
		ScopeKind:     attachments.ScopeKindGlobal,
		ContentSource: "inline:persist",
		Content:       "carry me over",
		Kind:          attachments.KindSystem,
		CreatedAt:     now,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(ctx)
	store2 := attachments.NewSQLStore(db2)
	got, err := store2.Get(ctx, "persist")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Content != "carry me over" {
		t.Errorf("Content after reopen = %q", got.Content)
	}
}
