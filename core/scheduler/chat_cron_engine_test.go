package scheduler_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

// openTestChatStore boots a fresh, empty sqlite DB through the production
// migration path and returns a real ScheduledChatStore. Per CLAUDE.md
// blind spot #2, engine behaviour (arming, firing, history persistence)
// must be asserted against real sqlite, not session.NewMemoryStore() or an
// in-memory map — a fixture that bypasses SQL encode/decode has hidden
// four separate production defects in this codebase before.
//
// This is NOT an upgrade-path test (WP-PI AC-PI-1): it exercises new
// cron-engine logic, not migration selection or schema evolution across a
// previously-shipped schema, so booting a fresh DB through the current
// migration set is the correct fixture — see the mission's WP-PI report.
func openTestChatStore(t *testing.T) scheduler.ScheduledChatStore {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return scheduler.NewSQLiteChatStore(db)
}

// stubDispatcher is a race-safe ChatRunDispatcher fake (CLAUDE.md: the
// engine drives it from a cron goroutine, so every write needs a mutex and
// test-side reads must go through a snapshot).
type stubDispatcher struct {
	mu    sync.Mutex
	calls []string // chat run ids dispatched, in order
	fire  chan string
}

func newStubDispatcher() *stubDispatcher {
	return &stubDispatcher{fire: make(chan string, 8)}
}

func (s *stubDispatcher) DispatchChatRun(_ context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
	id := ""
	if job.ChatRun != nil {
		id = job.ChatRun.ID
	}
	s.mu.Lock()
	s.calls = append(s.calls, id)
	s.mu.Unlock()
	ended := now
	rec := scheduler.ChatRunHistoryRecord{
		ChatRunID:     id,
		SessionID:     "sess-" + id,
		Status:        "completed",
		StartedAt:     now,
		EndedAt:       &ended,
		OutputSnippet: "stub dispatch ok",
	}
	select {
	case s.fire <- id:
	default:
	}
	return rec, nil
}

func (s *stubDispatcher) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

var _ scheduler.ChatRunDispatcher = (*stubDispatcher)(nil)

func mustCreateRow(t *testing.T, store scheduler.ScheduledChatStore, id, cronExpr string, enabled bool) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             id,
		Name:           "test-" + id,
		PromptTemplate: "Hello {{date}}",
		Cron:           cronExpr,
		OutputSink:     "none",
		Enabled:        enabled,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed row %s: %v", id, err)
	}
}

