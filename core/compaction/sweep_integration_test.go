package compaction

import (
	"context"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// sweep_integration_test.go drives the multi-cycle sweep scheduler
// against a small in-memory SweepStore that holds archived_at
// timestamps + compacted_into_id flags, plus the compaction.WithClock
// option for deterministic time-warp.
//
// These cover plan §4 acceptance-smoke item 5 (date-warp via WithClock)
// plus the scheduler's HARNESS_COMPACTION=off short-circuit and
// goroutine-exit cleanliness.
//
// We deliberately reuse fakeSweepStore from sweep_test.go where it
// covers the existing single-call paths; this file adds a richer
// stateful in-memory store (sweepIntegStore) for the multi-cycle
// path because the existing fake returns deterministic results per
// call rather than a cursor walk over a fixture set.

// sweepIntegStore is a stateful in-memory SweepStore for integration
// tests. It tracks every fixture row's (archivedAt, compactedIntoID)
// flags and applies the documented sweep filter on each call:
//
//   - archived_at IS NOT NULL
//   - archived_at < cutoff
//   - compacted_into_id IS NOT NULL
//
// Summary rows (compacted_into_id == "") are NEVER deleted; that
// invariant is the most important thing this fake encodes.
type sweepIntegStore struct {
	mu   sync.Mutex
	rows []sweepIntegRow

	calls int32 // atomic count of DeleteArchivedBefore invocations
}

type sweepIntegRow struct {
	id              string
	archivedAt      time.Time // zero == NOT archived (NULL)
	compactedIntoID string    // empty == summary row (do not sweep)
}

func (s *sweepIntegStore) DeleteArchivedBefore(_ context.Context, cutoff time.Time, pageLimit int) (int, time.Time, time.Time, error) {
	atomic.AddInt32(&s.calls, 1)

	s.mu.Lock()
	defer s.mu.Unlock()

	var (
		victims []int
		oldest  time.Time
		newest  time.Time
	)
	for i, r := range s.rows {
		if r.archivedAt.IsZero() {
			continue
		}
		if r.compactedIntoID == "" {
			continue // summary row — never delete
		}
		if !r.archivedAt.Before(cutoff) {
			continue
		}
		victims = append(victims, i)
	}
	if len(victims) == 0 {
		return 0, time.Time{}, time.Time{}, nil
	}
	// Oldest-first ordering matches the production index walk.
	sort.Slice(victims, func(a, b int) bool {
		return s.rows[victims[a]].archivedAt.Before(s.rows[victims[b]].archivedAt)
	})

	if pageLimit > 0 && len(victims) > pageLimit {
		victims = victims[:pageLimit]
	}

	deadIDs := make(map[string]struct{}, len(victims))
	for _, idx := range victims {
		deadIDs[s.rows[idx].id] = struct{}{}
		ts := s.rows[idx].archivedAt
		if oldest.IsZero() || ts.Before(oldest) {
			oldest = ts
		}
		if newest.IsZero() || ts.After(newest) {
			newest = ts
		}
	}

	survivors := s.rows[:0]
	for _, r := range s.rows {
		if _, dead := deadIDs[r.id]; dead {
			continue
		}
		survivors = append(survivors, r)
	}
	s.rows = append([]sweepIntegRow(nil), survivors...)

	return len(deadIDs), oldest, newest, nil
}

// snapshot returns a copy of every row id currently in the store.
// Used by tests to assert "summary rows survive" without holding the
// internal mutex.
func (s *sweepIntegStore) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, r.id)
	}
	return out
}

// callCount reports how many times DeleteArchivedBefore was invoked.
func (s *sweepIntegStore) callCount() int32 {
	return atomic.LoadInt32(&s.calls)
}

