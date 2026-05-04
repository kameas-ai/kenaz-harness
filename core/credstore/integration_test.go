package credstore_test

// WP09 — end-to-end integration scenarios.
//
// Coverage:
//   - RoundTrip + audit emits exactly once per call.
//   - Concurrent Issue/Use/RoundTrip under -race is clean.
//   - Retention sweep: SweepAuditLog with non-zero retainDuration calls
//     AuditSweeper; SweepAuditLog(0) is always a no-op.
//   - Error path: audit emits even when RoundTrip op fails.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/credstore"
)

// ── RoundTrip emits exactly one audit event ──────────────────────────

func TestIntegration_RoundTripAuditOnce(t *testing.T) {
	em := &fakeAuditEmitter{}
	resolver := newFakeResolver(map[string][]byte{"APIKEY": []byte("topsecret")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(em))
	t.Cleanup(s.Close)
	ctx := context.Background()

	err := s.RoundTrip(ctx, validRef("APIKEY"), "provider_call", func(b []byte) error {
		if string(b) != "topsecret" {
			t.Errorf("unexpected value: %q", string(b))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}

	waitForCount(t, em, 1, time.Second)
	if c := em.count(); c != 1 {
		t.Fatalf("want 1 audit event, got %d", c)
	}
}

// ── Audit emits even on op error ─────────────────────────────────────

func TestIntegration_RoundTripAuditOnOpError(t *testing.T) {
	em := &fakeAuditEmitter{}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(em))
	t.Cleanup(s.Close)
	ctx := context.Background()

	opErr := errors.New("op-failure")
	err := s.RoundTrip(ctx, validRef("K"), "provider_call", func([]byte) error { return opErr })
	if !errors.Is(err, opErr) {
		t.Fatalf("want opErr, got %v", err)
	}

	waitForCount(t, em, 1, time.Second)
	if c := em.count(); c != 1 {
		t.Fatalf("want 1 audit event on error path, got %d", c)
	}
}

// ── Concurrent Issue/Use/RoundTrip under -race ───────────────────────

func TestIntegration_ConcurrentRaceClean(t *testing.T) {
	const workers = 64
	em := &fakeAuditEmitter{}
	resolver := newFakeResolver(map[string][]byte{"CK": []byte("val")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(em))
	t.Cleanup(s.Close)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RoundTrip(ctx, validRef("CK"), "provider_call", func([]byte) error { return nil }); err != nil {
				t.Errorf("RoundTrip: %v", err)
			}
		}()
	}
	wg.Wait()

	waitForCount(t, em, workers, 2*time.Second)
	if c := em.count(); c != workers {
		t.Errorf("want %d audit events, got %d", workers, c)
	}
}

// ── Retention sweep: AuditSweeper called + no-op on zero ─────────────

// trackingSweeper records what it was asked to delete.
type trackingSweeper struct {
	mu      sync.Mutex
	cutoffs []time.Time
	perCall int
}

func (ts *trackingSweeper) SweepCredentialAccessed(_ context.Context, cutoff time.Time) (int, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.cutoffs = append(ts.cutoffs, cutoff)
	return ts.perCall, nil
}

func TestIntegration_RetentionSweepCalled(t *testing.T) {
	sw := &trackingSweeper{perCall: 77}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditSweeper(sw))
	t.Cleanup(s.Close)

	// First emit some events via RoundTrip to establish state.
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = s.RoundTrip(ctx, validRef("K"), "provider_call", func([]byte) error { return nil })
	}

	// Sweep with 30-day retention.
	n, err := s.SweepAuditLog(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("SweepAuditLog: %v", err)
	}
	if n != 77 {
		t.Fatalf("want 77 deleted (fake result), got %d", n)
	}

	sw.mu.Lock()
	calls := len(sw.cutoffs)
	sw.mu.Unlock()
	if calls != 1 {
		t.Fatalf("want 1 sweep call, got %d", calls)
	}
}

func TestIntegration_RetentionSweepZeroNoOp(t *testing.T) {
	sw := &trackingSweeper{perCall: 999}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditSweeper(sw))
	t.Cleanup(s.Close)

	n, err := s.SweepAuditLog(context.Background(), 0)
	if err != nil {
		t.Fatalf("SweepAuditLog(0): %v", err)
	}
	if n != 0 {
		t.Fatalf("want 0 for retainDuration=0, got %d", n)
	}

	sw.mu.Lock()
	calls := len(sw.cutoffs)
	sw.mu.Unlock()
	if calls != 0 {
		t.Fatalf("sweeper should not be called when retainDuration=0, got %d calls", calls)
	}
}
