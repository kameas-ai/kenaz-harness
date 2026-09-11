package hooks

// shutdown_deadline_test.go — round-2 review of finding #61's fix
// (commit 83b7ac91): wiring hooks.Runner.Shutdown into real process
// exit (desktop OnShutdown, runServeMode, cmd/harness-served) made a
// previously-dead path live, and the drain had no deadline. Worst
// case ceil(queueDepth/poolSize) * DefaultAsyncTimeoutMs =
// ceil(32/8) * 60s = 240s — a single hung async post_send hook (most
// commonly memory.persist's embedding call) could hold the OS process
// open for up to four minutes on quit with no user feedback.
//
// TestRunner_Shutdown_AbandonsHangingHook installs a builtin
// post_send hook that blocks forever on a channel the test controls
// (no network, no sleep-based flakiness), dispatches it async via
// RunPostSend, then calls Shutdown() and asserts it returns
// comfortably under the old 240s ceiling — proving
// asyncShutdownDrainTimeout (core/hooks/fire.go) actually bounds the
// drain instead of just documenting an intent.
//
// Mutation performed while developing this fix (and reverted before
// commit): changed Runner.Shutdown's call from
// `p.shutdown(asyncShutdownDrainTimeout)` to `p.shutdown(0)` — the
// unbounded-wait path asyncPool.shutdown kept for other test call
// sites. With that mutation this test hangs until Go's own -timeout
// kills the test binary, confirming the assertion is not vacuous.
// `git status --porcelain` was clean before and after.

import (
	"context"
	"testing"
	"time"
)

func TestRunner_Shutdown_AbandonsHangingHook(t *testing.T) {
	t.Parallel()

	reg, _ := NewRegistry("")
	builtins := NewBuiltinRegistry()

	block := make(chan struct{}) // released only at the end of the test
	entered := make(chan struct{}, 1)
	builtins.RegisterPostSend("test.hang", func(_ context.Context, _ PostSendEvent, _ map[string]any) error {
		entered <- struct{}{}
		<-block // deliberately ignores ctx cancellation — a genuinely hung hook
		return nil
	}, BuiltinDescriptor{ID: "test.hang", Name: "test hang", Events: []string{EventPostSend}})

	if err := reg.Add(Hook{
		ID: "h", Name: "n", Event: EventPostSend, Kind: KindBuiltin,
		Enabled: true, Builtin: "test.hang",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	runner := NewRunner(Config{Registry: reg, Builtins: builtins})
	runner.RunPostSend(context.Background(), PostSendEvent{
		SessionID: "s", UserTurn: "u", AssistantTurn: "a", FinishReason: "completed",
	})

	// Confirm the hook is actually inside the hang (and therefore
	// genuinely blocking a pool worker) before timing Shutdown —
	// otherwise a slow scheduler could let Shutdown race ahead of
	// dispatch and the test would pass for the wrong reason.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("hanging hook never started — test setup is broken, not exercising the shutdown deadline")
	}

	start := time.Now()
	runner.Shutdown()
	elapsed := time.Since(start)

	// Best-effort cleanup: release the permanently-blocked worker so
	// it doesn't linger for the rest of the test binary's life.
	// Shutdown itself does not wait for this — that's the point.
	close(block)

	// 5s, not 10s: the deadline is 3s, so this leaves ~2s of CI jitter
	// headroom while still catching a regression to 6-8s. A 10s bound only
	// catches a regression back toward the old 240s ceiling, which is the
	// easy case -- the plausible regression is someone nudging the deadline
	// up, not removing it.
	const bound = 5 * time.Second
	if elapsed > bound {
		t.Fatalf("Shutdown() took %v with a hung post_send hook, want <= %v (drain deadline is not being enforced)", elapsed, bound)
	}
	t.Logf("Shutdown() returned in %v with a permanently-hung post_send hook", elapsed)
}
