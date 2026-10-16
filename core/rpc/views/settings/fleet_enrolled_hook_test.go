package settings

// fleet_enrolled_hook_test.go — OnFleetEnrolled fires on EVERY successful
// enroll, including the boot / periodic enroll that runs no session-reset
// hook. The ML producer's consent gate re-reads on it; before this hook the
// gate waited out its refresher tick (~40-50 s) after a boot enroll.

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestOnFleetEnrolled_FiresOnPlainEnrollNotOnlySignIn(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	var enrolled, resets atomic.Int32
	r.api.OnFleetEnrolled(func() { enrolled.Add(1) })
	r.api.OnFleetSessionReset(func() { resets.Add(1) })

	// FleetRefreshIdentity is the same fleetEnroll the session supervisor's
	// boot / periodic step runs — no sign-in.
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if got := enrolled.Load(); got != 1 {
		t.Fatalf("OnFleetEnrolled ran %d times after a successful enroll, want 1", got)
	}
	if got := resets.Load(); got != 0 {
		t.Fatalf("a plain re-enroll ran %d session-reset hooks; those purge and must stay sign-in/out only", got)
	}
}

func TestOnFleetEnrolled_DoesNotFireOnFailedEnroll(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	r.fleet.mu.Lock()
	r.fleet.mode = "server_error"
	r.fleet.mu.Unlock()
	var enrolled atomic.Int32
	r.api.OnFleetEnrolled(func() { enrolled.Add(1) })
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err == nil {
		t.Fatal("fixture: enroll should have failed")
	}
	if got := enrolled.Load(); got != 0 {
		t.Fatalf("OnFleetEnrolled ran %d times after a FAILED enroll, want 0", got)
	}
}
