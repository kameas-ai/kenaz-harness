package scheduler_test

// Stop/one-shot interaction: a one-shot that engine Stop cancels before
// its run was dispatched is not used up (it stays enabled and fires on
// the next Start or boot); one cancelled after dispatch is (failed row,
// disabled), so a run's tool side effects never repeat. Every fire that
// began leaves a history row.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// reportingDispatcher implements scheduler.RunStartReporter. With
// blockFirst, its first call parks until ctx is cancelled (marking the
// run started first when markFirst is set); every other call completes
// at once.
type reportingDispatcher struct {
	blockFirst bool
	markFirst  bool
	entered    chan struct{}

	mu    sync.Mutex
	calls int
}

func (r *reportingDispatcher) ReportsRunStart() {}

func (r *reportingDispatcher) DispatchChatRun(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
	r.mu.Lock()
	r.calls++
	n := r.calls
	r.mu.Unlock()
	select {
	case r.entered <- struct{}{}:
	default:
	}
	ended := time.Now().UTC()
	if n > 1 || !r.blockFirst {
		return scheduler.ChatRunHistoryRecord{Status: "completed", StartedAt: now, EndedAt: &ended}, nil
	}
	if r.markFirst {
		scheduler.MarkRunDispatched(ctx)
	}
	<-ctx.Done()
	ended = time.Now().UTC()
	return scheduler.ChatRunHistoryRecord{Status: "failed", StartedAt: now, EndedAt: &ended, Error: "cancelled"}, nil
}

func (r *reportingDispatcher) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// gatedStore parks the first Get until release is closed, so a test can
// land Stop between beginFire and fireSync's row reload.
type gatedStore struct {
	scheduler.ScheduledChatStore
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (g *gatedStore) Get(ctx context.Context, id string) (scheduler.ChatRunRecord, error) {
	first := false
	g.once.Do(func() { first = true })
	if first {
		close(g.entered)
		<-g.release
	}
	return g.ScheduledChatStore.Get(ctx, id)
}

func newOnceEngine(t *testing.T, store scheduler.ScheduledChatStore, d scheduler.ChatRunDispatcher) *scheduler.ChatCronEngine {
	t.Helper()
	engine, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{Store: store, Dispatcher: d})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	t.Cleanup(engine.Stop)
	return engine
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func mustStop(t *testing.T, engine *scheduler.ChatCronEngine) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.StopContext(ctx); err != nil {
		t.Fatalf("StopContext: %v", err)
	}
}

