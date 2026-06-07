package projects_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/projects"
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
	store := projects.NewSQLStore(db)

	ctx := context.Background()
	now := time.Now().UTC()
	p := projects.Project{
		ID:          "p1",
		Name:        "alpha",
		Description: "the alpha project",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "alpha" || got.Description != "the alpha project" {
		t.Errorf("Get = %+v", got)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Errorf("timestamps drifted: %+v", got)
	}
}

func TestSQLStore_GetNotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	if _, err := store.Get(context.Background(), "ghost"); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestSQLStore_ListOrdered(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"a", "b", "c"} {
		ts := base.Add(time.Duration(i) * time.Second)
		if err := store.Create(ctx, projects.Project{ID: id, Name: id, CreatedAt: ts, UpdatedAt: ts}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Errorf("List = %+v", got)
	}
}

func TestSQLStore_RenameAndUpdateDescription(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Create(ctx, projects.Project{ID: "p1", Name: "old", Description: "d1", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := store.Rename(ctx, "p1", "new", later); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateDescription(ctx, "p1", "d2", later); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get(ctx, "p1")
	if got.Name != "new" || got.Description != "d2" {
		t.Errorf("after updates = %+v", got)
	}
	if err := store.Rename(ctx, "ghost", "x", later); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("Rename ghost = %v, want ErrNotFound", err)
	}
}

func TestSQLStore_Delete(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Create(ctx, projects.Project{ID: "p1", Name: "a", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "p1"); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, "p1"); !errors.Is(err, projects.ErrNotFound) {
		t.Errorf("Delete missing = %v, want ErrNotFound", err)
	}
}

// TestSQLStore_PersistsAcrossReopen exercises A1 (project persistence
// across Wails restart): create a project, close the DB, reopen, and
// confirm List surfaces the row.
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
	store := projects.NewSQLStore(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.Create(ctx, projects.Project{ID: "p1", Name: "alpha", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close(ctx)
	store2 := projects.NewSQLStore(db2)
	got, err := store2.Get(ctx, "p1")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "alpha" {
		t.Errorf("Name after reopen = %q, want alpha", got.Name)
	}
	all, err := store2.List(ctx)
	if err != nil {
		t.Fatalf("List after reopen: %v", err)
	}
	if len(all) != 1 || all[0].ID != "p1" {
		t.Errorf("List after reopen = %+v", all)
	}
}
