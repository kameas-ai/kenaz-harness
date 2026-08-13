package compaction

// This file is part of the PERSISTED-HISTORY layer of the compaction
// package — the half that rewrites a session's stored transcript,
// as opposed to the in-memory FR-041 strategy layer it now sits beside.
// It moved here from core/compaction in
// agentgraph-total-convergence-01PMGX01 WP10a, which merged the
// harness's two compaction packages into one: the two are layers of a
// single subsystem, not two subsystems. The `session_` filename prefix
// marks the layer at a glance; compactor.go carries the package-level
// map of how the layers fit together.
//
// session_scheduler.go runs the soft-archive sweep (session_sweep.go::RunSweep) on a
// periodic tick. The shape mirrors core/memory/prune/scheduler.go (same
// Start/Stop/RunOnce/LastRun API + WithSweepInterval/WithSweepClock/WithOnSweep
// options) so callers can reason about the two schedulers
// interchangeably and WP08 can wire them side-by-side in core boot.
//
// Trigger model:
//   - On Start, if LastRun is zero OR clock() - LastRun >= interval,
//     a sweep fires immediately on a fresh goroutine before the tick
//     loop settles in. Catch-up case for the harness having been
//     closed for several days.
//   - The scheduler fires every Interval thereafter.
//   - RunOnce is exposed so WP06's "sweep now" admin path (or a future
//     inspector button) can drive the sweep on demand.
//
// Errors from runOnce are SWALLOWED at the loop level — the scheduler
// itself never returns an error to the surrounding goroutine. The
// optional onSweep callback receives every (deleted, err) pair so
// metrics / log surfaces can record outcomes.
//
// Concurrency: Start MUST NOT be called twice without an intervening
// Stop. Stop is idempotent and safe to call from any goroutine; it
// blocks until the in-flight sweep returns.

import (
	"context"
	"sync"
	"time"
)

// defaultSchedulerInterval is the locked plan default — once per day.
const defaultSchedulerInterval = 24 * time.Hour

// SweepScheduler runs the compaction sweep on a periodic tick. See file
// header for the trigger model and concurrency contract.
type SweepScheduler struct {
	interval time.Duration
	clock    func() time.Time
	onSweep  func(deleted int, err error)
	runOnce  func(ctx context.Context) (int, error)

	mu      sync.Mutex
	lastRun time.Time
	stopCh  chan struct{}
	doneCh  chan struct{}
	running bool
}

// SweepSchedulerOption tunes a SweepScheduler.
type SweepSchedulerOption func(*SweepScheduler)

// WithSweepInterval overrides the default sweep cadence (24h). Non-positive
// values are ignored.
func WithSweepInterval(d time.Duration) SweepSchedulerOption {
	return func(s *SweepScheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithSweepClock overrides the wall clock. Tests use this to drive the
// catch-up logic deterministically.
func WithSweepClock(c func() time.Time) SweepSchedulerOption {
	return func(s *SweepScheduler) {
		if c != nil {
			s.clock = c
		}
	}
}

// WithOnSweep installs a callback invoked after every sweep with the
// (deleted, err) pair. Used by metrics surfaces; tests use it to
// assert the loop fired.
func WithOnSweep(fn func(deleted int, err error)) SweepSchedulerOption {
	return func(s *SweepScheduler) { s.onSweep = fn }
}

// NewSweepScheduler constructs a SweepScheduler around the given runOnce func.
// runOnce is the caller's bound RunSweep call: callers close over their
// store / audit / retentionDays / now closures so the scheduler itself
// stays oblivious to those details.
func NewSweepScheduler(runOnce func(ctx context.Context) (int, error), opts ...SweepSchedulerOption) *SweepScheduler {
	s := &SweepScheduler{
		interval: defaultSchedulerInterval,
		clock:    func() time.Time { return time.Now().UTC() },
		runOnce:  runOnce,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start begins the periodic loop. The provided ctx cancels the loop.
// Returns immediately. Use Stop to wait for the loop to exit cleanly.
//
// Start is idempotent — a second Start without an intervening Stop is
// a no-op so callers don't accidentally double-launch.
func (s *SweepScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go s.loop(ctx)
}

// Stop signals the loop to exit and blocks until it returns. Safe to
// call multiple times — a Stop on a never-started or already-stopped
// scheduler is a no-op.
func (s *SweepScheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	done := s.doneCh
	s.running = false
	s.mu.Unlock()
	<-done
}

// RunOnce invokes runOnce immediately, updating LastRun and firing the
// onSweep callback. Errors from runOnce are returned to the caller —
// only the background loop swallows them.
func (s *SweepScheduler) RunOnce(ctx context.Context) (int, error) {
	if s.runOnce == nil {
		return 0, nil
	}
	deleted, err := s.runOnce(ctx)
	s.mu.Lock()
	s.lastRun = s.clock()
	cb := s.onSweep
	s.mu.Unlock()
	if cb != nil {
		cb(deleted, err)
	}
	return deleted, err
}

// LastRun returns the timestamp of the most recent runOnce invocation,
// or zero if runOnce has never been called.
func (s *SweepScheduler) LastRun() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun
}

// loop is the background tick driver. The catch-up branch fires before
// the ticker is created so a freshly-started scheduler that was already
// past-due doesn't have to wait a full interval for its first sweep.
func (s *SweepScheduler) loop(ctx context.Context) {
	defer close(s.doneCh)

	// Catch-up sweep on boot if the persisted LastRun is unset or
	// older than one interval.
	s.mu.Lock()
	overdue := s.lastRun.IsZero() || s.clock().Sub(s.lastRun) >= s.interval
	s.mu.Unlock()
	if overdue {
		// Errors are swallowed; the onSweep callback inside RunOnce
		// receives the (deleted, err) pair if the caller wired it.
		_, _ = s.RunOnce(ctx)
	}

	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			_, _ = s.RunOnce(ctx)
		}
	}
}

// SeedLastRun installs a starting LastRun timestamp before Start. WP08
// uses this to feed the persisted last-sweep time from disk so the
// catch-up branch behaves correctly. Must be called before Start;
// calling it on a running scheduler is a no-op.
func (s *SweepScheduler) SeedLastRun(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.lastRun = t
}
