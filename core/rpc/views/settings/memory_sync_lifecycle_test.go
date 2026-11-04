package settings

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/memory"
)

func newTestMemorySync(t *testing.T, client *fleet.Client) *fleet.MemorySync {
	t.Helper()
	dir := t.TempDir()
	st, err := memory.NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatal(err)
	}
	clock, err := memory.NewHLC("node-test", memory.HLCStatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := fleet.NewMemorySync(fleet.MemorySyncConfig{
		Client: client, Store: st.(memory.SyncStore), Clock: clock, DataDir: dir, Interval: time.Hour,
		Identity: func() (fleet.Identity, error) { return fleet.Identity{OrgID: "o", UserID: "u"}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ms.Stop)
	return ms
}

// TestMemorySync_SignOutStopsLane_SignInRestartsIt pins both settings-side
// call sites of the memory sync lifecycle: sign-out (StopFleetBackground)
// stops the loop, and sign-in (startFleetBackgroundLocked) restarts it —
// otherwise memory sync stays dead after sign-out/sign-in until restart.
// Mutation: remove either ms.Stop() in StopFleetBackground or ms.Start in
// startFleetBackgroundLocked and this fails.
func TestMemorySync_SignOutStopsLane_SignInRestartsIt(t *testing.T) {
	r, _ := newRemovalRig(t)
	ctx := context.Background()
	ms := newTestMemorySync(t, r.api.fleetClient())
	r.api.SetMemorySync(ms)
	ms.Start(ctx) // what core/rpc's buildMemorySync does at boot
	if !ms.Running() {
		t.Fatal("lane not running after boot Start")
	}

	_ = r.api.FleetSignOut(ctx)
	if ms.Running() {
		t.Fatal("sign-out left the memory sync loop running")
	}

	r.api.fleet.signInFlow = func(context.Context, fleet.EnvProfile) (fleet.TokenSet, error) {
		return fleet.TokenSet{AccessToken: jwtFor("sub-alice", "zitadel-org-1")}, nil
	}
	if _, err := r.api.FleetSignIn(ctx); err != nil {
		t.Fatalf("sign-in: %v", err)
	}
	if !ms.Running() {
		t.Fatal("sign-in did not restart the memory sync loop")
	}
}

// TestSignIn_DropsThePreviousIdentityBeforeTheNewTokens (legal D164): memory
// sync reads the identity file to decide whose notes it may send, so a failed
// enroll must not leave the old account's name beside new tokens.
// Mutation: remove the os.Remove in runSignIn and this fails.
func TestSignIn_DropsThePreviousIdentityBeforeTheNewTokens(t *testing.T) {
	r, _ := newRemovalRig(t)
	ctx := context.Background()
	if err := fleet.SaveIdentity(r.dataDir, fleet.Identity{OrgID: "org-a", UserID: "user-a"}); err != nil {
		t.Fatal(err)
	}
	r.api.SetFleetClient(fleet.NewClientForTestingWithDataDir("http://127.0.0.1:1", r.dataDir), r.dataDir)
	var atFlow string
	r.api.fleet.signInFlow = func(context.Context, fleet.EnvProfile) (fleet.TokenSet, error) {
		atFlow = fleet.CurrentAccountKey(r.dataDir)
		return fleet.TokenSet{AccessToken: jwtFor("sub-bob", "zitadel-org-2")}, nil
	}
	_, _ = r.api.FleetSignIn(ctx) // enroll can't reach anything
	if atFlow != "org-a:user-a" {
		t.Fatalf("setup: identity during the browser flow = %q; a cancelled sign-in must keep it", atFlow)
	}
	if got := fleet.CurrentAccountKey(r.dataDir); got != "" {
		t.Fatalf("identity after a failed enroll = %q, want none", got)
	}
}
