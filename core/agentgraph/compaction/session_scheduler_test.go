package compaction

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// session_scheduler_test.go drives the periodic SweepScheduler through a fake
// runOnce closure. The tests set short intervals (50ms) for the
// tick-based assertions and rely on WithSweepClock + SeedLastRun for the
// catch-up branch coverage.

func TestScheduler_TickFiresMultipleTimes(t *testing.T) {
	var calls int32
	runOnce := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}

	s := NewSweepScheduler(runOnce, WithSweepInterval(50*time.Millisecond))
	// SeedLastRun to "now" to suppress the immediate catch-up — we want
	// to assert ticks specifically, not the boot sweep.
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Wait long enough that at least 2 ticks should have fired (50ms
	// interval × 5 = 250ms, generous for CI noise).
	time.Sleep(280 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected at least 2 tick-driven runOnce calls in 280ms, got %d", got)
	}
}

func TestScheduler_FirstRunAtStartupWhenLastRunZero(t *testing.T) {
	fired := make(chan struct{}, 1)
	runOnce := func(ctx context.Context) (int, error) {
		select {
		case fired <- struct{}{}:
		default:
		}
		return 0, nil
	}

	// Long interval — if the catch-up branch doesn't fire, the test
	// times out instead of accidentally passing on a tick.
	s := NewSweepScheduler(runOnce, WithSweepInterval(24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	select {
	case <-fired:
		// success — catch-up sweep ran on Start
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("expected runOnce to fire immediately when LastRun is zero")
	}
}

func TestScheduler_NoImmediateRunWhenRecent(t *testing.T) {
	var calls int32
	runOnce := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	s := NewSweepScheduler(runOnce, WithSweepInterval(24*time.Hour), WithSweepClock(clock))
	s.SeedLastRun(now.Add(-1 * time.Second)) // just ran a second ago

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	// Give the loop time to do whatever it would do; with a 24h interval
	// and a recent LastRun, no run should fire in 200ms.
	time.Sleep(200 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected zero runs (recent LastRun + 24h interval), got %d", got)
	}
}

func TestScheduler_StopIsIdempotent(t *testing.T) {
	s := NewSweepScheduler(func(ctx context.Context) (int, error) { return 0, nil },
		WithSweepInterval(24*time.Hour))
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	s.Stop()
	// Second Stop must not panic.
	s.Stop()
	// Third Stop on a never-restarted scheduler must also not panic.
	s.Stop()
}

func TestScheduler_StopBeforeStartIsNoop(t *testing.T) {
	s := NewSweepScheduler(func(ctx context.Context) (int, error) { return 0, nil })
	// Must not panic.
	s.Stop()
}

func TestScheduler_OnSweepCallbackFires(t *testing.T) {
	type cbResult struct {
		deleted int
		err     error
	}
	var (
		mu      sync.Mutex
		results []cbResult
	)
	cb := func(deleted int, err error) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, cbResult{deleted: deleted, err: err})
	}

	runOnce := func(ctx context.Context) (int, error) {
		return 7, nil
	}

	s := NewSweepScheduler(runOnce,
		WithSweepInterval(50*time.Millisecond),
		WithOnSweep(cb))
	// Suppress catch-up so we count ticks only.
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	time.Sleep(180 * time.Millisecond)

	mu.Lock()
	count := len(results)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected at least 2 onSweep invocations, got %d", count)
	}
	mu.Lock()
	for i, r := range results {
		if r.deleted != 7 {
			t.Fatalf("results[%d].deleted: want 7, got %d", i, r.deleted)
		}
		if r.err != nil {
			t.Fatalf("results[%d].err: want nil, got %v", i, r.err)
		}
	}
	mu.Unlock()
}

func TestScheduler_ErrorsSwallowedLoopContinues(t *testing.T) {
	var calls int32
	wantErr := errors.New("simulated sweep failure")
	runOnce := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, wantErr
	}

	var (
		mu      sync.Mutex
		seenErr error
	)
	cb := func(deleted int, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			seenErr = err
		}
	}

	s := NewSweepScheduler(runOnce,
		WithSweepInterval(50*time.Millisecond),
		WithOnSweep(cb))
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	time.Sleep(220 * time.Millisecond)

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("expected the loop to continue past errors (>=2 calls), got %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if seenErr == nil {
		t.Fatalf("expected onSweep callback to receive the error")
	}
	if !errors.Is(seenErr, wantErr) {
		t.Fatalf("seenErr: want %v, got %v", wantErr, seenErr)
	}
}

func TestScheduler_RunOnceUpdatesLastRun(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	s := NewSweepScheduler(
		func(ctx context.Context) (int, error) { return 3, nil },
		WithSweepClock(clock),
	)

	if !s.LastRun().IsZero() {
		t.Fatalf("LastRun should be zero before RunOnce, got %v", s.LastRun())
	}

	deleted, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("RunOnce deleted: want 3, got %d", deleted)
	}
	if !s.LastRun().Equal(now) {
		t.Fatalf("LastRun: want %v, got %v", now, s.LastRun())
	}
}

func TestScheduler_RunOnceWithoutFuncReturnsZero(t *testing.T) {
	s := NewSweepScheduler(nil)
	deleted, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", deleted)
	}
}

func TestScheduler_DoubleStartIsNoop(t *testing.T) {
	var calls int32
	runOnce := func(ctx context.Context) (int, error) {
		atomic.AddInt32(&calls, 1)
		return 0, nil
	}
	s := NewSweepScheduler(runOnce, WithSweepInterval(24*time.Hour))
	s.SeedLastRun(time.Now()) // suppress catch-up

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	s.Start(ctx) // must be a no-op, not a second loop

	// Brief settle window; with 24h interval and recent LastRun, no
	// runOnce should fire from either Start.
	time.Sleep(100 * time.Millisecond)

	s.Stop()

	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("expected 0 calls, got %d (double-Start may have spawned a second loop)", got)
	}
}
