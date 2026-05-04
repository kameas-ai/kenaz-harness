// Concrete Scheduler implementation backed by github.com/robfig/cron/v3.
//
// Timezone handling: robfig/cron/v3 supports per-entry timezone injection
// via the "CRON_TZ=<IANA>" prefix on the spec string. This avoids the
// process-wide WithLocation footgun that would clobber unrelated schedules.
// Example: Register("wf-1", "0 7 * * *", "America/New_York") stores the
// spec as "CRON_TZ=America/New_York 0 7 * * *" and cron parses it with
// the correct location for DST transitions.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// ErrInvalidCronExpr is returned when Register receives an unparseable
// cron expression.
var ErrInvalidCronExpr = errors.New("scheduler: invalid cron expression")

// ErrInvalidTimezone is returned when Register receives an IANA timezone
// name that time.LoadLocation does not recognise.
var ErrInvalidTimezone = errors.New("scheduler: invalid timezone")

// historyLimit caps the in-memory history ring-buffer per workflow.
const historyLimit = 100

// scheduleState is the per-workflow in-memory state.
type scheduleState struct {
	entry  cron.EntryID
	cron   string
	tz     string
	enabled bool
}

// CronScheduler is the production Scheduler backed by robfig/cron.
type CronScheduler struct {
	mu         sync.RWMutex
	c          *cron.Cron
	schedules  map[string]*scheduleState // workflowID → state
	history    map[string][]RunSummary   // workflowID → ring
	store      Storage                   // may be nil in tests
	dispatcher Dispatcher                // may be nil in tests

	// started tracks whether Start() has been called.
	started bool
}

// Config holds the optional wiring for CronScheduler.New.
type Config struct {
	// Store persists schedules across chassis restarts. nil is allowed
	// in tests; schedules are lost on process exit.
	Store Storage
	// Dispatcher fires actual workflow runs. nil is allowed in tests;
	// RunNow and tick-triggered runs return a no-op stub summary.
	Dispatcher Dispatcher
}

// New constructs a CronScheduler. It does NOT start the cron engine;
// call Start() (or the chassis Start hook) before ticks fire.
//
// If cfg.Store is non-nil, New loads existing schedules from the DB and
// pre-registers them so they survive a chassis Stop → New → Start cycle.
func New(ctx context.Context, cfg Config) (*CronScheduler, error) {
	s := &CronScheduler{
		c: cron.New(
			cron.WithSeconds(), // 6-field if user passes seconds; harmless on 5-field specs
			cron.WithParser(cron.NewParser(
				cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow|cron.Descriptor,
			)),
		),
		schedules:  make(map[string]*scheduleState),
		history:    make(map[string][]RunSummary),
		store:      cfg.Store,
		dispatcher: cfg.Dispatcher,
	}
	// Reload from DB at boot.
	if cfg.Store != nil {
		stored, err := cfg.Store.LoadAll(ctx)
		if err != nil {
			return nil, fmt.Errorf("scheduler: load stored schedules: %w", err)
		}
		for _, ss := range stored {
			if !ss.Enabled {
				continue
			}
			if err := s.register(ctx, ss.WorkflowID, ss.Cron, ss.Timezone, false /*skipPersist*/); err != nil {
				// Log but don't abort boot — a bad persisted spec should not
				// prevent the chassis from starting.
				_ = err
				continue
			}
		}
	}
	return s, nil
}

// buildSpec prepends the CRON_TZ prefix when tz is non-empty.
func buildSpec(cronExpr, tz string) (string, error) {
	if tz == "" || tz == "UTC" {
		return cronExpr, nil
	}
	// Validate the IANA name before embedding it.
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidTimezone, tz)
	}
	return "CRON_TZ=" + tz + " " + cronExpr, nil
}

