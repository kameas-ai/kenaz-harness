package rpc

// contextbootstrap_progress_test.go pins the delivery contract of
// bootstrapProgressSink: fleet progress PATCHes leave in emission order, and
// drain() is a real barrier rather than a hint.
//
// Both properties exist because the original implementation spawned one
// unowned goroutine per Emit. That shape let PATCHes overtake each other on
// the wire (fleet could see a run go backwards, or be mutated after
// dispatch() had already reported status=completed) and let a straggler
// outlive the whole run — which is how a leaked PATCH's fleet.LoadTokens()
// ended up racing a test cleanup's fleet.ClearTokens() and producing an
// intermittent `-race` failure that landed on whichever unrelated test
// happened to be executing at the time.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/kameas-ai/kenaz-harness/core/contextbootstrap"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// newProgressSinkFleet returns a BootstrapClient whose PATCHes land on a
// server that records the order they ARRIVE in, holding each one open for
// `delay` first. The delay is what makes concurrent delivery observable: with
// one worker the arrival order is the emission order, while N racing
// goroutines interleave.
func newProgressSinkFleet(t *testing.T, delay time.Duration) (*corefleet.BootstrapClient, func() []int) {
	t.Helper()

	var mu sync.Mutex
	var arrived []int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/v1/context/bootstrap/") {
			body, _ := io.ReadAll(r.Body)
			// Sleep BEFORE recording and while holding no lock, so a second
			// in-flight PATCH is free to overtake this one.
			time.Sleep(delay)
			var p struct {
				NodesCreated int `json:"nodes_created"`
			}
			_ = json.Unmarshal(body, &p)
			mu.Lock()
			arrived = append(arrived, p.NodesCreated)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// In-memory keyring: the self-hosted Linux ARM64 runners have no D-Bus /
	// GNOME keyring, and Client.do calls LoadTokens on every request.
	keyring.MockInit()
	corefleet.SeedFleetConfigForTesting(srv.URL, corefleet.FleetConfig{
		Issuer: srv.URL, ClientID: "test", APIBaseURL: srv.URL, FetchedAt: time.Now().UTC(),
	})
	if err := corefleet.SaveTokens(corefleet.TokenSet{
		AccessToken: "at-prog", RefreshToken: "rt-prog", ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = corefleet.ClearTokens() })

	client := corefleet.NewClientForTesting(srv.URL)
	caps := corefleet.NewCapabilityPoller(client, t.TempDir())
	caps.ForceSetCurrentForTesting(corefleet.Capabilities{
		Tier:      "pro",
		Enabled:   map[corefleet.Capability]bool{corefleet.CapContextBootstrap: true},
		FetchedAt: time.Now(), Source: "test",
	})

	snapshot := func() []int {
		mu.Lock()
		defer mu.Unlock()
		out := make([]int, len(arrived))
		copy(out, arrived)
		return out
	}
	return corefleet.NewBootstrapClient(client, caps), snapshot
}

// Progress PATCHes must reach fleet in the order the engine emitted them, and
// drain() must not return until every one of them has completed.
//
// The 20 ms server-side hold means an implementation that fires a goroutine
// per emission has all five requests in flight at once and records them in
// completion order, which is arbitrary. Serialised delivery cannot.
func TestBootstrapProgressSink_DeliversInOrderAndDrainIsABarrier(t *testing.T) {
	fleetBoot, arrivals := newProgressSinkFleet(t, 20*time.Millisecond)

	sink := newBootstrapProgressSink(nil, fleetBoot)
	sink.SetRunID("run-order")

	const emissions = 5
	for i := 1; i <= emissions; i++ {
		sink.Emit(context.Background(), contextbootstrap.RunStatus{
			Phase:             contextbootstrap.RunPhaseExtraction,
			TotalNodesWritten: i,
		})
	}

	sink.drain()

	// drain() is a barrier: every accepted PATCH has completed by the time it
	// returns. Read the arrivals with no sleep and no retry — if this needs
	// either, drain() is not doing its job.
	got := arrivals()
	if len(got) != emissions {
		t.Fatalf("drain() returned with %d/%d PATCHes delivered: %v — drain is not a barrier",
			len(got), emissions, got)
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("progress PATCHes reached fleet out of order: got %v, want [1 2 3 4 5]\n"+
				"out-of-order progress lets fleet show a run going backwards, and lets a "+
				"straggler mutate a run dispatch() already finalised as completed", got)
		}
	}
}

// After drain() the run is over: a late Emit (an engine goroutine that had not
// been scheduled yet) must be dropped, not delivered and not panic on a closed
// channel. This is the property that keeps a progress PATCH from outliving the
// run — and, in tests, from outliving the test that started it.
func TestBootstrapProgressSink_EmitAfterDrainIsDropped(t *testing.T) {
	fleetBoot, arrivals := newProgressSinkFleet(t, 0)

	sink := newBootstrapProgressSink(nil, fleetBoot)
	sink.SetRunID("run-late")
	sink.Emit(context.Background(), contextbootstrap.RunStatus{
		Phase: contextbootstrap.RunPhaseExtraction, TotalNodesWritten: 1,
	})
	sink.drain()

	before := len(arrivals())
	sink.Emit(context.Background(), contextbootstrap.RunStatus{
		Phase: contextbootstrap.RunPhaseDone, TotalNodesWritten: 2,
	})
	sink.drain() // idempotent; also flushes anything a late Emit wrongly queued

	if after := len(arrivals()); after != before {
		t.Fatalf("an Emit after drain() reached fleet (%d → %d PATCHes): a progress update "+
			"survived the run it belongs to", before, after)
	}
}

// drain() on a sink that never had a run (fleet disabled, or no run id) must be
// a no-op rather than a nil-channel block.
func TestBootstrapProgressSink_DrainWithoutRunIsNoop(t *testing.T) {
	sink := newBootstrapProgressSink(nil, nil)
	sink.Emit(context.Background(), contextbootstrap.RunStatus{Phase: contextbootstrap.RunPhaseIdle})
	sink.drain()
	sink.drain()
}
