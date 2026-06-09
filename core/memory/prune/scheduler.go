package prune

import (
	"context"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory"
)

// Scheduler runs the prune sweep on a cadence (default once per day
// per active project). It is goroutine-safe and shutdown-safe — Stop
// blocks until the in-flight sweep returns.
//
// Trigger model:
//
//   - On Start, if the on-disk LastRunAt is older than Interval the
//     sweep fires immediately. This covers the harness-startup case
//     where the user hasn't opened the app for several days.
//   - The scheduler subsequently fires every Interval.
//   - RunOnce is exposed for the inspector's "prune now" button.
//
// The scheduler does NOT persist LastRunAt anywhere — the prune
// metrics package (`metrics.go`) emits an event the host can record.
// The scheduler accepts a clock (via WithClock) so tests can drive
// trigger logic deterministically.
type Scheduler struct {
	pruner   *Pruner
	interval time.Duration
	now      func() time.Time
	scopes   []memory.ScopeFilter
	onSweep  func(Decision)

	mu       sync.Mutex
	last     time.Time
	stopCh   chan struct{}
	doneCh   chan struct{}
	running  bool
}

// SchedulerOption tunes a Scheduler.
type SchedulerOption func(*Scheduler)

// WithInterval overrides the default sweep cadence.
func WithInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithClock overrides the wall clock. Tests use this to step time.
func WithClock(now func() time.Time) SchedulerOption {
	return func(s *Scheduler) {
		if now != nil {
			s.now = now
		}
	}
}

// WithScopes restricts the sweep to a specific (kind, id) set. Empty
// means prune everywhere — the default.
func WithScopes(scopes ...memory.ScopeFilter) SchedulerOption {
	return func(s *Scheduler) { s.scopes = scopes }
}

// WithOnSweep installs a callback invoked after every sweep with
// the decision. Used by metrics.go to record kept/dropped/collapsed
// counts; tests use it to assert the scheduler fired.
func WithOnSweep(fn func(Decision)) SchedulerOption {
	return func(s *Scheduler) { s.onSweep = fn }
}

// NewScheduler returns a Scheduler ready to Start.
func NewScheduler(p *Pruner, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{
		pruner:   p,
		interval: 24 * time.Hour,
		now:      func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Start launches the background loop. lastRun is the recorded
// last-sweep time read from persistence (zero ⇒ "never"). When
// lastRun + Interval is already in the past, Start fires a sweep
// immediately on a fresh goroutine before settling into the cadence.
//
// Start is idempotent — calling it twice without an intervening Stop
// is a no-op on the second call.
func (s *Scheduler) Start(ctx context.Context, lastRun time.Time) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.last = lastRun
	s.stopCh = make(chan struct{})
	s.doneCh = make(chan struct{})
	s.mu.Unlock()

	go s.loop(ctx)
}

// Stop signals the loop to exit and blocks until it returns. Safe to
// call from any goroutine; Stop on a never-started Scheduler is a
// no-op.
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

// RunOnce fires the sweep once and returns the decision. Used by the
// inspector RPC's "prune now" button and by the size-cap trigger.
func (s *Scheduler) RunOnce(ctx context.Context) (Decision, error) {
	dec, err := s.pruner.Apply(ctx, s.scopes...)
	if err != nil {
		return Decision{}, err
	}
	s.mu.Lock()
	s.last = s.now()
	cb := s.onSweep
	s.mu.Unlock()
	if cb != nil {
		cb(dec)
	}
	return dec, nil
}

// LastRun returns the last successful sweep timestamp.
func (s *Scheduler) LastRun() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.doneCh)
	// Catch-up sweep on boot if we're already past due.
	s.mu.Lock()
	overdue := s.last.IsZero() || s.now().Sub(s.last) >= s.interval
	s.mu.Unlock()
	if overdue {
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
