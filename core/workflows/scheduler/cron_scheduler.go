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
	"github.com/kameas-ai/kenaz-harness/core/logging"
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

// ErrNoDispatcherWired is returned by fireSync (via RunNow and the cron
// fire path) when no Dispatcher has been configured. Until a production
// wfsched.Dispatcher exists (workflows-agentic mission's UNIT-2), this is
// the only production state: Config.Dispatcher has no non-test caller.
// A run in this state never happened — no engine was invoked, no run ID
// was produced — so it is recorded as a failure and does not advance
// last_fired_at/last_run_id, and does not appear in History (unlike a run
// that reached a real Dispatcher and failed there).
var ErrNoDispatcherWired = errors.New("no workflow dispatcher wired")

// historyLimit caps the in-memory history ring-buffer per workflow.
const historyLimit = 100

// scheduleState is the per-workflow in-memory state.
type scheduleState struct {
	entry   cron.EntryID
	cron    string
	tz      string
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
	// Dispatcher fires actual workflow runs. As of this writing nil is
	// NOT a supported "test-only" state — it is the only production
	// state, because no production wfsched.Dispatcher is constructed
	// anywhere in the chassis (see core/rpc/api.go's sole `wfsched.New`
	// call site). RunNow and tick-triggered runs on a nil Dispatcher do
	// NOT return a stub "completed" summary: they fail loudly with
	// ErrNoDispatcherWired, and do not advance the persisted
	// last_fired_at/last_run_id. Until a real Dispatcher is wired
	// (workflows-agentic mission's UNIT-2), every enabled schedule
	// reports failure — that is intentional honesty, not a regression
	// to be worked around here.
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

// NextFire implements Scheduler. Returns the next time the cron engine
// will fire for workflowID. Returns a zero Time when no schedule is
// registered for workflowID.
func (s *CronScheduler) NextFire(_ context.Context, workflowID string) (time.Time, error) {
	s.mu.RLock()
	st, ok := s.schedules[workflowID]
	if !ok {
		s.mu.RUnlock()
		return time.Time{}, nil
	}
	entryID := st.entry
	s.mu.RUnlock()
	// cron.Cron.Entry returns a zero-value Entry (with a zero Next) when
	// the id is not found, which is the right sentinel here.
	entry := s.c.Entry(entryID)
	return entry.Next, nil
}

// Tick is a no-op on the real implementation. The test fake overrides it.
func (s *CronScheduler) Tick(_ time.Time) {}

// fire is the cron callback — runs in the cron goroutine so it must not
// block the cron engine. It dispatches asynchronously.
func (s *CronScheduler) fire(workflowID string, scheduled bool) {
	go func() {
		ctx := context.Background()
		// The error is the ONLY signal on this path. A cron tick has no
		// caller to return to, and a nil-dispatcher tick deliberately
		// writes no History entry and does not advance last_fired_at --
		// so without this line the tick is completely silent. Ruling B-6
		// asks this fix to "convert a silent lie into a VISIBLE gap";
		// dropping the error would convert it into a silent one instead.
		if _, err := s.fireSync(ctx, workflowID, scheduled); err != nil {
			logging.L().Warn("wf.scheduler.fire_failed",
				"workflow_id", workflowID,
				"scheduled", scheduled,
				"err", err.Error())
		}
	}()
}

// fireSync dispatches a run and records the summary. Blocks until done.
//
// If no Dispatcher is wired, this is a configuration error, not an
// attempted-and-failed run: no engine was invoked and no run ID was
// produced. It is reported as a failure to the caller, but — unlike a
// run that reached a real Dispatcher and failed there — it is NOT
// appended to History and does NOT advance the durable
// last_fired_at/last_run_id (cron_scheduler.go's whole reason for
// existing is that those two facts must stay honest).
func (s *CronScheduler) fireSync(ctx context.Context, workflowID string, scheduled bool) (RunSummary, error) {
	startedAt := time.Now().UTC()

	if s.dispatcher == nil {
		return RunSummary{
			WorkflowID: workflowID,
			Status:     "failed",
			StartedAt:  startedAt,
			EndedAt:    time.Now().UTC(),
			Err:        ErrNoDispatcherWired.Error(),
			Scheduled:  scheduled,
		}, ErrNoDispatcherWired
	}

	summary := RunSummary{
		RunID:      newRunID(),
		WorkflowID: workflowID,
		Status:     "running",
		StartedAt:  startedAt,
		Scheduled:  scheduled,
	}

	runID, err := s.dispatcher.Dispatch(ctx, workflowID, scheduled)

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

	// Guarded on s.dispatcher != nil as well as summary.Status ==
	// "completed" — deliberately redundant with the early return above.
	// A later refactor of the status assignment above must not be able
	// to silently re-arm this durable write for the nil-dispatcher case.
	if scheduled && s.dispatcher != nil && s.store != nil && summary.Status == "completed" {
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
