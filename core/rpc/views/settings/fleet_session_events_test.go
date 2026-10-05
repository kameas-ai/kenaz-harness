package settings

// fleet_session_events_test.go — fleet-session-truth-01DOGF0A WP02.
// Each session transition emits exactly one fleet:session-changed carrying
// the new state; a non-transition emits nothing.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

type emitted struct {
	topic   string
	payload any
}

// fakeSessionBroker receives Emit from whatever goroutine publishes (the
// poller listener, the client's session-expired path) and is read by the
// test body: mutex + snapshot (CLAUDE.md race-safe fakes).
type fakeSessionBroker struct {
	mu  sync.Mutex
	got []emitted
}

func (b *fakeSessionBroker) Emit(topic string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.got = append(b.got, emitted{topic, payload})
}

func (b *fakeSessionBroker) snapshot() []emitted {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]emitted(nil), b.got...)
}

// sessionEvents returns the FleetSession payloads emitted so far.
func (b *fakeSessionBroker) sessionEvents() []FleetSessionView {
	var out []FleetSessionView
	for _, e := range b.snapshot() {
		if e.topic == fleet.TopicFleetSessionChanged {
			out = append(out, e.payload.(FleetSessionView))
		}
	}
	return out
}

func newEventRig(t *testing.T) (*sessionRig, *fakeSessionBroker) {
	t.Helper()
	r := newSessionRig(t)
	b := &fakeSessionBroker{}
	r.api.SetLockdownBroker(b)
	return r, b
}

func TestFleetSessionEvents_EachTransitionEmitsExactlyOne(t *testing.T) {
	r, b := newEventRig(t)
	ctx := context.Background()
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))

	// 1. enroll ok → signed_in
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	ev := b.sessionEvents()
	if len(ev) != 1 || ev[0].State != FleetSessionSignedIn {
		t.Fatalf("after enroll ok: events=%d last=%+v, want exactly one signed_in", len(ev), last(ev))
	}

	// 2. the same outcome again is not a transition → no event
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll 2: %v", err)
	}
	if n := len(b.sessionEvents()); n != 1 {
		t.Fatalf("a repeated identical enroll emitted %d events total, want still 1", n)
	}

	// 3. enroll fails → exactly one degraded
	r.fleet.setMode("server_error")
	_, _ = r.api.FleetRefreshIdentity(ctx)
	ev = b.sessionEvents()
	if len(ev) != 2 || ev[1].State != FleetSessionDegraded || ev[1].Reason != FleetReasonServerError {
		t.Fatalf("after enroll failure: events=%d last=%+v, want 2nd event degraded/server_error", len(ev), last(ev))
	}
	if ev[1].Identity == nil {
		t.Fatalf("degraded event dropped the identity: %+v", ev[1])
	}

	// 4. sign-out → exactly one signed_out
	r.setToken("")
	_ = r.api.FleetSignOut(ctx)
	ev = b.sessionEvents()
	if len(ev) != 3 || ev[2].State != FleetSessionSignedOut || ev[2].Identity != nil {
		t.Fatalf("after sign-out: events=%d last=%+v, want 3rd event signed_out without identity", len(ev), last(ev))
	}
}

