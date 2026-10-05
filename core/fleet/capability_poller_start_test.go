package fleet

import (
	"bytes"
	"context"
	"runtime"
	"testing"
	"time"
)

// gid returns the current goroutine's id by parsing the stack header.
// Test-only; there is no supported API for this on purpose.
func gid() []byte {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	// "goroutine 123 [running]:" → "123"
	buf = bytes.TrimPrefix(buf, []byte("goroutine "))
	if i := bytes.IndexByte(buf, ' '); i > 0 {
		return buf[:i]
	}
	return buf
}

// TestCapabilityPollerStart_ListenersDoNotFireOnCallersGoroutine pins the
// invariant that closed the v0.87.0 boot deadlock (2026-10-05): Start with
// a warm disk cache must fire OnChange listeners on the poller's own
// goroutine, never inline on the caller's. startFleetBackgroundLocked
// calls Start while holding settings' fleet.mu write lock and the listener
// (ReconcileTelemetry) read-locks the same mutex — a synchronous fire
// deadlocked every enrolled install at boot. Production gates Start off
// under go test, so this is the only test that executes this path.
func TestCapabilityPollerStart_ListenersDoNotFireOnCallersGoroutine(t *testing.T) {
	dir := t.TempDir()
	if err := SaveCapabilities(dir, Capabilities{
		Enabled:   map[Capability]bool{CapSharedTeamGraph: true},
		FetchedAt: time.Now(),
		Source:    "cache",
	}); err != nil {
		t.Fatalf("SaveCapabilities: %v", err)
	}
	if c, err := LoadCapabilities(dir); err != nil || c.Source != "cache" {
		t.Fatalf("warm cache not loadable (source=%q err=%v) — test setup no longer matches LoadCapabilities", c.Source, err)
	}

	p := NewCapabilityPoller(nil, dir)
	callerGID := string(gid())
	fired := make(chan string, 1)
	p.OnChange(func(Capabilities) {
		select {
		case fired <- string(gid()):
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx)
	defer p.Stop()

	select {
	case listenerGID := <-fired:
		if listenerGID == callerGID {
			t.Fatalf("OnChange listener fired on Start's calling goroutine (gid %s) — a caller holding a lock the listener needs deadlocks at boot", callerGID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("warm-cache OnChange never fired — Start no longer loads the disk cache into setCurrent")
	}
}