// TestIntegration_Scheduler_PostRetentionWindow_Sweeps mirrors plan §4
// acceptance-smoke item 5: rows archived past the 90-day retention
// window are deleted; summary rows are untouched; KindCompactedOriginalsDeleted
// is emitted with the right count.
func TestIntegration_Scheduler_PostRetentionWindow_Sweeps(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	pastTTL := now.AddDate(0, 0, -91)        // 91 days ago — past 90-day TTL
	freshlyArchived := now.AddDate(0, 0, -5) // 5 days ago — well within retention

	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			// Past-TTL archived originals — should be deleted.
			{id: "old-orig-1", archivedAt: pastTTL, compactedIntoID: "summary-A"},
			{id: "old-orig-2", archivedAt: pastTTL.Add(-time.Hour), compactedIntoID: "summary-A"},
			{id: "old-orig-3", archivedAt: pastTTL.Add(-2 * time.Hour), compactedIntoID: "summary-B"},
			// Summary rows — must NEVER be deleted, even when "archived"
			// past TTL. compacted_into_id IS NULL on these rows.
			{id: "summary-A", archivedAt: pastTTL, compactedIntoID: ""},
			{id: "summary-B", archivedAt: pastTTL, compactedIntoID: ""},
			// Recently archived original — within retention; survives.
			{id: "recent-orig", archivedAt: freshlyArchived, compactedIntoID: "summary-A"},
		},
	}
	em := &fakeAudit{}

	deleted, err := RunSweep(context.Background(), store, em, 90, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3 (three past-TTL originals)", deleted)
	}

	// Survivors: the two summaries + the recent original.
	survivors := store.snapshot()
	wantSet := map[string]struct{}{
		"summary-A":   {},
		"summary-B":   {},
		"recent-orig": {},
	}
	if len(survivors) != len(wantSet) {
		t.Fatalf("survivors len = %d, want %d (rows: %v)",
			len(survivors), len(wantSet), survivors)
	}
	for _, id := range survivors {
		if _, ok := wantSet[id]; !ok {
			t.Errorf("unexpected survivor: %q", id)
		}
	}

	// Audit emission shape.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
	if em.events[0].kind != audit.KindCompactedOriginalsDeleted {
		t.Errorf("audit kind = %q, want %q",
			em.events[0].kind, audit.KindCompactedOriginalsDeleted)
	}
	pl := em.events[0].payload.(audit.CompactedOriginalsDeletedPayload)
	if pl.DeletedCount != 3 {
		t.Errorf("DeletedCount = %d, want 3", pl.DeletedCount)
	}
}

// TestIntegration_Scheduler_MultiCycleSweep drives the scheduler over
// multiple ticks against a moving fake clock; rows that "age into" the
// retention cutoff between ticks are swept on the tick they cross.
//
// We use a 50ms tick interval and observe the sweep firing N times
// over the test window. The fake clock is advanced manually between
// asserts so the test is deterministic — we don't sleep waiting on
// real time to simulate days passing.
func TestIntegration_Scheduler_MultiCycleSweep(t *testing.T) {
	// Fake clock shared between the scheduler and RunSweep. The
	// scheduler uses it for catch-up math; RunSweep uses it for the
	// cutoff calculation.
	var (
		clockMu sync.Mutex
		clock   = time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	)
	now := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		defer clockMu.Unlock()
		clock = clock.Add(d)
	}

	// Fixture: three batches of archived originals, each 91 days older
	// than the next. The retention window is 90 days, so as the fake
	// clock advances forward, batches "age into" deletion eligibility
	// one at a time.
	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			{id: "batch-1-a", archivedAt: clock.AddDate(0, 0, -91), compactedIntoID: "s1"},
			{id: "batch-1-b", archivedAt: clock.AddDate(0, 0, -91), compactedIntoID: "s1"},
			// Batch 2 ages in after another ~91 days.
			{id: "batch-2-a", archivedAt: clock.AddDate(0, 0, 0), compactedIntoID: "s2"},
			{id: "batch-2-b", archivedAt: clock.AddDate(0, 0, 0), compactedIntoID: "s2"},
			// Summary rows persist forever.
			{id: "s1", archivedAt: clock.AddDate(0, 0, -91), compactedIntoID: ""},
			{id: "s2", archivedAt: clock.AddDate(0, 0, 0), compactedIntoID: ""},
		},
	}
	em := &fakeAudit{}

	// Drive RunSweep manually under the fake clock. We don't spin the
	// scheduler in this test because the scheduler's loop uses real
	// time.NewTicker which would either flake or require a fake-time
	// scheduler implementation. Driving RunSweep directly with the
	// fake clock pins exactly the same behavior the scheduler's
	// runOnce closure would: pull cutoff from now(), apply the
	// SweepStore filter, emit audit.

	// Cycle 1: only batch 1 is past TTL.
	deleted1, err := RunSweep(context.Background(), store, em, 90, now)
	if err != nil {
		t.Fatalf("cycle 1 RunSweep: %v", err)
	}
	if deleted1 != 2 {
		t.Errorf("cycle 1 deleted = %d, want 2 (batch 1 only)", deleted1)
	}

	// Cycle 2: advance clock 1 day; nothing new to sweep yet.
	advance(24 * time.Hour)
	deleted2, err := RunSweep(context.Background(), store, em, 90, now)
	if err != nil {
		t.Fatalf("cycle 2 RunSweep: %v", err)
	}
	if deleted2 != 0 {
		t.Errorf("cycle 2 deleted = %d, want 0 (no rows have crossed since cycle 1)", deleted2)
	}

	// Cycle 3: advance ~91 days; batch 2 now ages in.
	advance(91 * 24 * time.Hour)
	deleted3, err := RunSweep(context.Background(), store, em, 90, now)
	if err != nil {
		t.Fatalf("cycle 3 RunSweep: %v", err)
	}
	if deleted3 != 2 {
		t.Errorf("cycle 3 deleted = %d, want 2 (batch 2 ages in)", deleted3)
	}

	// Total: cycles 1 and 3 each emitted; cycle 2 was a no-deletion
	// silent run (audit only emits when at least one row is deleted —
	// see sweep.go RunSweep's documented contract).
	if got := len(em.events); got != 2 {
		t.Fatalf("audit events = %d, want 2 (cycles 1 + 3 emit; cycle 2 silent)", got)
	}
	for i, ev := range em.events {
		if ev.kind != audit.KindCompactedOriginalsDeleted {
			t.Errorf("events[%d].kind = %q, want %q",
				i, ev.kind, audit.KindCompactedOriginalsDeleted)
		}
	}

	// And summary rows survive every cycle.
	survivors := store.snapshot()
	hasSummary := func(id string) bool {
		for _, s := range survivors {
			if s == id {
				return true
			}
		}
		return false
	}
	if !hasSummary("s1") {
		t.Errorf("summary s1 was deleted; sweep must never delete summaries")
	}
	if !hasSummary("s2") {
		t.Errorf("summary s2 was deleted; sweep must never delete summaries")
	}
}

