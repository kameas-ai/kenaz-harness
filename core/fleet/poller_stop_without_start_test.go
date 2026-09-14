package fleet

import (
	"testing"
	"time"
)

// Stop() on a constructed-but-never-started poller must return, not block.
//
// This is a regression test for a real 10-minute CI hang, and the bound is the
// whole point of the test: both pollers used to allocate `done` in their
// constructor while only Start's goroutine ever closed it, so the bare
// `<-p.done` in Stop waited on a goroutine that did not exist.
//
// The construct-without-start state is not exotic. It is documented on
// NewCapabilityPoller ("The poller does NOT start automatically"), and it is
// exactly what settings.SetFleetClient produces under `go test`, where the
// Start call is guarded by testing.Testing() but the instance is still
// assigned because the lockdown watcher needs it. The observed failure was
// API.Shutdown -> StopFleetBackground -> CapabilityPoller.Stop hanging until
// the 10m test timeout killed the entire core/rpc package.
//
// A plain call to Stop() would hang the test binary instead of failing it, so
// each case runs Stop in a goroutine and fails on a timeout rather than
// relying on the package deadline to notice.
func TestPollers_StopWithoutStart_DoesNotBlock(t *testing.T) {
	t.Parallel()

	const budget = 5 * time.Second

	cases := []struct {
		name string
		stop func()
	}{
		{
			name: "CapabilityPoller",
			stop: NewCapabilityPoller(nil, t.TempDir()).Stop,
		},
		{
			// Same latent defect, unreachable in production today only
			// because SetFleetClient's guard wraps the whole ConfigPoller
			// block and leaves the field nil. Pinned so it stays fixed.
			name: "ConfigPoller",
			stop: NewConfigPoller(nil, t.TempDir(), nil).Stop,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			returned := make(chan struct{})
			go func() {
				defer close(returned)
				tc.stop()
			}()

			select {
			case <-returned:
			case <-time.After(budget):
				t.Fatalf("%s.Stop() did not return within %s on a poller that was never started; "+
					"it is blocked waiting for a goroutine that does not exist", tc.name, budget)
			}
		})
	}
}

// Stop() must still wait for a started poller's goroutine to exit, so the
// nil-check added above cannot be "fixed" by skipping the wait entirely.
func TestCapabilityPoller_StopAfterStart_StillWaitsForGoroutine(t *testing.T) {
	t.Parallel()

	p := NewCapabilityPoller(nil, t.TempDir())
	p.Start(t.Context())

	if p.done == nil {
		t.Fatal("Start must allocate done; Stop relies on it being non-nil to know a goroutine is running")
	}

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		p.Stop()
	}()

	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		t.Fatal("Stop() did not return after Start(); the poll goroutine never exited")
	}

	select {
	case <-p.done:
	default:
		t.Fatal("Stop() returned while done was still open: it is no longer waiting for the goroutine to exit")
	}
}
