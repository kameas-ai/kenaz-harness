package fleet

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

const memSyncLoopFrame = "core/fleet.(*MemorySync).Start.func1"

func memSyncLoops() int {
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	return bytes.Count(buf[:n], []byte(memSyncLoopFrame))
}

func waitMemSyncLoops(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	got := memSyncLoops()
	for got != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		got = memSyncLoops()
	}
	if got != want {
		t.Fatalf("(*MemorySync).Start loop goroutines = %d, want %d", got, want)
	}
}

// TestMemorySync_StopEndsLoop_RestartAfterSignIn pins the lifecycle the
// unwired-ledger 2026-10-07 entry asked for: Stop cancels + waits (no loop
// goroutine survives), is idempotent and nil-safe, Start is idempotent, and
// a Start after Stop (the sign-in restart) runs a fresh loop.
// Mutation: drop the cancel()/<-done in Stop and this fails.
func TestMemorySync_StopEndsLoop_RestartAfterSignIn(t *testing.T) {
	// Not parallel: counts goroutines process-wide.
	base := memSyncLoops()
	w := newMemWorld(t)
	d := w.device("devA", 0)
	d.ms.cfg.Interval = time.Hour

	d.ms.Start(context.Background())
	d.ms.Start(context.Background()) // idempotent
	waitMemSyncLoops(t, base+1)

	d.ms.Stop()
	if got := memSyncLoops(); got != base {
		t.Fatalf("after Stop: %d loop goroutines, want %d (Stop must wait for exit)", got, base)
	}
	d.ms.Stop() // idempotent
	(*MemorySync)(nil).Stop()

	d.ms.Start(context.Background())
	waitMemSyncLoops(t, base+1)
	d.ms.Stop()
	waitMemSyncLoops(t, base)
}

// TestMemorySync_StoppedLaneReportsSignedOut: after node_removed / sign-out
// StopFleetBackground stops the lane and nils the capability poller; the
// panel used to read not_entitled (RunOnce's entitled() saw default-deny).
// A stopped lane records nothing and Status names it signed_out.
func TestMemorySync_StoppedLaneReportsSignedOut(t *testing.T) {
	w := newMemWorld(t)
	d := w.device("devA", 0)
	d.ms.cfg.Interval = time.Hour
	d.ms.Start(context.Background())
	// Sign-out: capability poller gone (default-deny), lane stopped, board reset.
	d.ms.Stop()
	d.caps = nil // after Stop: the loop goroutine reads it
	d.lanes.Reset()
	if s := d.ms.Status(context.Background()).Lane; s.Status != LaneOff || s.Reason != "signed_out" {
		t.Fatalf("stopped lane status = %+v, want off/signed_out", s)
	}
	if s := d.lanes.Snapshot(LaneMemorySync); s.Reason == "not_entitled" {
		t.Fatalf("stopped lane recorded %+v on the board", s)
	}
	// Sign-in restarts it: the label is the live board's again.
	d.caps = &Capabilities{Enabled: map[Capability]bool{CapMemorySync: true}, FetchedAt: time.Now()}
	d.ms.Start(context.Background())
	t.Cleanup(d.ms.Stop)
	if s := d.ms.Status(context.Background()).Lane; s.Reason == "signed_out" {
		t.Fatalf("restarted lane still reports signed_out: %+v", s)
	}
}