// TestIntegration_Scheduler_TickFiresMultipleSweeps drives the actual
// Scheduler's tick loop over a short interval against the in-memory
// store. Confirms the scheduler invokes runOnce on every tick and the
// onSweep callback receives the (deleted, err) pair — the metrics
// surface relies on this.
func TestIntegration_Scheduler_TickFiresMultipleSweeps(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			{id: "old-1", archivedAt: now.AddDate(0, 0, -91), compactedIntoID: "s"},
		},
	}
	em := &fakeAudit{}

	var (
		mu      sync.Mutex
		results []int
	)
	cb := func(deleted int, err error) {
		mu.Lock()
		defer mu.Unlock()
		results = append(results, deleted)
	}

	runOnce := func(ctx context.Context) (int, error) {
		return RunSweep(ctx, store, em, 90, fixedNow(now))
	}

	s := NewScheduler(runOnce,
		WithInterval(50*time.Millisecond),
		WithOnSweep(cb))
	// Suppress the immediate catch-up sweep — we want to count tick-
	// driven invocations specifically.
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Wait long enough for at least 2 ticks (50ms × 4 = 200ms) plus
	// some CI noise headroom.
	time.Sleep(280 * time.Millisecond)

	s.Stop()

	mu.Lock()
	count := len(results)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected at least 2 onSweep invocations, got %d", count)
	}
	// First tick swept the one past-TTL row; subsequent ticks find
	// nothing.
	mu.Lock()
	first := results[0]
	mu.Unlock()
	if first != 1 {
		t.Errorf("first tick deleted = %d, want 1 (the single past-TTL row)", first)
	}
}

