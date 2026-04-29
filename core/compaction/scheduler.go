package compaction

// scheduler.go runs the soft-archive sweep (sweep.go::RunSweep) on a
// periodic tick. The shape mirrors core/memory/prune/scheduler.go (same
// Start/Stop/RunOnce/LastRun API + WithInterval/WithClock/WithOnSweep
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

// Scheduler runs the compaction sweep on a periodic tick. See file
// header for the trigger model and concurrency contract.
type Scheduler struct {
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

// SchedulerOption tunes a Scheduler.
type SchedulerOption func(*Scheduler)

// WithInterval overrides the default sweep cadence (24h). Non-positive
// values are ignored.
func WithInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithClock overrides the wall clock. Tests use this to drive the
// catch-up logic deterministically.
func WithClock(c func() time.Time) SchedulerOption {
	return func(s *Scheduler) {
		if c != nil {
			s.clock = c
		}
	}
}

// WithOnSweep installs a callback invoked after every sweep with the
// (deleted, err) pair. Used by metrics surfaces; tests use it to
// assert the loop fired.
func WithOnSweep(fn func(deleted int, err error)) SchedulerOption {
	return func(s *Scheduler) { s.onSweep = fn }
}

// NewScheduler constructs a Scheduler around the given runOnce func.
// runOnce is the caller's bound RunSweep call: callers close over their
// store / audit / retentionDays / now closures so the scheduler itself
// stays oblivious to those details.
func NewScheduler(runOnce func(ctx context.Context) (int, error), opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
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
func (s *Scheduler) Start(ctx context.Context) {
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
func (s *Scheduler) Stop() {
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
func (s *Scheduler) RunOnce(ctx context.Context) (int, error) {
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
func (s *Scheduler) LastRun() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun
}

// loop is the background tick driver. The catch-up branch fires before
// the ticker is created so a freshly-started scheduler that was already
// past-due doesn't have to wait a full interval for its first sweep.
func (s *Scheduler) loop(ctx context.Context) {
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
func (s *Scheduler) SeedLastRun(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return
	}
	s.lastRun = t
}