// TestChatCronEngine_RegisterAndFire is AC-003's "first half" companion:
// a row with enabled=1 and a 1-second cron registers at boot and fires.
// (The engine uses seconds-resolution cron so the test does not need to
// wait a full wall-clock minute.)
func TestChatCronEngine_RegisterAndFire(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-fire", "* * * * * *", true)

	disp := newStubDispatcher()
	ctx := context.Background()
	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
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
		if id != "cr-fire" {
			t.Errorf("fired id=%q, want cr-fire", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for cron tick to fire")
	}

	// The dispatch outcome must be persisted, not just observed in-memory.
	deadline := time.Now().Add(2 * time.Second)
	for {
		hist, herr := store.History(ctx, "cr-fire", 10)
		if herr != nil {
			t.Fatalf("History: %v", herr)
		}
		if len(hist) > 0 {
			if hist[0].Status != "completed" {
				t.Errorf("history status=%q, want completed", hist[0].Status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for history row to persist")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestChatCronEngine_DisabledRowNotRegistered: enabled=0 rows do not
// register at boot.
func TestChatCronEngine_DisabledRowNotRegistered(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-disabled", "* * * * * *", false)

	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	if engine.Registered("cr-disabled") {
		t.Fatal("disabled row was registered at boot")
	}
}

// TestChatCronEngine_SetEnabledArmsWithoutRestart: SetEnabled(true) arms
// via Sync without restarting the engine; SetEnabled(false) disarms it.
func TestChatCronEngine_SetEnabledArmsWithoutRestart(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-toggle", "* * * * * *", false)
	ctx := context.Background()

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	if engine.Registered("cr-toggle") {
		t.Fatal("cr-toggle should not be registered before SetEnabled(true)")
	}

	if err := store.SetEnabled(ctx, "cr-toggle", true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if err := engine.Sync(ctx, "cr-toggle"); err != nil {
		t.Fatalf("Sync after enable: %v", err)
	}

	select {
	case id := <-disp.fire:
		if id != "cr-toggle" {
			t.Errorf("fired id=%q, want cr-toggle", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for tick after SetEnabled(true) — arming without restart failed")
	}

	if err := store.SetEnabled(ctx, "cr-toggle", false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if err := engine.Sync(ctx, "cr-toggle"); err != nil {
		t.Fatalf("Sync after disable: %v", err)
	}
	if engine.Registered("cr-toggle") {
		t.Fatal("cr-toggle still registered after SetEnabled(false)")
	}
}

// TestChatCronEngine_DeleteRemovesEntry: Delete (Unregister) removes the
// cron entry; no further fires occur.
func TestChatCronEngine_DeleteRemovesEntry(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-delete", "* * * * * *", true)
	ctx := context.Background()

	disp := newStubDispatcher()
	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	// Wait for at least one fire so we know it was really armed.
	select {
	case <-disp.fire:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for initial fire before delete")
	}

	if err := store.Delete(ctx, "cr-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := engine.Unregister(ctx, "cr-delete"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if engine.Registered("cr-delete") {
		t.Fatal("cr-delete still registered after Unregister")
	}

	// Drain any in-flight fire that raced the unregister, then assert no
	// further fire arrives within a window that would have seen several
	// more ticks at a 1-second cadence.
	drainOne(disp.fire)
	select {
	case id := <-disp.fire:
		t.Fatalf("fired after delete: %q", id)
	case <-time.After(2500 * time.Millisecond):
		// expected: no further fires.
	}
}

// TestChatCronEngine_MalformedCronDoesNotAbortBoot: a bad cron expression
// on one row is logged and skipped, not fatal to boot; other rows still
// register.
func TestChatCronEngine_MalformedCronDoesNotAbortBoot(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-bad", "not a cron expression", true)
	mustCreateRow(t, store, "cr-good", "* * * * * *", true)

	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{
		Store: store,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine returned error for a boot-time malformed cron row; want nil (skip, not abort): %v", err)
	}
	if !engine.Registered("cr-good") {
		t.Fatal("cr-good was not registered even though only cr-bad was malformed")
	}
	if engine.Registered("cr-bad") {
		t.Fatal("cr-bad (malformed) should not be registered")
	}
}

// TestChatCronEngine_NilDispatcherRecordsFailure: a tick firing with no
// dispatcher wired must record a "failed" history row naming the missing
// dispatcher — never a fabricated "completed" (FR-002). This is the state
// production wiring is in between WP03 landing and WP05 arming the real
// dispatcher.
func TestChatCronEngine_NilDispatcherRecordsFailure(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-nodispatch", "* * * * * *", true)
	ctx := context.Background()

	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
		Store: store, // Dispatcher intentionally left nil.
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	deadline := time.Now().Add(3 * time.Second)
	for {
		hist, herr := store.History(ctx, "cr-nodispatch", 10)
		if herr != nil {
			t.Fatalf("History: %v", herr)
		}
		if len(hist) > 0 {
			if hist[0].Status != "failed" {
				t.Errorf("status=%q, want failed (fabricated completed with no dispatcher wired is FR-002's exact defect)", hist[0].Status)
			}
			if hist[0].Error == "" {
				t.Error("Error should name the missing dispatcher, got empty string")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the nil-dispatcher failure row")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestChatCronEngine_SetDispatcherIsRaceSafe: SetDispatcher can be called
// concurrently with ticks firing without triggering the race detector.
func TestChatCronEngine_SetDispatcherIsRaceSafe(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateRow(t, store, "cr-race", "* * * * * *", true)
	ctx := context.Background()

	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{Store: store})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	disp := newStubDispatcher()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			engine.SetDispatcher(disp)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	<-done
	_ = disp.snapshot()
}

func drainOne(ch <-chan string) {
	select {
	case <-ch:
	default:
	}
}
