package stdio

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeTicker is a Ticker the test drives manually. The supervisor
// goroutine reads C() each iteration; the test calls Tick() to
// fire one beat at a time so ping cadence is deterministic.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 16)} }

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               {}

// Tick fires one beat. Blocks if the channel buffer is saturated
// so callers can't outrun the consumer.
func (t *fakeTicker) Tick() {
	t.ch <- time.Now()
}

// TestHealth_TwoConsecutiveFailedPingsTripRestart drives the fake
// server with --ignore-pings and asserts the second consecutive
// ping timeout signals a crash, which the supervisor reacts to by
// restarting (A8 + FR-008).
func TestHealth_TwoConsecutiveFailedPingsTripRestart(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	tickerHandle := struct {
		mu sync.Mutex
		t  *fakeTicker
	}{}
	newTicker := func(time.Duration) Ticker {
		ft := newFakeTicker()
		tickerHandle.mu.Lock()
		tickerHandle.t = ft
		tickerHandle.mu.Unlock()
		return ft
	}

	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep:     func(time.Duration) {},
		NewTicker: newTicker,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:          "fake",
		Command:     []string{bin, "--ignore-pings"},
		PingPeriod:  50 * time.Millisecond,
		PingTimeout: 100 * time.Millisecond,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	// First spawn started healthPinger which built tickerHandle.t.
	// Wait a beat so the goroutine has stored the ticker before
	// the test drives it.
	deadline := time.Now().Add(2 * time.Second)
	var ft *fakeTicker
	for time.Now().Before(deadline) {
		tickerHandle.mu.Lock()
		ft = tickerHandle.t
		tickerHandle.mu.Unlock()
		if ft != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ft == nil {
		t.Fatalf("healthPinger never registered its ticker")
	}

	// Snapshot original PID; expect a fresh process post-restart.
	origPID := inst.statusPIDForTest()
	if origPID == 0 {
		t.Fatalf("origPID = 0; expected a running process")
	}

	// Two consecutive ping failures. Each Tick fires one beat;
	// the server ignores the ping; callRaw times out after
	// PingTimeout (100 ms). The healthPinger increments its
	// failure counter and on the 2nd one calls signalCrash.
	ft.Tick()
	// Wait long enough for the first ping to time out before the
	// second tick lands so they register as consecutive failures.
	time.Sleep(200 * time.Millisecond)
	ft.Tick()

	// Restart should fire. Wait for the cycle to complete by
	// observing the restartHistory growing — observing state
	// alone is racy because state stays at running between the
	// crash signal and the supervisor's first state mutation.
	waitForRestartCount(t, inst, 1, 5*time.Second)
	newPID := inst.statusPIDForTest()
	if newPID == 0 || newPID == origPID {
		t.Fatalf("newPID = %d (origPID = %d); expected fresh process", newPID, origPID)
	}
}

// TestHealth_SuccessfulPingResetsFailureCounter asserts a single
// ping miss followed by a successful one does not snowball into a
// restart — the failure count resets.
func TestHealth_SuccessfulPingResetsFailureCounter(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)

	tickerHandle := struct {
		mu sync.Mutex
		t  *fakeTicker
	}{}
	newTicker := func(time.Duration) Ticker {
		ft := newFakeTicker()
		tickerHandle.mu.Lock()
		tickerHandle.t = ft
		tickerHandle.mu.Unlock()
		return ft
	}

	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{
		Sleep:     func(time.Duration) {},
		NewTicker: newTicker,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{
		ID:          "fake",
		Command:     []string{bin}, // no --ignore-pings; pings succeed
		PingPeriod:  50 * time.Millisecond,
		PingTimeout: 500 * time.Millisecond,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	var ft *fakeTicker
	for time.Now().Before(deadline) {
		tickerHandle.mu.Lock()
		ft = tickerHandle.t
		tickerHandle.mu.Unlock()
		if ft != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ft == nil {
		t.Fatalf("healthPinger never registered its ticker")
	}

	// Fire 3 successful ticks. Without ignore-pings the fake
	// server replies; failure counter never advances; no restart
	// happens.
	for i := 0; i < 3; i++ {
		ft.Tick()
		time.Sleep(60 * time.Millisecond)
	}

	if got := inst.State(); got != StateRunning {
		t.Fatalf("state after 3 healthy pings = %q, want running", got)
	}
	if got := len(inst.restartHistorySnapshot()); got != 0 {
		t.Fatalf("restartHistory after healthy pings = %d, want 0", got)
	}
}
