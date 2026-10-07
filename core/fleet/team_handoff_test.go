package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

)

// The v2 send is covered by handoff_send_test.go and the end-to-end
// share → accept round trip by handoff_roundtrip_test.go
// (device-keys-handoff-01DEVKH01 WP04/WP05).

// ── ListTeam ─────────────────────────────────────────────────────────────────

func TestHandoffHandler_ListTeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/team/members" {
			members := []TeamMember{
				{UserID: "u1", DisplayName: "Alice", Email: "alice@example.com"},
				{UserID: "u2", DisplayName: "Bob", Email: "bob@example.com"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(members)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-team",
		RefreshToken: "rt-team",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	h := NewHandoffHandler(c, nil, nil)

	members, err := h.ListTeam(context.Background())
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(members) != 2 {
		t.Errorf("got %d members, want 2", len(members))
	}
}

// ── NopClient ────────────────────────────────────────────────────────────────

func TestHandoffHandler_NopClient(t *testing.T) {
	h := NewHandoffHandler(nopClientInstance(), nil, nil)

	_, err := h.ListTeam(context.Background())
	if err != ErrFleetDisabled {
		t.Errorf("ListTeam on nop: got %v, want ErrFleetDisabled", err)
	}

	_, err = h.ShareSession(context.Background(), "s", "r", nil)
	if err != ErrFleetDisabled {
		t.Errorf("ShareSession on nop: got %v, want ErrFleetDisabled", err)
	}

	_, err = h.Inbox(context.Background())
	if err != ErrFleetDisabled {
		t.Errorf("Inbox on nop: got %v, want ErrFleetDisabled", err)
	}

	_, err = h.AcceptShare(context.Background(), "item")
	if err != ErrFleetDisabled {
		t.Errorf("AcceptShare on nop: got %v, want ErrFleetDisabled", err)
	}
}

// ── capability gate ───────────────────────────────────────────────────────────

func TestHandoffHandler_CapabilityGate(t *testing.T) {
	// Build a Capabilities snapshot that does NOT include CapTeamSessionHandoff.
	caps := &Capabilities{Enabled: map[Capability]bool{}}

	h := NewHandoffHandler(nopClientInstance(), nil, caps)
	// Even though client is nop, capability check fires first.
	// But nop returns ErrFleetDisabled before capability check.
	// Use a real client to test capability gate.
	stubTokens(t, TokenSet{
		AccessToken:  "at-cap",
		RefreshToken: "rt-cap",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := makeTestClient(t, srv.URL)
	h2 := NewHandoffHandler(c, nil, caps)

	seed := make([]byte, seedSize)
	_ = StoreContextSeed(seed)

	_, err := h2.ShareSession(context.Background(), "s", "r", nil)
	if err != ErrTeamHandoffCapabilityRequired {
		t.Errorf("expected ErrTeamHandoffCapabilityRequired, got %v", err)
	}
	_ = h
}