// register is the internal helper that adds an entry to the cron engine.
// When skipPersist is false it writes to the Storage before adding to
// the engine (used by the public Register path).
func (s *CronScheduler) register(ctx context.Context, workflowID, cronExpr, tz string, skipPersist bool) error {
	spec, err := buildSpec(cronExpr, tz)
	if err != nil {
		return err
	}
	// Parse-validate the spec before touching state.
	if _, err := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor).Parse(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCronExpr, err)
	}
	if !skipPersist && s.store != nil {
		if err := s.store.Upsert(ctx, StoredSchedule{
			WorkflowID: workflowID,
			Cron:       cronExpr,
			Timezone:   tz,
			Enabled:    true,
		}); err != nil {
			return fmt.Errorf("scheduler: persist schedule: %w", err)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Remove any existing entry first.
	if existing, ok := s.schedules[workflowID]; ok {
		s.c.Remove(existing.entry)
	}
	entryID, err := s.c.AddFunc(spec, func() {
		s.fire(workflowID, true)
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCronExpr, err)
	}
	s.schedules[workflowID] = &scheduleState{
		entry:   entryID,
		cron:    cronExpr,
		tz:      tz,
		enabled: true,
	}
	return nil
}

// Register implements Scheduler.
func (s *CronScheduler) Register(ctx context.Context, workflowID, cronExpr, tz string) error {
	return s.register(ctx, workflowID, cronExpr, tz, false)
}

// Unregister implements Scheduler.
func (s *CronScheduler) Unregister(ctx context.Context, workflowID string) error {
	if s.store != nil {
		if err := s.store.Delete(ctx, workflowID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.schedules[workflowID]; ok {
		s.c.Remove(st.entry)
		delete(s.schedules, workflowID)
	}
	return nil
}

// RunNow implements Scheduler. Dispatches immediately and returns a
// summary. Blocks until the dispatcher returns.
func (s *CronScheduler) RunNow(ctx context.Context, workflowID string) (RunSummary, error) {
	return s.fireSync(ctx, workflowID, false)
}

// History implements Scheduler.
func (s *CronScheduler) History(_ context.Context, workflowID string, limit int) ([]RunSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h := s.history[workflowID]
	if limit <= 0 || limit > len(h) {
		limit = len(h)
	}
	out := make([]RunSummary, limit)
	copy(out, h[len(h)-limit:])
	return out, nil
}

// List implements Scheduler.
func (s *CronScheduler) List(_ context.Context) ([]ScheduleEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ScheduleEntry, 0, len(s.schedules))
	for id, st := range s.schedules {
		out = append(out, ScheduleEntry{
			WorkflowID: id,
			Cron:       st.cron,
			Timezone:   st.tz,
			Enabled:    st.enabled,
		})
	}
	return out, nil
}

// Start implements Scheduler.
func (s *CronScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.c.Start()
	s.started = true
}

// Stop implements Scheduler.
func (s *CronScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	s.c.Stop()
	s.started = false
}

// Tick is a no-op on the real implementation. The test fake overrides it.
func (s *CronScheduler) Tick(_ time.Time) {}

// fire is the cron callback — runs in the cron goroutine so it must not
// block the cron engine. It dispatches asynchronously.
func (s *CronScheduler) fire(workflowID string, scheduled bool) {
	go func() {
		ctx := context.Background()
		_, _ = s.fireSync(ctx, workflowID, scheduled)
	}()
}

// fireSync dispatches a run and records the summary. Blocks until done.
func (s *CronScheduler) fireSync(ctx context.Context, workflowID string, scheduled bool) (RunSummary, error) {
	startedAt := time.Now().UTC()
	summary := RunSummary{
		RunID:      newRunID(),
		WorkflowID: workflowID,
		Status:     "running",
		StartedAt:  startedAt,
		Scheduled:  scheduled,
	}

	var (
		runID string
		err   error
	)
	if s.dispatcher != nil {
		runID, err = s.dispatcher.Dispatch(ctx, workflowID, scheduled)
	} else {
		// No dispatcher wired (test path): synthesise a run ID.
		runID = summary.RunID
	}

	endedAt := time.Now().UTC()
	if err != nil {
		summary.Status = "failed"
		summary.Err = err.Error()
	} else {
		summary.Status = "completed"
		if runID != "" {
			summary.RunID = runID
		}
	}
	summary.EndedAt = endedAt

	s.mu.Lock()
	h := s.history[workflowID]
	h = append(h, summary)
	if len(h) > historyLimit {
		h = h[len(h)-historyLimit:]
	}
	s.history[workflowID] = h
	s.mu.Unlock()

	if scheduled && s.store != nil && summary.Status == "completed" {
		_ = s.store.SetLastFired(ctx, workflowID, summary.RunID, startedAt)
	}
	return summary, err
}

// newRunID returns a 16-byte random hex string.
func newRunID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var _ Scheduler = (*CronScheduler)(nil)
