package stdio

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestSupervisor_RestartsOnCrash drives the fake server through one
// crash + recovery and asserts the supervisor restarts the process
// without wallclock backoff (the test injects a no-op Sleep).
func TestSupervisor_RestartsOnCrash(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		// Inject a no-op Sleep so the 1 s / 2 s / 4 s backoff
		// schedule does not pace the test.
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--crash-on-call"},
		PingPeriod: -1, // disable health pings; test drives crashes directly
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	if got := inst.State(); got != StateRunning {
		t.Fatalf("post-spawn state = %q, want running", got)
	}

	// Crash 1 — server exits on tools/call. CallTool returns an
	// error because the response router unblocks via CancelAll.
	preLen := len(inst.restartHistorySnapshot())
	if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("CallTool: expected crash error, got nil")
	}
	waitForRestartCount(t, inst, preLen+1, 5*time.Second)

	if got := len(inst.restartHistorySnapshot()); got != 1 {
		t.Fatalf("restartHistory after 1 crash = %d, want 1", got)
	}
}

// TestSupervisor_FailsAfterThreeRestarts hammers the server with
// four crashes and asserts state transitions to failed once the
// restart window's three-attempt budget is exhausted (FR-007).
func TestSupervisor_FailsAfterThreeRestarts(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--crash-on-call"},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// Three crashes → three restarts (history fills to 3 entries).
	for i := 0; i < 3; i++ {
		preLen := len(inst.restartHistorySnapshot())
		if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("CallTool %d: expected crash error", i+1)
		}
		waitForRestartCount(t, inst, preLen+1, 5*time.Second)
	}

	// Fourth crash → exceed budget, state=failed, no further
	// restart.
	if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("CallTool 4: expected crash error")
	}
	waitForState(t, inst, StateFailed, 5*time.Second)

	if attempts := len(inst.restartHistorySnapshot()); attempts != 3 {
		t.Fatalf("restartHistory after exhaustion = %d, want 3", attempts)
	}

	// Subsequent calls return an error and DO NOT trip another
	// restart cycle. Verify by snapshotting after a brief settle.
	if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("CallTool after failed: expected error")
	}
	time.Sleep(50 * time.Millisecond)
	if got := inst.State(); got != StateFailed {
		t.Fatalf("state after post-failed call = %q, want failed", got)
	}
}

// TestSupervisor_ClockInjectionIsConsulted asserts that the
// injected Now is the source of truth for restartHistory pruning —
// shifting the clock forward by > 5 min between crashes resets the
// budget.
func TestSupervisor_ClockInjectionIsConsulted(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	// Manual clock the test advances explicitly. Sleep is a no-op
	// so the supervisor's backoff doesn't push wallclock around.
	clock := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Now:   clock.Now,
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--crash-on-call"},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// Three crashes inside the window.
	for i := 0; i < 3; i++ {
		preLen := len(inst.restartHistorySnapshot())
		if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("CallTool %d: expected crash error", i+1)
		}
		waitForRestartCount(t, inst, preLen+1, 5*time.Second)
	}
	if attempts := len(inst.restartHistorySnapshot()); attempts != 3 {
		t.Fatalf("restartHistory in-window = %d, want 3", attempts)
	}

	// Advance the injected clock past the 5 minute window. Next
	// crash should prune the history and restart cleanly instead
	// of failing.
	clock.Advance(6 * time.Minute)
	if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("CallTool 4: expected crash error")
	}
	// After pruning, the post-restart history is exactly [now]
	// (length 1). Wait for it.
	waitForHistoryEquals(t, inst, 1, 5*time.Second)
	if got := len(inst.restartHistorySnapshot()); got != 1 {
		t.Fatalf("restartHistory after window slide = %d, want 1 (pruned)", got)
	}
}

