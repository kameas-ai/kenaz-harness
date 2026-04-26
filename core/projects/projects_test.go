package projects

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func counterIDGen() IDGen {
	var n int64
	return func() (string, error) {
		v := atomic.AddInt64(&n, 1)
		return "proj-" + strconv.FormatInt(v, 10), nil
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	now := time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC)
	clk := func() time.Time { now = now.Add(time.Second); return now }
	return NewManager(NewMemoryStore(),
		WithClock(clk),
		WithIDGen(counterIDGen()),
	)
}

func TestManager_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)

	p, err := m.Create(ctx, "foo", "the foo project")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" || p.Name != "foo" || p.Description != "the foo project" {
		t.Errorf("Create returned %+v", p)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Errorf("zero timestamps: %+v", p)
	}
}

func TestManager_Create_RejectsBlankName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	if _, err := m.Create(ctx, "   ", ""); !errors.Is(err, ErrNameRequired) {
		t.Errorf("got %v, want ErrNameRequired", err)
	}
}

func TestManager_Get(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	p, _ := m.Create(ctx, "foo", "")
	got, err := m.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != p.ID || got.Name != "foo" {
		t.Errorf("got %+v", got)
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	_, err := m.Get(ctx, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestManager_List_OrderedByCreatedAt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	a, _ := m.Create(ctx, "a", "")
	b, _ := m.Create(ctx, "b", "")
	c, _ := m.Create(ctx, "c", "")
	got, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 || got[0].ID != a.ID || got[1].ID != b.ID || got[2].ID != c.ID {
		t.Errorf("List = %+v, want a/b/c order", got)
	}
}

func TestManager_Rename(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	p, _ := m.Create(ctx, "foo", "")
	if err := m.Rename(ctx, p.ID, "bar"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	got, _ := m.Get(ctx, p.ID)
	if got.Name != "bar" {
		t.Errorf("Name = %q, want bar", got.Name)
	}
	if !got.UpdatedAt.After(p.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: was %v, now %v", p.UpdatedAt, got.UpdatedAt)
	}
}

func TestManager_Rename_TrimsAndRejectsBlank(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	p, _ := m.Create(ctx, "x", "")
	if err := m.Rename(ctx, p.ID, "  trimmed  "); err != nil {
		t.Fatal(err)
	}
	got, _ := m.Get(ctx, p.ID)
	if got.Name != "trimmed" {
		t.Errorf("Name = %q, want trimmed", got.Name)
	}
	if err := m.Rename(ctx, p.ID, "   "); !errors.Is(err, ErrNameRequired) {
		t.Errorf("got %v, want ErrNameRequired", err)
	}
}

func TestManager_Rename_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	if err := m.Rename(ctx, "ghost", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestManager_UpdateDescription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	p, _ := m.Create(ctx, "foo", "old")
	if err := m.UpdateDescription(ctx, p.ID, "new"); err != nil {
		t.Fatalf("UpdateDescription: %v", err)
	}
	got, _ := m.Get(ctx, p.ID)
	if got.Description != "new" {
		t.Errorf("Description = %q, want new", got.Description)
	}
}

func TestManager_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	p, _ := m.Create(ctx, "doomed", "")
	if err := m.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := m.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestManager_Delete_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := newTestManager(t)
	if err := m.Delete(ctx, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestNewManager_NilStorePanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("NewManager(nil) did not panic")
		}
	}()
	_ = NewManager(nil)
}

func TestMemStore_DuplicateRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := NewMemoryStore()
	now := time.Now().UTC()
	p := Project{ID: "p1", Name: "a", CreatedAt: now, UpdatedAt: now}
	if err := s.Create(ctx, p); err != nil {
		t.Fatal(err)
	}
	err := s.Create(ctx, p)
	if !errors.Is(err, ErrProjectExists) {
		t.Errorf("got %v, want ErrProjectExists", err)
	}
}
