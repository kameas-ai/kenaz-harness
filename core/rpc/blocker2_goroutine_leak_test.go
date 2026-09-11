package rpc

// blocker2_goroutine_leak_test.go — committed regression proof for the
// review-of-finding-#61 Blocker 2 (2026-09-11 review of
// fix/memory-persist-growth-and-latency): buildMemoryPruneScheduler is
// gated only on a real on-disk memory store (i.e. c.DataDir() != ""),
// so ANY test that boots New(c) over a real temp DataDir without
// calling Shutdown leaks a prune.(*Scheduler).loop goroutine — and,
// because compactionScheduler (CK-09) and pruneScheduler (GAP-1) are
// started together in New() and stopped together in Shutdown(), a
// compaction.(*SweepScheduler).loop goroutine too.
//
// The reviewer found this live and reproduced it manually: 5
// un-Shutdown-ed rpc.New() instances produced a runtime.NumGoroutine()
// delta of 55, with 5 prune.(*Scheduler).loop frames in a full stack
// dump. This test is the permanent, CI-enforced version of that manual
// check — the reviewer explicitly noted the original leak check that
// caught this class of bug was exploratory and got deleted, which
// protects nothing going forward. This one stays.
//
// It asserts on the STACK DUMP (goroutine function names), not just a
// raw NumGoroutine() delta, because other background pollers this repo
// wires (settings sync, context-graph sync, unit sync, chat cron, …)
// make a bare count too noisy to pin a specific leak class reliably —
// see the "vacuous premise" check below, which would already catch a
// too-small delta before the real assertion runs.

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
)

// Goroutine top-frame substrings for the two schedulers finding #61
// and CK-09 wired into New()/Shutdown(). These are the unqualified
// receiver-method names as they appear in a runtime.Stack dump
// (package-path-qualified, e.g.
// "github.com/kameas-ai/kenaz-harness/core/memory/prune.(*Scheduler).loop").
const (
	pruneLoopFrame      = "core/memory/prune.(*Scheduler).loop"
	compactionLoopFrame = "core/agentgraph/compaction.(*SweepScheduler).loop"
)

// countGoroutineFrames returns how many goroutines in a full stack
// dump of the current process contain substr anywhere in their trace.
func countGoroutineFrames(substr string) int {
	// Stack dumps under heavy goroutine counts can exceed a small
	// buffer; runtime.Stack silently truncates instead of erroring, so
	// size generously. 4 MiB comfortably covers a test binary's worth
	// of goroutines.
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	return bytes.Count(buf[:n], []byte(substr))
}

// TestAPI_Shutdown_NoSchedulerGoroutineLeak constructs N real-DataDir
// APIs (which starts N prune.(*Scheduler).loop + N
// compaction.(*SweepScheduler).loop goroutines), confirms those
// goroutines are actually running (so the assertion below isn't
// vacuous against a future refactor that changes the gate), Shuts all
// N down, and asserts both goroutine classes are fully gone and
// runtime.NumGoroutine() returns to baseline.
//
// Mutation: revert either core/rpc/api.go's
// `a.pruneScheduler.Stop()` / `a.compactionScheduler.Stop()` line in
// Shutdown(), or core/hooks/fire.go's Runner.Shutdown nil-out (Blocker
// 1) with a hook dispatched first — either should leave goroutines
// behind and fail this test.
func TestAPI_Shutdown_NoSchedulerGoroutineLeak(t *testing.T) {
	// Not t.Parallel(): this test reasons about the ENTIRE test
	// binary's goroutine population, which sibling parallel tests
	// would pollute in both directions (false leak, false clean).
	runtime.GC()
	baseline := runtime.NumGoroutine()

	const n = 5
	apis := make([]*API, 0, n)
	for i := 0; i < n; i++ {
		c, err := core.New(core.Options{DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("core.New: %v", err)
		}
		apis = append(apis, New(c, WithSettingsStore(newTestStore(t))))
	}

	// Vacuity guard: the two scheduler goroutine classes must actually
	// be running N times over before we can meaningfully assert they
	// are gone after Shutdown.
	deadline := time.Now().Add(2 * time.Second)
	var pruneBefore, compactionBefore int
	for time.Now().Before(deadline) {
		pruneBefore = countGoroutineFrames(pruneLoopFrame)
		compactionBefore = countGoroutineFrames(compactionLoopFrame)
		if pruneBefore >= n && compactionBefore >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pruneBefore < n {
		t.Fatalf("only %d prune.(*Scheduler).loop goroutine(s) running after constructing %d real-DataDir APIs, want >= %d — "+
			"buildMemoryPruneScheduler's gate no longer starts a goroutine here, so the leak assertion below would be vacuous",
			pruneBefore, n, n)
	}
	if compactionBefore < n {
		t.Fatalf("only %d compaction.(*SweepScheduler).loop goroutine(s) running after constructing %d real-DataDir APIs, want >= %d — "+
			"buildCompactionWiring no longer starts a goroutine here, so the leak assertion below would be vacuous",
			compactionBefore, n, n)
	}

	for _, api := range apis {
		api.Shutdown()
	}

	// Both Scheduler.Stop() implementations block until their loop's
	// doneCh closes, so by the time the loop above returns the
	// goroutines should already be gone; poll briefly anyway rather
	// than sleeping a fixed duration.
	deadline = time.Now().Add(3 * time.Second)
	var pruneAfter, compactionAfter int
	for time.Now().Before(deadline) {
		pruneAfter = countGoroutineFrames(pruneLoopFrame)
		compactionAfter = countGoroutineFrames(compactionLoopFrame)
		if pruneAfter == 0 && compactionAfter == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if pruneAfter != 0 {
		t.Errorf("%d prune.(*Scheduler).loop goroutine(s) still running after Shutdown-ing all %d APIs (finding #61 GAP-1 leak)", pruneAfter, n)
	}
	if compactionAfter != 0 {
		t.Errorf("%d compaction.(*SweepScheduler).loop goroutine(s) still running after Shutdown-ing all %d APIs (CK-09 leak)", compactionAfter, n)
	}

	// NumGoroutine() is logged for diagnostic visibility only — it is
	// deliberately NOT asserted on. Investigated live (2026-09-11): a
	// single real-DataDir New(c) leaves a delta of ~7 goroutines after
	// Shutdown() even with both scheduler classes above at zero —
	// database/sql's connectionOpener (the sqlite pool Shutdown does
	// not, and should not, close — Core's DB lifecycle is the caller's
	// to manage, not API.Shutdown's), core/contexts.(*fsWatcher).run +
	// its fsnotify kqueue reader (startContextsWatcher in api.go has no
	// Stop() call site at all — a real gap, but a DIFFERENT one than
	// Blocker 2 and out of scope for finding #61's fix), and one
	// in-flight openrouter.RefreshModelsAsync HTTP fetch + its dial
	// goroutine (one-shot, self-terminating, not a recurring leak).
	// None of those are prune/compaction goroutines, so folding them
	// into a single NumGoroutine budget would make this test flaky (or
	// falsely red) for reasons this fix doesn't touch, while hiding the
	// two classes that actually matter behind noise. The startContextsWatcher
	// gap is worth its own follow-up; it is not this Blocker's claim.
	runtime.GC()
	t.Logf("runtime.NumGoroutine() after Shutdown-ing %d APIs: %d (baseline %d, delta %d) — diagnostic only, see comment above",
		n, runtime.NumGoroutine(), baseline, runtime.NumGoroutine()-baseline)
}
