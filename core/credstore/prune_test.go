package credstore_test

// WP07 — retention sweep tests.
//
// Coverage:
//   - SweepAuditLog with retainDuration=0 is a no-op (returns 0, nil).
//   - SweepAuditLog with retainDuration>0 calls the injected AuditSweeper.
//   - nil AuditSweeper is a no-op (no panic).
//   - Sweeper receives the correct cutoff time.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore"
)

// fakeSweeper records calls made to SweepCredentialAccessed.
type fakeSweeper struct {
	calls  atomic.Int64
	cutoff time.Time
	result int
}

func (f *fakeSweeper) SweepCredentialAccessed(_ context.Context, cutoff time.Time) (int, error) {
	f.calls.Add(1)
	f.cutoff = cutoff
	return f.result, nil
}

func TestSweep_ZeroRetainIsNoOp(t *testing.T) {
	sw := &fakeSweeper{result: 100}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditSweeper(sw))
	t.Cleanup(s.Close)

	n, err := s.SweepAuditLog(context.Background(), 0)
	if err != nil {
		t.Fatalf("SweepAuditLog(0): %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 deleted when retainDuration=0, got %d", n)
	}
	if sw.calls.Load() != 0 {
		t.Fatalf("sweeper should not be called for retainDuration=0")
	}
}

func TestSweep_NonZeroCallsSweeper(t *testing.T) {
	sw := &fakeSweeper{result: 42}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditSweeper(sw))
	t.Cleanup(s.Close)

	before := time.Now()
	n, err := s.SweepAuditLog(context.Background(), 30*24*time.Hour)
	after := time.Now()
	if err != nil {
		t.Fatalf("SweepAuditLog: %v", err)
	}
	if n != 42 {
		t.Fatalf("want 42 deleted, got %d", n)
	}
	if sw.calls.Load() != 1 {
		t.Fatalf("sweeper should be called exactly once")
	}
	// Cutoff should be approximately 30 days ago.
	expectedCutoff := before.Add(-30 * 24 * time.Hour)
	latestCutoff := after.Add(-30 * 24 * time.Hour)
	if sw.cutoff.Before(expectedCutoff.Add(-time.Second)) || sw.cutoff.After(latestCutoff.Add(time.Second)) {
		t.Fatalf("cutoff %v outside expected range [%v, %v]", sw.cutoff, expectedCutoff, latestCutoff)
	}
}

func TestSweep_NilSweeperNoOp(t *testing.T) {
	// No WithAuditSweeper — SweepAuditLog must not panic.
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil)
	t.Cleanup(s.Close)

	n, err := s.SweepAuditLog(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepAuditLog with nil sweeper: %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0, got %d", n)
	}
}
