package session

import (
	"context"
	"errors"
	"testing"
	"time"
)

func mustRecord(id, name string, position int64, now time.Time) Record {
	return Record{
		ID:           id,
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
		Position:     position,
	}
}

func TestMemStore_CreateAndGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)

	r := mustRecord("s1", "first", 0, now)
	if err := s.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Get(ctx, "s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "first" || got.Position != 0 {
		t.Errorf("got %+v, want name=first position=0", got)
	}
}

func TestMemStore_CreateRejectsEmptyName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	r := mustRecord("s1", "", 0, time.Now())
	if err := s.Create(ctx, r); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestMemStore_CreateDuplicateRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	err := s.Create(ctx, mustRecord("s1", "b", 1, now))
	if !errors.Is(err, ErrSessionExists) {
		t.Errorf("got %v, want ErrSessionExists", err)
	}
}

func TestMemStore_GetMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	_, err := s.Get(ctx, "ghost")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_ListByPosition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s2", "second", 1, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s1", "first", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s3", "third", 2, now)); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantOrder := []string{"s1", "s2", "s3"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("List[%d].ID = %s, want %s", i, got[i].ID, id)
		}
	}
}

func TestMemStore_ListSkipsArchived(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	r := mustRecord("s1", "first", 0, now)
	archived := now.Add(time.Minute)
	r.ArchivedAt = &archived
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := s.Create(ctx, mustRecord("s2", "second", 1, now)); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "s2" {
		t.Errorf("List = %+v, want [s2]", got)
	}
}

func TestMemStore_Rename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "old", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.Rename(ctx, "s1", "new", later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Name != "new" {
		t.Errorf("Name = %q, want new", got.Name)
	}
	if !got.UpdatedAt.Equal(later) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, later)
	}
}

func TestMemStore_RenameMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	err := s.Rename(ctx, "ghost", "x", time.Now())
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_RenameEmptyRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	_ = s.Create(ctx, mustRecord("s1", "ok", 0, now))
	if err := s.Rename(ctx, "s1", "", now); !errors.Is(err, ErrInvalidName) {
		t.Errorf("got %v, want ErrInvalidName", err)
	}
}

func TestMemStore_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "s1"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("after Delete: got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_DeleteMissing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	if err := s.Delete(ctx, "ghost"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_Reorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"a", "b", "c"} {
		if err := s.Create(ctx, mustRecord(id, id, int64(i), now)); err != nil {
			t.Fatal(err)
		}
	}
	later := now.Add(time.Hour)
	if err := s.Reorder(ctx, []string{"c", "a", "b"}, later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List(ctx)
	wantOrder := []string{"c", "a", "b"}
	for i, want := range wantOrder {
		if got[i].ID != want {
			t.Errorf("List[%d] = %s, want %s", i, got[i].ID, want)
		}
		if got[i].Position != int64(i) {
			t.Errorf("List[%d].Position = %d, want %d", i, got[i].Position, i)
		}
	}
}

func TestMemStore_ReorderRejectsUnknownID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	_ = s.Create(ctx, mustRecord("a", "a", 0, now))
	err := s.Reorder(ctx, []string{"a", "ghost"}, now)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
	// Mutation must roll back.
	got, _ := s.Get(ctx, "a")
	if got.Position != 0 {
		t.Errorf("Position changed despite failed Reorder: %d", got.Position)
	}
}

func TestMemStore_AppendMessageAssignsSequence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now()
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		m := Message{
			ID:        "m" + string(rune('0'+i)),
			SessionID: "s1",
			Role:      RoleUser,
			Content:   "hello",
			CreatedAt: now,
		}
		out, err := s.AppendMessage(ctx, m)
		if err != nil {
			t.Fatalf("AppendMessage[%d]: %v", i, err)
		}
		if out.Sequence != int64(i) {
			t.Errorf("Sequence = %d, want %d", out.Sequence, i)
		}
	}
	msgs, err := s.ListMessages(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Errorf("len = %d, want 3", len(msgs))
	}
}

func TestMemStore_AppendMessage_UnknownSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	_, err := s.AppendMessage(ctx, Message{ID: "m1", SessionID: "ghost", Content: "x"})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("got %v, want ErrSessionNotFound", err)
	}
}

func TestMemStore_UpdateDraftAndScroll(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Hour)
	if err := s.UpdateDraft(ctx, "s1", "hello world", later); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateScrollPosition(ctx, "s1", 42, later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if got.Draft != "hello world" {
		t.Errorf("Draft = %q, want hello world", got.Draft)
	}
	if got.ScrollPosition != 42 {
		t.Errorf("ScrollPosition = %d, want 42", got.ScrollPosition)
	}
}

func TestMemStore_UpdateLastActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.Create(ctx, mustRecord("s1", "a", 0, now)); err != nil {
		t.Fatal(err)
	}
	later := now.Add(2 * time.Hour)
	if err := s.UpdateLastActive(ctx, "s1", later); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "s1")
	if !got.LastActiveAt.Equal(later) {
		t.Errorf("LastActiveAt = %v, want %v", got.LastActiveAt, later)
	}
}
