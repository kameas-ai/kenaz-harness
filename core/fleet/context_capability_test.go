package fleet

// context_capability_test.go — WP04: capability gating + offline no-op.
//
// Tests that:
//   - Team publish without CapSharedTeamGraph → ErrCapabilityNotInTier.
//   - Org publish without CapSharedTeamGraph → ErrCapabilityNotInTier.
//   - Pull without CapSharedTeamGraph → (0, nil) — local-only, no error.
//   - Signed-out (ErrNotSignedIn from transport) → (0, nil).
//   - Promote without cap → ErrCapabilityNotInTier.
//
// (fleet-context-graph-sync-01NDFSEX17 WP04)

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contextpack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// makeCapPollerWithNoCap creates a poller with no capabilities enabled.
func makeCapPollerWithNoCap(t *testing.T) *CapabilityPoller {
	t.Helper()
	return &CapabilityPoller{
		done: make(chan struct{}),
		current: Capabilities{
			Tier:      "pro",
			Enabled:   map[Capability]bool{},
			FetchedAt: time.Now(),
			Source:    "test",
		},
	}
}

// TestCapabilityGate_TeamPushDenied verifies that pushing a team entry
// when CapSharedTeamGraph is absent returns ErrCapabilityNotInTier.
func TestCapabilityGate_TeamPushDenied(t *testing.T) {
	// Use a live server so we can verify the gate fires before the request.
	// The gate should prevent any network call.
	var networkHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-gate1",
		RefreshToken: "rt-gate1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithNoCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	teamID := "tid-1"
	entry := ContextNodeEntry{ID: "e1", Layer: contextpack.LayerTeam, Kind: "fact", Title: "T", Body: "B", TeamID: &teamID, Version: 1}

	ctx := context.Background()
	_, err := syncer.PushEntry(ctx, entry, nil)
	if !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("PushEntry(team, no cap): want ErrCapabilityNotInTier, got %v", err)
	}
	if networkHit {
		t.Error("capability gate should prevent network call, but server was hit")
	}
}

// TestCapabilityGate_OrgPushDenied verifies that pushing an org entry
// when CapSharedTeamGraph is absent returns ErrCapabilityNotInTier.
func TestCapabilityGate_OrgPushDenied(t *testing.T) {
	var networkHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-gate2",
		RefreshToken: "rt-gate2",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithNoCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	entry := ContextNodeEntry{ID: "e2", Layer: contextpack.LayerOrg, Kind: "fact", Title: "O", Body: "B", Version: 1}

	ctx := context.Background()
	_, err := syncer.PushEntry(ctx, entry, nil)
	if !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("PushEntry(org, no cap): want ErrCapabilityNotInTier, got %v", err)
	}
	if networkHit {
		t.Error("capability gate should prevent network call, but server was hit")
	}
}

// TestCapabilityGate_PullNoCapLocalOnly verifies that PullDelta returns
// (0, nil) when the team-graph capability is absent — local-only fallback
// with no error (FR-104).
func TestCapabilityGate_PullNoCapLocalOnly(t *testing.T) {
	var networkHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-gate3",
		RefreshToken: "rt-gate3",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithNoCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Errorf("PullDelta(no cap): want nil error, got %v", err)
	}
	if n != 0 {
		t.Errorf("PullDelta(no cap): want 0, got %d", n)
	}
	if networkHit {
		t.Error("no-cap pull should not hit network")
	}
}

// TestCapabilityGate_PromoteDenied verifies that Promote returns
// ErrCapabilityNotInTier when the team-graph cap is absent.
func TestCapabilityGate_PromoteDenied(t *testing.T) {
	var networkHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		networkHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-gate4",
		RefreshToken: "rt-gate4",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	client := makeTestClient(t, srv.URL)
	caps := makeCapPollerWithNoCap(t)
	syncer := NewContextGraphSyncer(client, t.TempDir(), caps)

	ctx := context.Background()
	_, err := syncer.Promote(ctx, "some-node-id")
	if !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("Promote(no cap): want ErrCapabilityNotInTier, got %v", err)
	}
	if networkHit {
		t.Error("capability gate should prevent network call")
	}
}

// TestOffline_PullReturnsLocalOnly verifies that a nil client PullDelta
// returns (0, nil) — consistent with local-only Contexts behavior (NFR-005).
func TestOffline_PullReturnsLocalOnly(t *testing.T) {
	syncer := NewContextGraphSyncer(nil, t.TempDir(), nil)
	ctx := context.Background()
	n, err := syncer.PullDelta(ctx)
	if err != nil {
		t.Errorf("offline pull: want nil, got %v", err)
	}
	if n != 0 {
		t.Errorf("offline pull: want 0, got %d", n)
	}
}

// TestCapabilityGate_StaleCacheDefaultDeny verifies that a capability snapshot
// older than capabilityTTL is treated as default-deny.
func TestCapabilityGate_StaleCacheDefaultDeny(t *testing.T) {
	p := &CapabilityPoller{
		done: make(chan struct{}),
		current: Capabilities{
			Tier: "team",
			Enabled: map[Capability]bool{
				CapSharedTeamGraph: true,
			},
			FetchedAt: time.Now().Add(-capabilityTTL - time.Minute), // stale
			Source:    "cache",
		},
	}
	syncer := NewContextGraphSyncer(nil, t.TempDir(), p)
	teamID := "tid-stale"
	entry := ContextNodeEntry{ID: "e-stale", Layer: contextpack.LayerTeam, Kind: "fact", Title: "T", Body: "B", TeamID: &teamID, Version: 1}
	ctx := context.Background()
	_, err := syncer.PushEntry(ctx, entry, nil)
	if !errors.Is(err, ErrCapabilityNotInTier) {
		t.Errorf("stale cache: want ErrCapabilityNotInTier, got %v", err)
	}
}