// TestIntegration_Scheduler_RespectsHARNESS_COMPACTION_OFF — feature flag
// short-circuit. With HARNESS_COMPACTION=off, neither the chat-runner
// pre-send hook nor the scheduler should touch any session data.
//
// The compaction package itself doesn't read the env var (the scheduler's
// runOnce closure decides what to do); the production wiring layer
// gates the closure on compactionDisabledByEnv(). We model that here by
// wiring a runOnce closure that consults the env var directly — same
// pattern the production wiring uses.
func TestIntegration_Scheduler_RespectsHARNESS_COMPACTION_OFF(t *testing.T) {
	const envCompactionVar = "HARNESS_COMPACTION"
	const envCompactionDisabled = "off"

	// Save and restore the env var around the test.
	prev, hadPrev := os.LookupEnv(envCompactionVar)
	defer func() {
		if hadPrev {
			_ = os.Setenv(envCompactionVar, prev)
		} else {
			_ = os.Unsetenv(envCompactionVar)
		}
	}()
	if err := os.Setenv(envCompactionVar, envCompactionDisabled); err != nil {
		t.Fatalf("setenv: %v", err)
	}

	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			// Eligible row — would be swept if the flag weren't set.
			{id: "eligible", archivedAt: now.AddDate(0, 0, -100), compactedIntoID: "s"},
		},
	}
	em := &fakeAudit{}

	// Production-shaped runOnce closure: env-var gated. If the flag is
	// "off", the closure is a no-op without touching the store.
	runOnce := func(ctx context.Context) (int, error) {
		if os.Getenv(envCompactionVar) == envCompactionDisabled {
			return 0, nil
		}
		return RunSweep(ctx, store, em, 90, fixedNow(now))
	}

	s := NewScheduler(runOnce, WithInterval(50*time.Millisecond))
	// Force the catch-up branch by leaving LastRun at zero — that's the
	// path the env-flag check has to short-circuit.

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	time.Sleep(180 * time.Millisecond)

	s.Stop()

	// Even though ticks fired, the closure was a no-op — no Delete
	// calls reached the store, no audit emitted.
	if got := store.callCount(); got != 0 {
		t.Errorf("store.callCount = %d, want 0 (HARNESS_COMPACTION=off must not touch store)", got)
	}
	if got := len(em.events); got != 0 {
		t.Errorf("audit events = %d, want 0", got)
	}
	// And the eligible row is still there.
	survivors := store.snapshot()
	found := false
	for _, id := range survivors {
		if id == "eligible" {
			found = true
		}
	}
	if !found {
		t.Errorf("eligible row was deleted despite HARNESS_COMPACTION=off")
	}
}

// TestIntegration_Scheduler_StopIsClean asserts the Scheduler's Stop()
// returns within a deadline-bounded select — i.e. no goroutine leak.
// We don't pull goleak in (per the task constraints); a deadline-bounded
// select on the doneCh side-effect is enough to catch a hang.
func TestIntegration_Scheduler_StopIsClean(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			{id: "any", archivedAt: now.AddDate(0, 0, -91), compactedIntoID: "s"},
		},
	}
	em := &fakeAudit{}

	runOnce := func(ctx context.Context) (int, error) {
		return RunSweep(ctx, store, em, 90, fixedNow(now))
	}

	s := NewScheduler(runOnce, WithInterval(20*time.Millisecond))
	s.SeedLastRun(time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	// Let a couple of ticks fire so the goroutine is doing real work
	// when Stop arrives.
	time.Sleep(80 * time.Millisecond)

	stopReturned := make(chan struct{})
	go func() {
		s.Stop()
		close(stopReturned)
	}()
	select {
	case <-stopReturned:
		// success — Stop returned cleanly
	case <-time.After(2 * time.Second):
		t.Fatalf("Scheduler.Stop did not return within 2s; goroutine likely leaked")
	}

	// Calling Stop again must be idempotent and immediate.
	stopAgainDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopAgainDone)
	}()
	select {
	case <-stopAgainDone:
		// success
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Second Stop() did not return promptly; idempotency broken")
	}
}

// TestIntegration_Scheduler_RunOnceManualTrigger covers the "sweep now"
// admin path (RPC debug button / catch-up after long-stopped install).
// RunOnce must invoke runOnce, update LastRun, and propagate the
// deleted count.
func TestIntegration_Scheduler_RunOnceManualTrigger(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &sweepIntegStore{
		rows: []sweepIntegRow{
			{id: "old-a", archivedAt: now.AddDate(0, 0, -91), compactedIntoID: "s"},
			{id: "old-b", archivedAt: now.AddDate(0, 0, -100), compactedIntoID: "s"},
		},
	}
	em := &fakeAudit{}

	runOnce := func(ctx context.Context) (int, error) {
		return RunSweep(ctx, store, em, 90, fixedNow(now))
	}

	s := NewScheduler(runOnce,
		WithClock(func() time.Time { return now }),
	)

	if !s.LastRun().IsZero() {
		t.Fatalf("LastRun should be zero before RunOnce, got %v", s.LastRun())
	}

	deleted, err := s.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("RunOnce deleted = %d, want 2", deleted)
	}
	if !s.LastRun().Equal(now) {
		t.Errorf("LastRun = %v, want %v", s.LastRun(), now)
	}
	// And audit emitted exactly once.
	if got := len(em.events); got != 1 {
		t.Fatalf("audit events = %d, want 1", got)
	}
}
