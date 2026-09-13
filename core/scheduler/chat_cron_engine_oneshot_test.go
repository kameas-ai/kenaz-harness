package scheduler_test

// One-shot schedule tests (model-scheduled-jobs-01PMSJ01 WP08, FR-006,
// AC-010's engine half — AC-010 itself, "from a real upgrade", lives in
// core/storage/sqlite/scheduled_chat_trigger_upgrade_test.go per CLAUDE.md
// blind spot #3: migration-selection defects are invisible on a fresh
// database, so the upgrade-snapshot proof belongs there. This file drives
// NEW cron-engine logic (arming, firing, disabling) against a fresh
// sqlite DB through the production migration path — real sqlite per
// CLAUDE.md blind spot #2, but not an upgrade-path assertion, matching
// the existing chat_cron_engine_test.go fixtures' own stated rationale.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// waitOnceSettled blocks until id's row reports enabled=false and the
// engine no longer has it registered — i.e. fireOnce has fully finished,
// including its SetEnabled write, not merely sent on the dispatcher's
// fire channel. Callers MUST wait for this before returning (letting a
// test's t.Cleanup close the underlying sqlite DB) — fireOnce's SetEnabled
// call happens on the timer's own goroutine, strictly after the dispatch
// that feeds disp.fire, so racing ahead of it here previously raced a
// live WriteTx against Close() (found by -race during this WP's own
// development, not hypothetical).
func waitOnceSettled(t *testing.T, store scheduler.ScheduledChatStore, engine *scheduler.ChatCronEngine, id string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, gerr := store.Get(ctx, id)
		if gerr != nil {
			t.Fatalf("Get while waiting for settle: %v", gerr)
		}
		if !rec.Enabled && !engine.Registered(id) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to settle; enabled=%v registered=%v", id, rec.Enabled, engine.Registered(id))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// mustCreateOnceRow seeds a trigger_kind='once' row with the given run_at.
func mustCreateOnceRow(t *testing.T, store scheduler.ScheduledChatStore, id string, runAt time.Time, enabled bool) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             id,
		Name:           "once-" + id,
		PromptTemplate: "Hello {{date}}",
		Cron:           "", // one-shot rows carry no cron expression (FR-006)
		OutputSink:     "none",
		Enabled:        enabled,
		TriggerKind:    scheduler.TriggerKindOnce,
		RunAt:          &runAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed once row %s: %v", id, err)
	}
}

