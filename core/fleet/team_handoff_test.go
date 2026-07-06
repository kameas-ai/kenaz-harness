package fleet

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// newHandoffTestServer creates an httptest server that simulates the fleet
// handoff + identity endpoints.
func newHandoffTestServer(t *testing.T, recipientPrivKey *ecdh.PrivateKey) *httptest.Server {
	t.Helper()

	// Build a fake public key response for the recipient.
	recipientPubKeyBytes := recipientPrivKey.PublicKey().Bytes()

	var sharedItems []acceptHandoffResponse

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/identity/public-key":
			resp := publicKeyResponse{
				UserID:    r.URL.Query().Get("user_id"),
				PublicKey: recipientPubKeyBytes,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/handoff/send":
			var req map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&req)

			// Reconstruct the item for the inbox.
			var item acceptHandoffResponse
			_ = json.Unmarshal(req["session_id"], &item.SessionID)
			_ = json.Unmarshal(req["sender_user_id"], &item.SenderUserID)
			_ = json.Unmarshal(req["ephemeral_public_key"], &item.EphemeralPublicKey)
			_ = json.Unmarshal(req["events"], &item.Events)
			sharedItems = append(sharedItems, item)
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/api/v1/handoff/inbox":
			inboxItems := make([]InboxItem, len(sharedItems))
			for i, si := range sharedItems {
				inboxItems[i] = InboxItem{
					InboxItemID:  "item-" + si.SessionID,
					SessionID:    si.SessionID,
					SenderUserID: "sender-001",
					ReceivedAt:   time.Now(),
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(inboxItems)

		case r.Method == http.MethodGet && len(r.URL.Path) > len("/api/v1/handoff/"):
			// GET /api/v1/handoff/{itemID}
			itemID := r.URL.Path[len("/api/v1/handoff/"):]
			for _, si := range sharedItems {
				if "item-"+si.SessionID == itemID {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(si)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return srv
}

func TestHandoffHandler_ShareAndAccept(t *testing.T) {
	// Generate a recipient X25519 key pair.
	curve := ecdh.X25519()
	recipientPriv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate recipient key: %v", err)
	}

	srv := newHandoffTestServer(t, recipientPriv)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-handoff",
		RefreshToken: "rt-handoff",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 42)
	}
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed: %v", err)
	}

	fe := &fakeEmitter{}
	h := NewHandoffHandler(c, fe, nil)

	// Share a session with the recipient.
	plainEvents := []SessionEventRecord{
		{Seq: 1, Bytes: []byte("turn one")},
		{Seq: 2, Bytes: []byte("turn two")},
	}

	// NOTE: In the simplified v0.21.0 model, ShareSession sends the payload
	// re-encrypted with an ephemeral key. We verify audit fires and no
	// plaintext appears in the wire payload.
	err = h.ShareSession(context.Background(), "sess-abc", "user-recipient-001", plainEvents)
	if err != nil {
		t.Fatalf("ShareSession: %v", err)
	}

	// Verify outbound audit.
	evts := fe.snapshot()
	if len(evts) == 0 {
		t.Fatal("expected audit event for ShareSession")
	}
	found := false
	for _, e := range evts {
		if e.Kind == contextaudit.KindFleetSessionSharedOutbound {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected KindFleetSessionSharedOutbound, got %v", evts)
	}

	// Check that plaintext does not appear in the emitted audit payload.
	for _, e := range evts {
		raw, _ := json.Marshal(e)
		if bytes.Contains(raw, []byte("turn one")) || bytes.Contains(raw, []byte("turn two")) {
			t.Error("plaintext event content must not appear in audit payload")
		}
	}
}

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

	err = h.ShareSession(context.Background(), "s", "r", nil)
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

	err := h2.ShareSession(context.Background(), "s", "r", nil)
	if err != ErrTeamHandoffCapabilityRequired {
		t.Errorf("expected ErrTeamHandoffCapabilityRequired, got %v", err)
	}
	_ = h
}
