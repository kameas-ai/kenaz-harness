package scheduler

// White-box tests for DispatcherFunc (automation-actually-runs-01PMZ404
// UNIT-2). Config.DispatcherFunc is the production wiring shape — see its
// doc comment on scheduler.go — because the chassis constructs
// CronScheduler before the workflowsview.API it needs to dispatch through
// exists. These tests pin that DispatcherFunc is consulted on every fire,
// takes priority over a plain Dispatcher when both are set, and that a
// real sqlite last_fired_at advance happens through it exactly as it does
// through the plain Dispatcher field (fire_test.go's existing coverage).

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// countingDispatcherFunc is a race-safe fake DispatcherFunc: each call
// returns a fresh stubDispatcher-shaped Dispatcher and records how many
// times it was invoked, so tests can tell "resolved per fire" from
// "resolved once at construction".
type countingDispatcherFunc struct {
	mu       sync.Mutex
	resolves int
	runID    string
}

func (c *countingDispatcherFunc) resolve() Dispatcher {
	c.mu.Lock()
	c.resolves++
	c.mu.Unlock()
	return &stubDispatcher{runID: c.runID}
}

func (c *countingDispatcherFunc) resolveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resolves
}

// TestFireSync_DispatcherFunc_ResolvedOnEveryFire is UNIT-2's core claim:
// DispatcherFunc, not a snapshot taken at New() time, decides the
// Dispatcher for each fire.
func TestFireSync_DispatcherFunc_ResolvedOnEveryFire(t *testing.T) {
	t.Parallel()
	db := openRealDB(t)
	workflowID := seedWorkflow(t, db)

	store := NewSQLiteStorage(db)
	ctx := context.Background()
	if err := store.Upsert(ctx, StoredSchedule{
		WorkflowID: workflowID,
		Cron:       "0 7 * * *",
		Timezone:   "UTC",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	cf := &countingDispatcherFunc{runID: "run-func-123"}
	s, err := New(ctx, Config{Store: store, DispatcherFunc: cf.resolve})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, fireErr := s.fireSync(ctx, workflowID, true); fireErr != nil {
		t.Fatalf("fireSync #1: %v", fireErr)
	}
	if _, fireErr := s.fireSync(ctx, workflowID, false); fireErr != nil {
		t.Fatalf("fireSync #2: %v", fireErr)
	}

	if got := cf.resolveCount(); got != 2 {
		t.Errorf("DispatcherFunc resolved %d times across 2 fires, want 2 — "+
			"it must be called fresh per fire, not cached at construction", got)
	}

	after := selectScheduleRow(t, db, workflowID)
	if after.lastFiredAt == nil {
		t.Fatal("last_fired_at did not advance with DispatcherFunc wired")
	}
	if after.lastRunID == nil || *after.lastRunID != "run-func-123" {
		t.Errorf("last_run_id = %v, want run-func-123", after.lastRunID)
	}
}

// TestFireSync_DispatcherFunc_TakesPriorityOverDispatcher pins the
// documented priority: when both Dispatcher and DispatcherFunc are set,
// DispatcherFunc wins. Config's own doc comment makes this claim; this
// test is what falsifies it if a future refactor swaps the priority.
func TestFireSync_DispatcherFunc_TakesPriorityOverDispatcher(t *testing.T) {
	t.Parallel()
	db := openRealDB(t)
	workflowID := seedWorkflow(t, db)

	store := NewSQLiteStorage(db)
	ctx := context.Background()
	if err := store.Upsert(ctx, StoredSchedule{
		WorkflowID: workflowID,
		Cron:       "0 7 * * *",
		Timezone:   "UTC",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	fieldDisp := &stubDispatcher{runID: "run-from-field"}
	funcDisp := &stubDispatcher{runID: "run-from-func"}
	s, err := New(ctx, Config{
		Store:          store,
		Dispatcher:     fieldDisp,
		DispatcherFunc: func() Dispatcher { return funcDisp },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	summary, fireErr := s.fireSync(ctx, workflowID, true)
	if fireErr != nil {
		t.Fatalf("fireSync: %v", fireErr)
	}
	if summary.RunID != "run-from-func" {
		t.Errorf("RunID = %q, want run-from-func (DispatcherFunc should win over Dispatcher)", summary.RunID)
	}
	if fieldDisp.callCount() != 0 {
		t.Errorf("Dispatcher field was called %d times; DispatcherFunc should have been used exclusively", fieldDisp.callCount())
	}
	if funcDisp.callCount() != 1 {
		t.Errorf("Dispatcher returned by DispatcherFunc was called %d times, want 1", funcDisp.callCount())
	}
}

// TestFireSync_DispatcherFunc_NilResolution_IsHonestFailure: a
// DispatcherFunc that resolves to nil (e.g. the workflows subsystem
// booted disabled) must behave exactly like an unset Dispatcher — fail
// with ErrNoDispatcherWired, not panic and not fabricate success.
func TestFireSync_DispatcherFunc_NilResolution_IsHonestFailure(t *testing.T) {
	t.Parallel()
	db := openRealDB(t)
	workflowID := seedWorkflow(t, db)

	store := NewSQLiteStorage(db)
	ctx := context.Background()
	if err := store.Upsert(ctx, StoredSchedule{
		WorkflowID: workflowID,
		Cron:       "0 7 * * *",
		Timezone:   "UTC",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	s, err := New(ctx, Config{
		Store:          store,
		DispatcherFunc: func() Dispatcher { return nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	summary, fireErr := s.fireSync(ctx, workflowID, true)
	if !errors.Is(fireErr, ErrNoDispatcherWired) {
		t.Fatalf("fireSync error = %v, want ErrNoDispatcherWired", fireErr)
	}
	if summary.Status == "completed" {
		t.Fatalf("summary.Status = %q, must not be completed when DispatcherFunc resolves nil", summary.Status)
	}

	after := selectScheduleRow(t, db, workflowID)
	if after.lastFiredAt != nil {
		t.Errorf("last_fired_at advanced to %v with a nil-resolving DispatcherFunc", *after.lastFiredAt)
	}
}