// TestChatCronEngine_OnceFiresExactlyOnceAndDisables is AC-010's engine
// half: a trigger_kind='once' row with a near-future run_at fires exactly
// once, writes history, and the row reports enabled=false afterward.
//
// Mutation coverage: if fireOnce's SetEnabled(false) call were deleted,
// the "enabled=false" assertion below fails; if disarmLocked's `case
// TriggerKindOnce` arm were deleted (falling through to `e.c.Remove` on
// a zero-value cron.EntryID, a no-op) unregister would still work by
// accident, so the real proof-of-life is the second half: waiting past
// the fire and asserting the dispatcher was called exactly once, not
// twice — that fails if fireOnce re-armed the timer instead of dropping
// the entry.
func TestChatCronEngine_OnceFiresExactlyOnceAndDisables(t *testing.T) {
	store := openTestChatStore(t)
	runAt := time.Now().Add(150 * time.Millisecond)
	mustCreateOnceRow(t, store, "once-fire", runAt, true)

	disp := newStubDispatcher()
	ctx := context.Background()
	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	if !engine.Registered("once-fire") {
		t.Fatal("a trigger_kind='once' row with a future run_at was not registered at boot")
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	select {
	case id := <-disp.fire:
		if id != "once-fire" {
			t.Errorf("fired id=%q, want once-fire", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the one-shot timer to fire")
	}

	// enabled=false and the entry is dropped — poll briefly since fireOnce
	// runs its SetEnabled/unregister calls after fireSync returns, on the
	// timer's own goroutine, not synchronously with the fire channel send.
	waitOnceSettled(t, store, engine, "once-fire")

	hist, herr := store.History(ctx, "once-fire", 10)
	if herr != nil {
		t.Fatalf("History: %v", herr)
	}
	if len(hist) != 1 {
		t.Fatalf("history rows = %d, want exactly 1", len(hist))
	}
	if hist[0].Status != "completed" {
		t.Errorf("history status=%q, want completed", hist[0].Status)
	}

	// Prove it does not fire a second time: wait past another possible
	// tick window and confirm the dispatcher was called exactly once.
	time.Sleep(300 * time.Millisecond)
	if calls := disp.snapshot(); len(calls) != 1 {
		t.Fatalf("dispatcher calls = %v, want exactly 1 (a one-shot row fired more than once)", calls)
	}
}

// TestChatCronEngine_OnceInThePastFiresOnBootNotSkipped: a trigger_kind=
// 'once' row whose run_at already passed (the app was closed at the
// scheduled time) still fires on the next boot rather than silently
// never firing — spec.md's "run this once at T" promise would otherwise
// break for the exact case a user most cares about missing.
func TestChatCronEngine_OnceInThePastFiresOnBootNotSkipped(t *testing.T) {
	store := openTestChatStore(t)
	past := time.Now().Add(-1 * time.Hour)
	mustCreateOnceRow(t, store, "once-past", past, true)

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	select {
	case id := <-disp.fire:
		if id != "once-past" {
			t.Errorf("fired id=%q, want once-past", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a past-due one-shot row to fire on boot")
	}
	// Wait for fireOnce's SetEnabled write to complete before this test
	// returns and its t.Cleanup closes the underlying sqlite DB out from
	// under that still-in-flight goroutine (see waitOnceSettled's doc).
	waitOnceSettled(t, store, engine, "once-past")
}

// TestChatCronEngine_OnceMissingRunAtSkipsBootWithoutCrashing: a
// malformed trigger_kind='once' row (RunAt nil — should not occur
// through the API layer, which requires RunAt for 'once', but a
// hand-edited database can produce it) is logged and skipped, exactly
// like a malformed cron expression — one bad row must not prevent every
// other schedule (and the rest of chassis boot) from coming up.
func TestChatCronEngine_OnceMissingRunAtSkipsBootWithoutCrashing(t *testing.T) {
	store := openTestChatStore(t)
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             "once-missing-runat",
		Name:           "malformed",
		PromptTemplate: "x",
		OutputSink:     "none",
		Enabled:        true,
		TriggerKind:    scheduler.TriggerKindOnce,
		RunAt:          nil,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed malformed once row: %v", err)
	}

	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{Store: store})
	if err != nil {
		t.Fatalf("NewChatCronEngine must not fail boot on one bad row: %v", err)
	}
	if engine.Registered("once-missing-runat") {
		t.Fatal("a trigger_kind='once' row with nil run_at must not register")
	}
}

// TestChatCronEngine_CronRowStillFiresRepeatedlyWithOneShotSupportPresent
// is the mutation guard for the shared registerRecord dispatch: a plain
// trigger_kind='cron' row (the zero-value / pre-migration shape) must
// keep firing on every tick, not just once, now that a `once` branch
// exists alongside it. Fails if registerRecord's default case were
// changed to call registerOnce, or if disarmLocked's default (cron) arm
// were broken.
func TestChatCronEngine_CronRowStillFiresRepeatedlyWithOneShotSupportPresent(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-still-repeats", "* * * * * *", true)

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	seen := 0
	deadline := time.After(3 * time.Second)
	for seen < 2 {
		select {
		case <-disp.fire:
			seen++
		case <-deadline:
			t.Fatalf("only saw %d fire(s) in 3s, want at least 2 (a recurring cron row must not stop after one fire)", seen)
		}
	}
	if !engine.Registered("cr-still-repeats") {
		t.Error("a recurring cron row must remain registered after firing")
	}
}
