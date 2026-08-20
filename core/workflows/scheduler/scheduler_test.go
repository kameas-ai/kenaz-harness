package scheduler_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
)

// --- fakes ---

// fakeDispatcher records Dispatch calls and returns configured outcomes.
type fakeDispatcher struct {
	mu      sync.Mutex
	calls   []dispatchCall
	runErr  error
	nextID  string
}

type dispatchCall struct {
	workflowID string
	scheduled  bool
}

func (f *fakeDispatcher) Dispatch(_ context.Context, workflowID string, scheduled bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dispatchCall{workflowID, scheduled})
	if f.runErr != nil {
		return "", f.runErr
	}
	id := f.nextID
	if id == "" {
		id = "run-" + workflowID
	}
	return id, nil
}

func (f *fakeDispatcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeStorage is an in-memory Storage for tests.
type fakeStorage struct {
	mu        sync.Mutex
	schedules map[string]scheduler.StoredSchedule
	lastFired map[string]lastFiredEntry
}

type lastFiredEntry struct {
	runID   string
	firedAt time.Time
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{
		schedules: make(map[string]scheduler.StoredSchedule),
		lastFired: make(map[string]lastFiredEntry),
	}
}

func (s *fakeStorage) LoadAll(_ context.Context) ([]scheduler.StoredSchedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]scheduler.StoredSchedule, 0, len(s.schedules))
	for _, ss := range s.schedules {
		out = append(out, ss)
	}
	return out, nil
}

func (s *fakeStorage) Upsert(_ context.Context, ss scheduler.StoredSchedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules[ss.WorkflowID] = ss
	return nil
}

func (s *fakeStorage) Delete(_ context.Context, workflowID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.schedules, workflowID)
	return nil
}

func (s *fakeStorage) SetLastFired(_ context.Context, workflowID, runID string, firedAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastFired[workflowID] = lastFiredEntry{runID: runID, firedAt: firedAt}
	return nil
}

var _ scheduler.Storage = (*fakeStorage)(nil)

// --- helpers ---

func newSched(t *testing.T, store scheduler.Storage, disp scheduler.Dispatcher) *scheduler.CronScheduler {
	t.Helper()
	ctx := context.Background()
	cfg := scheduler.Config{
		Store:      store,
		Dispatcher: disp,
	}
	s, err := scheduler.New(ctx, cfg)
	if err != nil {
		t.Fatalf("scheduler.New: %v", err)
	}
	return s
}

// --- tests ---