// TestSupervisor_EOFDetectionTriggersRestart kills the underlying
// process with an external SIGKILL and asserts the supervisor
// recovers — exercises T011's EOF-detection path.
func TestSupervisor_EOFDetectionTriggersRestart(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// Snapshot the original PID so we can assert the respawn used
	// a fresh process.
	origPID := inst.statusPIDForTest()
	if origPID == 0 {
		t.Fatalf("origPID = 0; expected a running process")
	}

	// External SIGKILL — emulate A4 (npx process killed by the
	// user / OS). Reader sees EOF, signalCrash fires, supervisor
	// restarts.
	if err := inst.killProcessForTest(); err != nil {
		t.Fatalf("kill: %v", err)
	}

	waitForRestartCount(t, inst, 1, 5*time.Second)
	newPID := inst.statusPIDForTest()
	if newPID == 0 || newPID == origPID {
		t.Fatalf("newPID = %d (origPID = %d); expected a fresh process", newPID, origPID)
	}
}

// TestSupervisor_GoroutinesReturnToBaselineAfterRestarts asserts
// NFR-002 still holds after the supervisor has fired several
// restart cycles — Close reaps every spawned goroutine.
func TestSupervisor_GoroutinesReturnToBaselineAfterRestarts(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	baseline := runtime.NumGoroutine()

	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep: func(time.Duration) {},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:         "fake",
		Command:    []string{bin, "--crash-on-call"},
		PingPeriod: -1,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Two successful restart cycles to give the supervisor a real
	// workout.
	for i := 0; i < 2; i++ {
		preLen := len(inst.restartHistorySnapshot())
		if _, err := inst.CallTool(ctx, "fake_echo", json.RawMessage(`{}`)); err == nil {
			t.Fatalf("CallTool %d: expected crash error", i+1)
		}
		waitForRestartCount(t, inst, preLen+1, 5*time.Second)
	}

	if err := inst.Close(context.Background()); err != nil && !isExpectedExit(err) {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := runtime.NumGoroutine(); got <= baseline+2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d, baseline %d (NFR-002)", runtime.NumGoroutine(), baseline)
}

// --- test helpers ---

// fakeClock is a manually-advanced wallclock for the supervisor
// test. The Advance/Now pair takes a sync.Mutex so the supervisor
// goroutine can safely read while the test mutates.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// waitForState polls inst.State() until it matches want or the
// deadline elapses. Polling beats injecting a state-change
// channel because the lifecycle is mediated by lifecycleMu, not by
// a publisher.
func waitForState(t *testing.T, inst *ServerInstance, want State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if inst.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state never reached %q within %s (now %q)", want, within, inst.State())
}

// waitForRestartCount polls until the restart history reaches
// atLeast entries AND state is back to running. This is the
// reliable "did the supervisor finish a restart cycle" signal —
// observing state==running alone is racy because the value
// remains true between the crash and the supervisor's reaction.
func waitForRestartCount(t *testing.T, inst *ServerInstance, atLeast int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		hist := inst.restartHistorySnapshot()
		if len(hist) >= atLeast && inst.State() == StateRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("restart count never reached %d (now %d, state %q) within %s",
		atLeast, len(inst.restartHistorySnapshot()), inst.State(), within)
}

// waitForHistoryEquals polls until the restart history has
// exactly want entries AND state is running. Used when the
// supervisor is expected to PRUNE history (the count can drop).
func waitForHistoryEquals(t *testing.T, inst *ServerInstance, want int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		hist := inst.restartHistorySnapshot()
		if len(hist) == want && inst.State() == StateRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("history never reached len=%d (now %d, state %q) within %s",
		want, len(inst.restartHistorySnapshot()), inst.State(), within)
}

// restartHistorySnapshot reads the history slice under the
// lifecycle lock so the test can assert post-condition counts
// without racing the supervisor.
func (s *ServerInstance) restartHistorySnapshot() []time.Time {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	out := make([]time.Time, len(s.restartHistory))
	copy(out, s.restartHistory)
	return out
}

// statusPIDForTest reads the live PID under the lifecycle lock.
// Test-only; production code uses RecipeStatus().PID.
func (s *ServerInstance) statusPIDForTest() int {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// killProcessForTest SIGKILLs the underlying process. Used by the
// EOF test to simulate an external kill.
func (s *ServerInstance) killProcessForTest() error {
	s.lifecycleMu.Lock()
	cmd := s.cmd
	s.lifecycleMu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
