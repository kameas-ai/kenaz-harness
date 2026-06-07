package credstore_test

// WP03 — audit emission tests.
//
// Coverage:
//   - Every Use call emits exactly one KindCredentialAccessed event.
//   - Emit fires even when op returns an error.
//   - nil Emitter is a no-op (no panic).
//   - Bursty channel-buffered emitter (>256 concurrent emits) drops on
//     full — never blocks the caller (NFR-002).
//   - Microbenchmark: emit overhead < 1ms per iteration (NFR-001).

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/credstore"
)

// fakeAuditEmitter records every Emit call.
type fakeAuditEmitter struct {
	mu     sync.Mutex
	events []audit.Event
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func (f *fakeAuditEmitter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// waitForCount polls until the emitter has seen at least n events or
// deadline passes.
func waitForCount(t *testing.T, f *fakeAuditEmitter, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.count() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("timed out waiting for %d audit events, got %d", n, f.count())
}

// newStoreWithEmitter wraps newStore + threads in a fake audit emitter.
func newStoreWithEmitter(t *testing.T) (credstore.Store, *fakeAuditEmitter) {
	t.Helper()
	em := &fakeAuditEmitter{}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("secret-value")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(em))
	t.Cleanup(s.Close)
	return s, em
}

// ── WP03 functional tests ────────────────────────────────────────────

func TestAudit_EmitOnSuccessfulUse(t *testing.T) {
	s, em := newStoreWithEmitter(t)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "provider_call")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if err := s.Use(ctx, h, func([]byte) error { return nil }); err != nil {
		t.Fatalf("Use: %v", err)
	}

	waitForCount(t, em, 1, time.Second)
	if c := em.count(); c != 1 {
		t.Fatalf("want 1 audit event, got %d", c)
	}
	if em.events[0].Kind != audit.KindCredentialAccessed {
		t.Fatalf("want KindCredentialAccessed, got %q", em.events[0].Kind)
	}
}

func TestAudit_EmitOnOpError(t *testing.T) {
	// Audit must fire even when op returns an error.
	s, em := newStoreWithEmitter(t)
	ctx := context.Background()

	h, _ := s.Issue(ctx, validRef("K"), "provider_test")
	opErr := errSentinel
	err := s.Use(ctx, h, func([]byte) error { return opErr })
	if err != opErr {
		t.Fatalf("want op error, got %v", err)
	}

	waitForCount(t, em, 1, time.Second)
	if em.count() != 1 {
		t.Fatalf("want 1 audit event after op error, got %d", em.count())
	}
}

// errSentinel is a reusable test error.
type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }

var errSentinel error = &sentinelErr{"sentinel"}

func TestAudit_NilEmitterNoOp(t *testing.T) {
	// A store without an emitter must not panic.
	resolver := newFakeResolver(map[string][]byte{"K": []byte("val")})
	s := credstore.New(resolver, nil) // no WithAuditEmitter
	t.Cleanup(s.Close)
	ctx := context.Background()

	h, err := s.Issue(ctx, validRef("K"), "provider_call")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if err := s.Use(ctx, h, func([]byte) error { return nil }); err != nil {
		t.Fatalf("Use with nil emitter: %v", err)
	}
}

// ── NFR-002: bursty emitter — channel full drops, never blocks ───────

// blockingEmitter blocks until release is closed, used to fill the
// channel in-flight.
type blockingEmitter struct {
	release chan struct{}
	emitted atomic.Int64
}

func (b *blockingEmitter) Emit(_ context.Context, _ audit.Event) error {
	<-b.release
	b.emitted.Add(1)
	return nil
}

func TestAudit_BurstyDropOnFull(t *testing.T) {
	// Fill the 256-slot channel and confirm extra emits are dropped
	// without blocking.
	be := &blockingEmitter{release: make(chan struct{})}
	resolver := newFakeResolver(map[string][]byte{"K": []byte("v")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(be))
	t.Cleanup(s.Close)
	ctx := context.Background()

	const total = 300 // more than auditChanCap=256
	const deadline = 2 * time.Second

	start := time.Now()
	for i := 0; i < total; i++ {
		h, err := s.Issue(ctx, validRef("K"), "burst")
		if err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
		_ = s.Use(ctx, h, func([]byte) error { return nil })
	}
	// All 300 Use calls must return within the deadline (no blocking).
	if elapsed := time.Since(start); elapsed > deadline {
		t.Fatalf("Use calls blocked: %v (want < %v)", elapsed, deadline)
	}

	// Unblock the drain goroutine.
	close(be.release)
}

// ── NFR-001: emit overhead < 1ms ─────────────────────────────────────

func BenchmarkAudit_EmitOverhead(b *testing.B) {
	em := &fakeAuditEmitter{}
	resolver := newFakeResolver(map[string][]byte{"BK": []byte("benchsecret")})
	s := credstore.New(resolver, nil, credstore.WithAuditEmitter(em))
	defer s.Close()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		h, err := s.Issue(ctx, validRef("BK"), "provider_call")
		if err != nil {
			b.Fatalf("Issue: %v", err)
		}
		if err := s.Use(ctx, h, func([]byte) error { return nil }); err != nil {
			b.Fatalf("Use: %v", err)
		}
	}

	b.StopTimer()
	// NFR-001: the benchmark runner will report ns/op; we assert at < 1ms
	// (1_000_000 ns). The test suite is run with -bench=. to surface this.
	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	const maxNs = 1_000_000 // 1ms
	if nsPerOp > maxNs {
		b.Errorf("emit overhead %.0f ns/op exceeds NFR-001 budget of %d ns", nsPerOp, maxNs)
	}
}
