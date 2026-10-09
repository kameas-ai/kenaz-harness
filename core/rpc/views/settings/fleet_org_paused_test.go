package settings

// fleet_org_paused_test.go — the staff "pause paid features" org state
// (kenaz-fleet #206) on the FleetSession snapshot, and the one
// OnOrgUnpaused fan-out that reopens what org_paused held.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestFleetSession_OrgPaused_SnapshotAndUnpauseFanOut(t *testing.T) {
	r, b := newEventRig(t)
	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(context.Background()); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	var mu sync.Mutex
	reopened := 0
	r.api.OnOrgUnpaused(func() { mu.Lock(); reopened++; mu.Unlock() })

	// The capability poll reports the pause: tier kept, every key false.
	p := r.api.CapabilityPoller()
	p.ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "team", Enabled: map[fleet.Capability]bool{fleet.CapContextSync: false},
		FetchedAt: time.Now(), Source: "fleet", Paused: true, PausedCategory: "billing_review",
	})
	v := snap(t, r.api)
	if !v.Paused || v.PausedCategory != "billing_review" {
		t.Fatalf("snapshot paused=%v category=%q, want paused/billing_review", v.Paused, v.PausedCategory)
	}
	if v.State != FleetSessionSignedIn || v.Reason != "" {
		t.Fatalf("state=%q reason=%q — a pause is neither degraded nor signed out", v.State, v.Reason)
	}
	if v.Capabilities.Tier != "team" {
		t.Fatalf("tier = %q, want the kept tier", v.Capabilities.Tier)
	}
	waitUntil(t, func() bool {
		ev := b.sessionEvents()
		return len(ev) > 0 && ev[len(ev)-1].Paused
	}, "a fleet:session-changed carrying paused:true")

	// The pause lifts: one fan-out, every registered reopen runs.
	p.ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "team", Enabled: map[fleet.Capability]bool{fleet.CapContextSync: true},
		FetchedAt: time.Now(), Source: "fleet",
	})
	mu.Lock()
	n := reopened
	mu.Unlock()
	if n != 1 {
		t.Fatalf("OnOrgUnpaused hooks ran %d times, want 1", n)
	}
	if v := snap(t, r.api); v.Paused || v.PausedCategory != "" {
		t.Fatalf("snapshot still paused after unpause: %+v", v)
	}
	waitUntil(t, func() bool {
		ev := b.sessionEvents()
		return len(ev) > 0 && !ev[len(ev)-1].Paused
	}, "a fleet:session-changed carrying paused:false")
}

func TestClassifyEnrollError_OrgPaused_IsNotSignedOut(t *testing.T) {
	reason, expired := classifyEnrollError(&fleet.OrgPausedError{PausedCategory: "legal"})
	if reason != FleetReasonOrgPaused || expired {
		t.Fatalf("classify = (%q, %v), want (org_paused, not expired)", reason, expired)
	}
}

func TestMemorySyncView_CarriesOrgPaused(t *testing.T) {
	v := memorySyncView(fleet.MemorySyncStatus{OrgPaused: true, PausedCategory: "abuse"})
	if !v.OrgPaused || v.PausedCategory != "abuse" {
		t.Fatalf("view = %+v", v)
	}
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Review fix 2: the pause is session-scoped. After sign-out → sign-in (a
// possibly different org) the snapshot is not paused until a real poll for
// the NEW session says so — neither the client's old pause state (reset on
// the session-reset hook, as core/rpc wires it) nor a default-deny poller
// may carry it over.
func TestFleetSession_OrgPause_DoesNotSurviveSignOutSignIn(t *testing.T) {
	r, _ := newEventRig(t)
	ctx := context.Background()
	r.api.fleet.mu.RLock()
	client := r.api.fleet.client
	r.api.fleet.mu.RUnlock()
	r.api.OnFleetSessionReset(client.ResetOrgPause) // production wiring (core/rpc/api.go)

	r.setToken(jwtFor("sub-alice", "zitadel-org-1"))
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	r.api.CapabilityPoller().ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "team", Enabled: map[fleet.Capability]bool{}, FetchedAt: time.Now(),
		Source: "fleet", Paused: true, PausedCategory: "security",
	})
	if v := snap(t, r.api); !v.Paused {
		t.Fatal("precondition: snapshot not paused")
	}

	r.setToken("")
	if err := r.api.FleetSignOut(ctx); err != nil {
		t.Fatalf("sign-out: %v", err)
	}
	// New session, different org; the background restarts with a fresh
	// (default-deny) poller, as after a real sign-in.
	r.setToken(jwtFor("sub-bob", "zitadel-org-2"))
	r.api.fleet.mu.Lock()
	r.api.startFleetBackgroundLocked()
	r.api.fleet.mu.Unlock()
	if _, err := r.api.FleetRefreshIdentity(ctx); err != nil {
		t.Fatalf("enroll 2: %v", err)
	}
	if v := snap(t, r.api); v.Paused || v.PausedCategory != "" {
		t.Fatalf("new session inherited the old pause: paused=%v category=%q caps.source=%q",
			v.Paused, v.PausedCategory, v.Capabilities.Source)
	}
	if client.OrgPause().Paused {
		t.Fatal("client pause state survived the session reset")
	}

	// Belt-and-braces: even a pause observed before the first real poll is
	// not shown while the poller is still default-deny.
	client.ObserveCapabilitiesPause(true, "abuse")
	if v := snap(t, r.api); v.Paused {
		t.Fatal("snapshot paused while the poller is default-deny")
	}
	// A real poll for this session says paused → shown.
	r.api.CapabilityPoller().ForceSetCurrentForTesting(fleet.Capabilities{
		Tier: "pro", Enabled: map[fleet.Capability]bool{}, FetchedAt: time.Now(),
		Source: "fleet", Paused: true, PausedCategory: "abuse",
	})
	if v := snap(t, r.api); !v.Paused || v.PausedCategory != "abuse" {
		t.Fatalf("snapshot = paused %v / %q, want abuse", v.Paused, v.PausedCategory)
	}
}
