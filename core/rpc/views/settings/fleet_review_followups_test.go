package settings

// fleet_review_followups_test.go — fleet-session-truth-01DOGF0A review
// follow-ups (F2, F5, F8) on the settings side.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/keyring"
)

// keyringSessionRig is a session rig whose tokens live in the token store
// (core/keyring's in-memory mock under go test — asserted), not an
// external source, so the desktop session cadence runs.
func keyringSessionRig(t *testing.T) *sessionRig {
	t.Helper()
	r := newSessionRig(t)
	fleet.SetExternalTokenSource(nil)
	if b := keyring.ActiveBackend(); b != "mock" {
		t.Fatalf("keyring backend = %q under go test; refusing to touch a real keychain", b)
	}
	if err := fleet.SaveTokens(fleet.TokenSet{
		AccessToken:  jwtFor("sub-alice", "zitadel-org-1"),
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = fleet.ClearTokens() })
	return r
}

// F2 pin: a session-expired event while the tokens are still usable (the
// wake-from-sleep refresh hiccup) must not be sticky — the next due cadence
// tick re-probes and, fleet accepting the token, the snapshot is signed_in.
func TestReview_F2_ExpiryWithUsableTokens_SelfHealsOnNextTick(t *testing.T) {
	r := keyringSessionRig(t)
	ctx := context.Background()
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	sessionExpiredTap{api: r.api}.Emit(fleet.TopicFleetSessionExpired,
		fleet.SessionExpiredPayload{Reason: "proactive refresh failed"})
	if v := snap(t, r.api); v.State != FleetSessionSignedOut {
		t.Fatalf("after the expiry event state = %q, want signed_out", v.State)
	}
	before := r.fleet.calls()
	r.api.sessionSupervisorStep(ctx, time.Now().Add(2*time.Hour)) // past any backoff
	if r.fleet.calls() == before {
		t.Fatal("the cadence never re-probed a recorded expiry — it was sticky until restart")
	}
	if v := snap(t, r.api); v.State != FleetSessionSignedIn || !v.TokensUsable {
		t.Fatalf("after a successful probe state=%q tokensUsable=%v, want signed_in", v.State, v.TokensUsable)
	}
}

// F2 belt-and-braces: any request fleet accepts clears a recorded expiry.
func TestReview_F2_AcceptedRequestClearsExpiry(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	sessionExpiredTap{api: r.api}.Emit(fleet.TopicFleetSessionExpired, fleet.SessionExpiredPayload{})
	if v := snap(t, r.api); v.State != FleetSessionSignedOut {
		t.Fatalf("state = %q, want signed_out after the event", v.State)
	}
	resp, err := r.api.fleetClient().Get(context.Background(), "/api/v1/enroll")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	_ = resp.Body.Close()
	if v := snap(t, r.api); v.State == FleetSessionSignedOut {
		t.Fatalf("fleet accepted the token but the snapshot still says %q/%q", v.State, v.Reason)
	}
}

// F5: a re-auth (sign-in while the old tokens still work) keeps the
// session's identity and capabilities in the snapshot and says so.
func TestReview_F5_SigningInWithUsableTokens_IsReauth(t *testing.T) {
	r := newSessionRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	r.api.fleet.mu.Lock()
	r.api.fleet.sess.signingIn = true
	r.api.fleet.mu.Unlock()
	v := snap(t, r.api)
	if v.State != FleetSessionSigningIn || !v.TokensUsable || v.Identity == nil {
		t.Fatalf("state=%q tokensUsable=%v identity=%v, want signing_in + usable + identity kept", v.State, v.TokensUsable, v.Identity)
	}
}

// F8: lane failures that only bump the count / error text are one
// transition, not one event per attempt.
func TestReview_F8_RepeatedLaneFailures_OneEvent(t *testing.T) {
	r, b := newEventRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	lanes := r.api.FleetSyncLanes()
	before := len(b.sessionEvents())
	for i := 3; i < 8; i++ {
		lanes.RecordFailure(fleet.LaneUnitPoll, "network", context.DeadlineExceeded, i, time.Now())
	}
	if n := len(b.sessionEvents()) - before; n != 1 {
		t.Fatalf("5 failed attempts of one degraded lane emitted %d events, want 1", n)
	}
}

// F8: sign-out never publishes a transient signed_in snapshot.
func TestReview_F8_SignOut_NoTransientSignedIn(t *testing.T) {
	r := keyringSessionRig(t) // ClearTokens really clears here
	b := &fakeSessionBroker{}
	r.api.SetLockdownBroker(b)
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	r.api.FleetSyncLanes().RecordSuccess(fleet.LaneTelemetry)
	before := len(b.sessionEvents())
	_ = r.api.FleetSignOut(context.Background())
	for _, ev := range b.sessionEvents()[before:] {
		if ev.State != FleetSessionSignedOut {
			t.Fatalf("sign-out published a %q snapshot", ev.State)
		}
	}
}
