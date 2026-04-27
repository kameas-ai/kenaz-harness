package bash

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStore_PutGet(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.Put("run-1", Record{
		Command:  "echo hi",
		Stdout:   "hi\n",
		ExitCode: 0,
	})
	got, err := s.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Stdout != "hi\n" {
		t.Errorf("Stdout = %q want %q", got.Stdout, "hi\n")
	}
	if got.RunID != "run-1" {
		t.Errorf("RunID = %q want %q", got.RunID, "run-1")
	}
	if got.StoredAt.IsZero() {
		t.Error("StoredAt is zero")
	}
}

func TestStore_GetMissing(t *testing.T) {
	t.Parallel()
	s := NewStore()
	if _, err := s.Get(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v want ErrNotFound", err)
	}
}

func TestStore_TTLExpiry(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	s := NewStore(WithStoreTTL(1*time.Second), WithStoreClock(clock))
	s.Put("run-1", Record{Stdout: "ok"})
	if _, err := s.Get(context.Background(), "run-1"); err != nil {
		t.Fatalf("immediate Get: %v", err)
	}
	now = now.Add(2 * time.Second)
	if _, err := s.Get(context.Background(), "run-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired Get err = %v want ErrNotFound", err)
	}
}

func TestStore_NilSafe(t *testing.T) {
	t.Parallel()
	var s *Store
	s.Put("x", Record{}) // should not panic
	if _, err := s.Get(context.Background(), "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("nil Get err = %v want ErrNotFound", err)
	}
	if n := s.Sweep(); n != 0 {
		t.Errorf("nil Sweep = %d", n)
	}
}

func TestStore_OverwriteWins(t *testing.T) {
	t.Parallel()
	s := NewStore()
	s.Put("run-1", Record{Stdout: "first"})
	s.Put("run-1", Record{Stdout: "second"})
	got, err := s.Get(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Stdout != "second" {
		t.Errorf("Stdout = %q want %q", got.Stdout, "second")
	}
}

func TestStore_Sweep(t *testing.T) {
	t.Parallel()
	now := time.Now()
	clock := func() time.Time { return now }
	s := NewStore(WithStoreTTL(time.Second), WithStoreClock(clock))
	s.Put("run-1", Record{})
	s.Put("run-2", Record{})
	now = now.Add(2 * time.Second)
	if removed := s.Sweep(); removed != 2 {
		t.Errorf("Sweep removed = %d want 2", removed)
	}
}