// TestRegisterAndList verifies that Register persists to storage and
// is reflected in List.
func TestRegisterAndList(t *testing.T) {
	t.Parallel()
	store := newFakeStorage()
	s := newSched(t, store, nil)
	ctx := context.Background()

	if err := s.Register(ctx, "wf-1", "0 7 * * *", "America/New_York"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	entries, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	e := entries[0]
	if e.WorkflowID != "wf-1" {
		t.Errorf("WorkflowID = %q, want wf-1", e.WorkflowID)
	}
	if e.Cron != "0 7 * * *" {
		t.Errorf("Cron = %q, want '0 7 * * *'", e.Cron)
	}
	if e.Timezone != "America/New_York" {
		t.Errorf("Timezone = %q, want America/New_York", e.Timezone)
	}
	if !e.Enabled {
		t.Errorf("Enabled = false, want true")
	}

	// Storage should have the row.
	stored, _ := store.LoadAll(ctx)
	if len(stored) != 1 || stored[0].WorkflowID != "wf-1" {
		t.Errorf("storage does not have wf-1: %+v", stored)
	}
}

// TestUnregister verifies that Unregister removes from both the engine
// and storage.
func TestUnregister(t *testing.T) {
	t.Parallel()
	store := newFakeStorage()
	s := newSched(t, store, nil)
	ctx := context.Background()

	if err := s.Register(ctx, "wf-2", "*/5 * * * *", ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := s.Unregister(ctx, "wf-2"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}

	entries, _ := s.List(ctx)
	if len(entries) != 0 {
		t.Errorf("List after Unregister: got %d entries, want 0", len(entries))
	}
	stored, _ := store.LoadAll(ctx)
	if len(stored) != 0 {
		t.Errorf("storage after Unregister: got %d rows, want 0", len(stored))
	}
}

// TestUnregisterNoSchedule verifies that Unregister returns nil for
// unknown workflows.
func TestUnregisterNoSchedule(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	if err := s.Unregister(context.Background(), "no-such-workflow"); err != nil {
		t.Errorf("Unregister unknown: %v", err)
	}
}

// TestRunNow verifies that RunNow dispatches immediately and records
// the summary in History.
func TestRunNow(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{nextID: "run-abc"}
	s := newSched(t, newFakeStorage(), disp)
	ctx := context.Background()

	sum, err := s.RunNow(ctx, "wf-3")
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if sum.WorkflowID != "wf-3" {
		t.Errorf("WorkflowID = %q, want wf-3", sum.WorkflowID)
	}
	if sum.Status != "completed" {
		t.Errorf("Status = %q, want completed", sum.Status)
	}
	if sum.Scheduled {
		t.Errorf("Scheduled = true for RunNow, want false")
	}
	if sum.RunID != "run-abc" {
		t.Errorf("RunID = %q, want run-abc", sum.RunID)
	}
	if disp.callCount() != 1 {
		t.Errorf("dispatcher.calls = %d, want 1", disp.callCount())
	}

	history, err := s.History(ctx, "wf-3", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("history len = %d, want 1", len(history))
	}
}

// TestRunNow_DispatchError verifies that a dispatcher error is
// surfaced through RunNow.
func TestRunNow_DispatchError(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{runErr: errors.New("boom")}
	s := newSched(t, newFakeStorage(), disp)

	sum, err := s.RunNow(context.Background(), "wf-err")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if sum.Status != "failed" {
		t.Errorf("Status = %q, want failed", sum.Status)
	}
}

// TestRunNow_NilDispatcher_ReturnsErrorAndNoHistoryEntry is AC-002 (UNIT-1,
// FR-001): the human-clicked Run Now path with no dispatcher wired must
// not manufacture a "completed" run, and — because no dispatch was even
// attempted (this is a configuration error, not an attempted-and-failed
// run) — must not appear in History either. Contrast with
// TestRunNow_DispatchError above, where a real Dispatcher was reached and
// failed there: that IS recorded, because a run was genuinely attempted.
func TestRunNow_NilDispatcher_ReturnsErrorAndNoHistoryEntry(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	ctx := context.Background()

	sum, err := s.RunNow(ctx, "wf-no-dispatcher")
	if err == nil {
		t.Fatal("RunNow with nil dispatcher: expected error, got nil")
	}
	if !errors.Is(err, scheduler.ErrNoDispatcherWired) {
		t.Errorf("RunNow error = %v, want ErrNoDispatcherWired", err)
	}
	if sum.Status == "completed" {
		t.Errorf("Status = %q, must not be completed", sum.Status)
	}
	if sum.Err == "" {
		t.Error("summary.Err is empty; must name the missing dispatcher")
	}

	history, err := s.History(ctx, "wf-no-dispatcher", 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("History = %d entries, want 0 (config error, not an attempted run)", len(history))
	}
}

// TestInvalidCronExpr verifies that an unparseable cron string returns
// ErrInvalidCronExpr.
func TestInvalidCronExpr(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	err := s.Register(context.Background(), "wf-bad", "not-a-cron", "")
	if !errors.Is(err, scheduler.ErrInvalidCronExpr) {
		t.Errorf("Register bad expr: got %v, want ErrInvalidCronExpr", err)
	}
}

// TestInvalidTimezone verifies that an unknown IANA timezone name
// returns ErrInvalidTimezone.
func TestInvalidTimezone(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	err := s.Register(context.Background(), "wf-tz", "0 7 * * *", "Not/ATimezone")
	if !errors.Is(err, scheduler.ErrInvalidTimezone) {
		t.Errorf("Register bad tz: got %v, want ErrInvalidTimezone", err)
	}
}

// TestReloadFromDB verifies that schedules survive a Stop → New → Start
// round-trip (the storage re-loads them at New time).
func TestReloadFromDB(t *testing.T) {
	t.Parallel()
	store := newFakeStorage()
	ctx := context.Background()

	// First chassis: register two schedules.
	s1 := newSched(t, store, nil)
	if err := s1.Register(ctx, "wf-reload-a", "0 8 * * *", "UTC"); err != nil {
		t.Fatalf("Register wf-reload-a: %v", err)
	}
	if err := s1.Register(ctx, "wf-reload-b", "30 9 * * *", "Europe/London"); err != nil {
		t.Fatalf("Register wf-reload-b: %v", err)
	}
	s1.Stop()

	// Second chassis: same store, new scheduler instance.
	s2 := newSched(t, store, nil)
	entries, err := s2.List(ctx)
	if err != nil {
		t.Fatalf("List after reload: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List after reload = %d entries, want 2; entries: %+v", len(entries), entries)
	}
	found := make(map[string]bool)
	for _, e := range entries {
		found[e.WorkflowID] = true
	}
	for _, id := range []string{"wf-reload-a", "wf-reload-b"} {
		if !found[id] {
			t.Errorf("reloaded entries missing %q", id)
		}
	}
}

// TestHistoryLimit verifies that History respects the limit argument.
func TestHistoryLimit(t *testing.T) {
	t.Parallel()
	disp := &fakeDispatcher{}
	s := newSched(t, newFakeStorage(), disp)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := s.RunNow(ctx, "wf-hist"); err != nil {
			t.Fatalf("RunNow[%d]: %v", i, err)
		}
	}
	h, err := s.History(ctx, "wf-hist", 3)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h) != 3 {
		t.Errorf("History(limit=3) = %d, want 3", len(h))
	}
}

// TestTickIsNoop verifies that Tick on the production scheduler is a
// harmless no-op (it exists only for test-clock injection on the fake).
func TestTickIsNoop(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	s.Tick(time.Now()) // must not panic
}

// TestStartStop verifies that Start and Stop are idempotent.
func TestStartStop(t *testing.T) {
	t.Parallel()
	s := newSched(t, newFakeStorage(), nil)
	s.Start()
	s.Start() // second call is a no-op
	s.Stop()
	s.Stop() // second call is a no-op
}

// TestRegisterReplacesExisting verifies that re-registering the same
// workflow ID replaces the old schedule.
func TestRegisterReplacesExisting(t *testing.T) {
	t.Parallel()
	store := newFakeStorage()
	s := newSched(t, store, nil)
	ctx := context.Background()

	if err := s.Register(ctx, "wf-replace", "0 6 * * *", "UTC"); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := s.Register(ctx, "wf-replace", "0 10 * * *", "America/Los_Angeles"); err != nil {
		t.Fatalf("second Register: %v", err)
	}

	entries, _ := s.List(ctx)
	if len(entries) != 1 {
		t.Fatalf("List = %d, want 1", len(entries))
	}
	if entries[0].Cron != "0 10 * * *" {
		t.Errorf("Cron = %q, want '0 10 * * *'", entries[0].Cron)
	}
}
