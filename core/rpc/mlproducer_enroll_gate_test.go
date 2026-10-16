package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// The 2026-10 dev log: mlproducer.gate.changed open landed ~40 s after
// fleet.rpc.enroll.success. Boot order is the cause — the gate's first read
// runs before the boot enroll (not_enrolled, a transient closure), and the
// boot enroll runs no session-reset hook, so nothing re-read the gate until
// the refresher's next tick (TTL·5/6 = 50 s). The wiring now registers the
// gate's Invalidate on settings.OnFleetEnrolled. This drives the real
// settings API + fleet client: gate started before enroll, then a plain
// (non-sign-in) enroll, and the gate must open well inside one tick.
func TestMLWiring_EnrollOpensTheGateImmediately(t *testing.T) {
	if corefleet.Disabled() {
		t.Skip("HARNESS_FLEET_DISABLED=1: the rig needs fleet enabled")
	}
	sandboxUserConfigDir(t)
	f := newMLFleet(t, mlDevOrg)
	tok := mlTestJWT("sub-alice", mlTestZitadelOrg)
	corefleet.SetExternalTokenSource(func() string { return tok })
	t.Cleanup(func() { corefleet.SetExternalTokenSource(nil) })

	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := &settings.API{}
	api.SetFleetClient(corefleet.NewClientForTesting(f.srv.URL), dataDir)
	t.Cleanup(api.StopFleetBackground)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := api.CapabilityPoller().Refresh(ctx); err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	w := newMLProducerWiring(c, api)
	if w == nil {
		t.Fatal("newMLProducerWiring returned nil over a real DataDir")
	}
	t.Cleanup(w.shutdown)

	// Boot: the refresher's first read happens before any enroll.
	w.start(ctx)
	waitForCond(t, func() bool { return w.gate.Last().Reason == mlproducer.ReasonNotEnrolled },
		"pre-enroll decision not_enrolled")

	start := time.Now()
	if _, err := api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	// 5 s is a tenth of the old 50 s wait and far above a local re-read.
	deadline := time.Now().Add(5 * time.Second)
	for !w.gate.Last().Open {
		if time.Now().After(deadline) {
			t.Fatalf("gate still %q %v after a successful enroll; it must re-read immediately, not at the refresher tick",
				w.gate.Last().Reason, time.Since(start))
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitForCond(t, w.shipper.Running, "shipper starts once the gate opens")
}