func historyOf(t *testing.T, store scheduler.ScheduledChatStore, id string) []scheduler.ChatRunHistoryRecord {
	t.Helper()
	h, err := store.History(context.Background(), id, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	return h
}

func mustEnabled(t *testing.T, store scheduler.ScheduledChatStore, id string, want bool) {
	t.Helper()
	rec, err := store.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if rec.Enabled != want {
		t.Fatalf("%s enabled=%v, want %v", id, rec.Enabled, want)
	}
}

// Stop lands after the fire began but before its row reload: the reload
// still runs (WithoutCancel), a "skipped" history row is written, nothing
// is dispatched, and the one-shot stays enabled and fires on Start.
func TestChatCronEngine_OnceStoppedBeforeReloadStaysEnabled(t *testing.T) {
	base := openTestChatStore(t)
	mustCreateOnceRow(t, base, "once-gate", time.Now(), true)
	store := &gatedStore{ScheduledChatStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	disp := &reportingDispatcher{entered: make(chan struct{}, 4)}
	engine := newOnceEngine(t, store, disp)
	engine.Start()
	waitSignal(t, store.entered, "the fire's row reload")

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- engine.StopContext(ctx)
	}()
	for engine.Started() {
		time.Sleep(time.Millisecond)
	}
	close(store.release)
	if err := <-stopped; err != nil {
		t.Fatalf("StopContext: %v", err)
	}

	if n := disp.callCount(); n != 0 {
		t.Fatalf("dispatcher calls = %d, want 0 (stopped before dispatch)", n)
	}
	h := historyOf(t, base, "once-gate")
	if len(h) != 1 || h[0].Status != scheduler.ChatRunStatusSkipped {
		t.Fatalf("history = %+v, want one skipped row", h)
	}
	mustEnabled(t, base, "once-gate", true)
	if !engine.Registered("once-gate") {
		t.Fatal("one-shot dropped although it never ran")
	}

	engine.Start()
	waitSignal(t, disp.entered, "the re-fire after Start")
	waitOnceSettled(t, base, engine, "once-gate")
	if h := historyOf(t, base, "once-gate"); len(h) != 2 {
		t.Fatalf("history rows = %d, want 2 (skipped, then the real run)", len(h))
	}
}

// Stop cancels the fire inside a reporting dispatcher that had not yet
// started the run: same outcome as above.
func TestChatCronEngine_OnceStoppedBeforeRunStartStaysEnabled(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateOnceRow(t, store, "once-pre", time.Now(), true)
	disp := &reportingDispatcher{blockFirst: true, entered: make(chan struct{}, 4)}
	engine := newOnceEngine(t, store, disp)
	engine.Start()
	waitSignal(t, disp.entered, "the dispatch")
	mustStop(t, engine)

	h := historyOf(t, store, "once-pre")
	if len(h) != 1 || h[0].Status != scheduler.ChatRunStatusSkipped {
		t.Fatalf("history = %+v, want one skipped row", h)
	}
	mustEnabled(t, store, "once-pre", true)

	engine.Start()
	waitSignal(t, disp.entered, "the re-fire after Start")
	waitOnceSettled(t, store, engine, "once-pre")
	if n := disp.callCount(); n != 2 {
		t.Fatalf("dispatcher calls = %d, want 2", n)
	}
}

// Stop cancels a run that was already dispatched: the attempt is used up
// (failed row, disabled, dropped) so its tools can't run twice. The
// disable must survive the cancelled ctx (WithoutCancel).
func TestChatCronEngine_OnceStoppedAfterRunStartIsUsedUp(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateOnceRow(t, store, "once-post", time.Now(), true)
	disp := &reportingDispatcher{blockFirst: true, markFirst: true, entered: make(chan struct{}, 4)}
	engine := newOnceEngine(t, store, disp)
	engine.Start()
	waitSignal(t, disp.entered, "the dispatch")
	mustStop(t, engine)

	h := historyOf(t, store, "once-post")
	if len(h) != 1 || h[0].Status != "failed" {
		t.Fatalf("history = %+v, want one failed row", h)
	}
	mustEnabled(t, store, "once-post", false)
	if engine.Registered("once-post") {
		t.Fatal("used-up one-shot is still registered")
	}
	engine.Start()
	time.Sleep(200 * time.Millisecond)
	if n := disp.callCount(); n != 1 {
		t.Fatalf("dispatcher calls = %d, want 1 (no re-fire after a dispatched attempt)", n)
	}
}

// A dispatcher that does not report run starts can't prove nothing ran,
// so a Stop-cancelled one-shot is used up.
func TestChatCronEngine_OnceStoppedNonReportingDispatcherIsUsedUp(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateOnceRow(t, store, "once-nr", time.Now(), true)
	disp := &blockingDispatcher{entered: make(chan struct{}, 1)}
	engine := newOnceEngine(t, store, disp)
	engine.Start()
	waitSignal(t, disp.entered, "the dispatch")
	mustStop(t, engine)
	mustEnabled(t, store, "once-nr", false)
}

// A tick or timer callback that runs after Stop is refused: no dispatch,
// no history row, and a refused one-shot stays registered and enabled,
// then fires when Start re-arms it.
func TestChatCronEngine_FireAfterStopIsRefusedAndReArmed(t *testing.T) {
	store := openTestChatStore(t)
	mustCreateOnceRow(t, store, "once-late", time.Now().Add(time.Hour), true)
	mustCreateRow(t, store, "cron-late", "0 0 1 1 *", true)
	disp := newStubDispatcher()
	engine := newOnceEngine(t, store, disp)
	engine.Start()
	mustStop(t, engine)

	engine.FireOnceForTest("once-late")
	engine.FireForTest("cron-late")
	mustStop(t, engine) // waits for anything the calls above started

	if calls := disp.snapshot(); len(calls) != 0 {
		t.Fatalf("dispatcher calls after Stop = %v, want none", calls)
	}
	for _, id := range []string{"once-late", "cron-late"} {
		if h := historyOf(t, store, id); len(h) != 0 {
			t.Fatalf("%s: history after a refused fire = %+v, want none", id, h)
		}
	}
	mustEnabled(t, store, "once-late", true)
	if !engine.Registered("once-late") {
		t.Fatal("refused one-shot was dropped")
	}

	// Re-arm: move run_at into the past, re-sync, Start — it fires now.
	rec, err := store.Get(context.Background(), "once-late")
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	rec.RunAt = &past
	if err := store.Update(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := engine.Sync(context.Background(), "once-late"); err != nil {
		t.Fatal(err)
	}
	engine.Start()
	select {
	case id := <-disp.fire:
		if id != "once-late" {
			t.Fatalf("fired %q, want once-late", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("re-armed one-shot did not fire after Start")
	}
	waitOnceSettled(t, store, engine, "once-late")
}
