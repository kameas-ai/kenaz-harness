package settings

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

// A late enroll answer from before a sign-in says nothing about the new session:
// it must not show a good sign-in as degraded (review of the D164 guard).
func TestEnroll_SupersededByASignInIsNotASessionFailure(t *testing.T) {
	r, _ := newRemovalRig(t)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/config.json" {
			_, _ = w.Write([]byte(`{"api_base_url":"` + srv.URL + `"}`))
			return
		}
		r.setToken(jwtFor("sub-bob", "zitadel-org-2")) // the sign-in lands mid-enroll
		_, _ = w.Write([]byte(`{"user_id":"user-alice","org_id":"org-1","role":"org_member"}`))
	}))
	defer srv.Close()
	r.api.SetFleetClient(fleet.NewClientForTestingWithDataDir(srv.URL, r.dataDir), r.dataDir)

	if _, err := r.api.fleetEnroll(context.Background()); !errors.Is(err, fleet.ErrEnrollTokenChanged) {
		t.Fatalf("fleetEnroll err = %v", err)
	}
	r.api.fleet.mu.RLock()
	defer r.api.fleet.mu.RUnlock()
	if got := r.api.fleet.sess.lastErr; got != "" {
		t.Fatalf("session recorded a failure: %q", got)
	}
}
