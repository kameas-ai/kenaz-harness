package rpc

// finding61_memory_prune_scheduler_test.go — finding #61 GAP-1:
// prune.NewScheduler had a complete, independently-tested
// Start/Stop/RunOnce implementation (core/memory/prune/pruner_test.go)
// and ZERO production callers — only the manual "Prune preview" /
// "Prune now" inspector RPCs ever constructed a Pruner. The store's
// default 10k-row cap was therefore never enforced automatically; it
// only shrank when a user happened to open the memory inspector and
// prune by hand.
//
// These tests pin both halves of the fix:
//   - buildMemoryPruneScheduler is the pure gating function: nil store
//     in (nil-core test chassis, no DataDir) -> nil scheduler out, so
//     the background goroutine never starts under those conditions;
//     real store in -> a working Scheduler out.
//   - TestAPI_New_StartsAndStopsPruneScheduler drives the REAL rpc.New(c)
//     boot path (mirrors api_narrative_gate_boot_test.go's pattern) and
//     asserts the scheduler is not just constructed but actually
//     STARTED (LastRun gets stamped by the boot catch-up sweep) and
//     that Shutdown stops it cleanly (idempotent, no panic).
//
// The store-side behavior (that a real sweep actually evicts rows past
// the cap on real disk, surviving a close/reopen) is proven once,
// package-locally, in core/memory/prune/pruner_test.go's
// TestScheduler_RunOnce_EvictsPastCapOnRealDisk — not re-litigated
// here.
//
// Review finding (Blocker 1, 2026-09-11): TestAPI_New_StartsAndStopsPruneScheduler
// previously called api.Shutdown() twice to "prove idempotency" without
// ever dispatching a hook, so hooks.Runner.lazyPool() never created
// r.pool and the dangerous path — asyncPool.shutdown() unconditionally
// closing r.pool.work a second time — was never exercised. The test
// passed for the wrong reason. It now installs the real starter
// memory.persist hook and dispatches a post_send event first, so the
// pool actually exists before the double-Shutdown assertion runs. See
// core/hooks/fire.go's Runner.Shutdown for the fix (nil the pool under
// the lock before draining it, so a second call is a no-op).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	"github.com/kameas-ai/kenaz-harness/core/memory"
)

func TestBuildMemoryPruneScheduler_NilStoreReturnsNil(t *testing.T) {
	t.Parallel()
	if s := buildMemoryPruneScheduler(nil); s != nil {
		t.Fatalf("buildMemoryPruneScheduler(nil) = %v, want nil (this is what keeps the scheduler's goroutine out of every test that doesn't wire a real on-disk store)", s)
	}
}

func TestBuildMemoryPruneScheduler_RealStore_WiresAWorkingScheduler(t *testing.T) {
	t.Parallel()
	path := t.TempDir() + "/memory.gob"
	store, err := memory.NewChromemStore(path)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sched := buildMemoryPruneScheduler(store)
	if sched == nil {
		t.Fatal("buildMemoryPruneScheduler(store) = nil, want a configured Scheduler for a real on-disk store")
	}
	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if sched.LastRun().IsZero() {
		t.Fatal("RunOnce did not stamp LastRun")
	}
}

// TestAPI_New_StartsAndStopsPruneScheduler pins the production wiring:
// New(c) must actually construct AND START a.pruneScheduler when a
// real on-disk memory store exists (DataDir set), and Shutdown must
// stop it cleanly.
//
// Mutation: remove (or fail to call) the
// `a.pruneScheduler.Start(context.Background(), time.Time{})` line in
// New(). Must fail — LastRun() stays zero forever because nothing
// ever ran the catch-up sweep.
func TestAPI_New_StartsAndStopsPruneScheduler(t *testing.T) {
	t.Parallel()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	api := New(c, WithSettingsStore(newTestStore(t)))
	if api == nil {
		t.Fatal("rpc.New returned nil")
	}
	if api.pruneScheduler == nil {
		t.Fatal("api.pruneScheduler is nil — New(c) did not wire the automatic prune scheduler even though a real on-disk memory store exists (DataDir set)")
	}

	// Start's boot catch-up sweep runs on its own goroutine; poll
	// briefly instead of sleeping a fixed duration.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && api.pruneScheduler.LastRun().IsZero() {
		time.Sleep(10 * time.Millisecond)
	}
	if api.pruneScheduler.LastRun().IsZero() {
		t.Fatal("pruneScheduler.LastRun() is still zero after 2s — the boot catch-up sweep never ran, meaning Start() was not called (GAP-1 still open)")
	}

	// Blocker 1 (review of finding #61): the double-Shutdown assertion
	// below only exercises hooks.Runner.Shutdown()'s dangerous
	// double-close-of-channel path if the async pool was actually
	// created first (hooks.Runner.lazyPool() creates it lazily, on
	// first dispatch). Install the real starter memory.persist hook —
	// the same one production wires via the Settings "enable long-term
	// memory" toggle — and dispatch a post_send event through it so
	// api.hookRunner's pool exists before we shut down.
	ctx := context.Background()
	if err := api.Hooks().InstallStarterMemoryHooks(ctx); err != nil {
		t.Fatalf("InstallStarterMemoryHooks: %v", err)
	}
	if api.hookRunner == nil {
		t.Fatal("api.hookRunner is nil — cannot dispatch a post_send event to exercise the pool")
	}
	api.hookRunner.RunPostSend(ctx, hooks.PostSendEvent{
		SessionID:     "s1",
		UserTurn:      strings.Repeat("u", 200),
		AssistantTurn: strings.Repeat("a", 400),
		FinishReason:  "completed",
	})

	api.Shutdown()
	// Shutdown must be safe to call twice (main.go's OnShutdown /
	// double-invocation safety, same contract every other scheduler
	// Shutdown stops honors). Before the core/hooks/fire.go fix, this
	// second call panicked with "close of closed channel" — verified by
	// reverting the fix locally and confirming this line then panics.
	api.Shutdown()
}