// FR-8 / P-10 backend half: a capability arriving republishes the snapshot
// with the new set — and the listener, which reads the poller back, must not
// deadlock (the old setCurrent ran listeners under the write lock).
func TestFleetSessionEvents_CapabilityChange_EmitsWithoutDeadlock(t *testing.T) {
	r, b := newEventRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	before := len(b.sessionEvents())

	done := make(chan struct{})
	go func() {
		r.api.CapabilityPoller().ForceSetCurrentForTesting(fleet.Capabilities{
			Tier:    "enterprise",
			Enabled: map[fleet.Capability]bool{fleet.CapSharedTeamGraph: true},
			Source:  "fleet",
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("capability OnChange deadlocked: listener re-entered the poller lock")
	}
	ev := b.sessionEvents()
	if len(ev) != before+1 {
		t.Fatalf("capability change emitted %d events, want exactly 1", len(ev)-before)
	}
	if !ev[len(ev)-1].Capabilities.Enabled[string(fleet.CapSharedTeamGraph)] {
		t.Fatalf("event capabilities = %+v, want shared_team_graph", ev[len(ev)-1].Capabilities)
	}
}

// A server-side session death (the client's fleet:session:expired) is a
// transition to signed_out even though tokens are still stored. The raw
// event is consumed in-process (review F8: SessionExpiredBanner reads the
// snapshot now), so it must NOT reach the broker.
func TestFleetSessionEvents_SessionExpiredTap(t *testing.T) {
	r, b := newEventRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	tap := sessionExpiredTap{api: r.api, inner: b}
	tap.Emit(fleet.TopicFleetSessionExpired, fleet.SessionExpiredPayload{Reason: "refresh failed"})

	var sawExpired bool
	for _, e := range b.snapshot() {
		if e.topic == fleet.TopicFleetSessionExpired {
			sawExpired = true
		}
	}
	if sawExpired {
		t.Fatalf("tap forwarded fleet:session:expired; the snapshot is its only UI carrier now")
	}
	ev := b.sessionEvents()
	if l := last(ev); l == nil || l.State != FleetSessionSignedOut || l.Reason != FleetReasonSessionExpired {
		t.Fatalf("last session event = %+v, want signed_out/session_expired", l)
	}
}

func TestSessionEnrollDue(t *testing.T) {
	now := time.Now()
	usable := fleet.TokenState{Present: true, AccessValid: true}
	cases := []struct {
		name string
		tr   sessionTrack
		ts   fleet.TokenState
		want bool
	}{
		{"no tokens", sessionTrack{}, fleet.TokenState{}, false},
		{"never attempted", sessionTrack{}, usable, true},
		{"healthy, recent", sessionTrack{lastAttemptAt: now.Add(-time.Minute)}, usable, false},
		{"healthy, interval elapsed", sessionTrack{lastAttemptAt: now.Add(-sessionRefreshInterval)}, usable, true},
		{"failing, before backoff", sessionTrack{lastAttemptAt: now, failures: 2, nextRetryAt: now.Add(time.Minute)}, usable, false},
		{"failing, backoff elapsed", sessionTrack{lastAttemptAt: now.Add(-time.Hour), failures: 2, nextRetryAt: now.Add(-time.Second)}, usable, true},
		{"not provisioned stops", sessionTrack{lastAttemptAt: now.Add(-time.Hour), failures: 1, autoRetryStopped: true}, usable, false},
		{"expired, before re-probe backoff", sessionTrack{expired: true, failures: 1, nextRetryAt: now.Add(time.Minute)}, usable, false},
		{"expired, re-probe due (review F2: not sticky)", sessionTrack{expired: true, failures: 1, nextRetryAt: now.Add(-time.Second)}, usable, true},
		{"signing in", sessionTrack{signingIn: true}, usable, false},
	}
	for _, c := range cases {
		if got := sessionEnrollDue(c.tr, c.ts, now); got != c.want {
			t.Errorf("%s: due=%v, want %v", c.name, got, c.want)
		}
	}
}

// The cadence tick re-enrolls when due and publishes; a second tick inside
// the interval does neither.
func TestSessionSupervisorStep_EnrollsWhenDue(t *testing.T) {
	r, b := newEventRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	// The served-mode guard stands the cadence down while an external token
	// source is active (the served supervisor owns enroll there). This rig
	// uses one, so drive the step logic directly through the enroll path.
	if !fleet.ExternalTokenSourceActive() {
		t.Fatal("rig expected to run with an external token source")
	}
	r.api.sessionSupervisorStep(context.Background(), time.Now())
	if c := r.fleet.calls(); c != 0 {
		t.Fatalf("supervisor enrolled %d times under an external token source, want 0 (served owns enroll)", c)
	}
	// It still publishes the current truth.
	if len(b.sessionEvents()) != 1 {
		t.Fatalf("supervisor step published %d events, want 1", len(b.sessionEvents()))
	}
}

func last(ev []FleetSessionView) *FleetSessionView {
	if len(ev) == 0 {
		return nil
	}
	return &ev[len(ev)-1]
}
