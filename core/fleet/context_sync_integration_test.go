package fleet

// context_sync_integration_test.go — fleet-context-sync-01NDFSEX15 WP09
//
// End-to-end integration tests covering all four context-sync sub-flows:
//
//  1. Session sync: EnableSync backfills events; Resume replays from fleet.
//  2. Project sync: EnableSync + SetArtifactClassOptions round-trip.
//  3. Team handoff: ShareSession posts to fleet; Inbox lists the item.
//     Full decrypt round-trip is deferred pending the v0.22 real X25519
//     persistent key pair (current receive-key path uses seed XOR, not ECDH).
//  4. Recovery: MintRecoveryCode + UseRecoveryCode + StoreContextSeed.
//
// All tests run against httptest fleet servers. No real network calls.

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// ── Shared helpers ────────────────────────────────────────────────────────

// integrationEmitter is a race-safe emitter for integration tests.
type integrationEmitter struct {
	mu      sync.Mutex
	emitted []contextaudit.Event
}

func (e *integrationEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.emitted = append(e.emitted, ev)
	return nil
}

func (e *integrationEmitter) snapshot() []contextaudit.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]contextaudit.Event, len(e.emitted))
	copy(out, e.emitted)
	return out
}

// stubTestSeed installs a deterministic 32-byte seed and cleans up on Cleanup.
func stubTestSeed(t *testing.T) []byte {
	t.Helper()
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(0xAB ^ i)
	}
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed setup: %v", err)
	}
	t.Cleanup(func() {
		_ = StoreContextSeed(make([]byte, seedSize))
	})
	return seed
}

// ── Sub-flow 1: Session sync ──────────────────────────────────────────────

func TestIntegration_SessionSync_EnableAndResume(t *testing.T) {
	var mu sync.Mutex
	// storedWireEvents holds the raw wire events received from Append calls
	// so we can re-serve them on Replay.
	var storedWireEvents json.RawMessage

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append":
			body, _ := io.ReadAll(r.Body)
			// Extract the events array so we can re-serve it on replay.
			var req struct {
				Events json.RawMessage `json:"events"`
			}
			if err := json.Unmarshal(body, &req); err == nil {
				mu.Lock()
				storedWireEvents = req.Events
				mu.Unlock()
			}
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/replay":
			mu.Lock()
			evs := storedWireEvents
			mu.Unlock()
			if evs == nil {
				evs = json.RawMessage(`[]`)
			}
			// replayResponse expects {"events":[...wireEvent...], "has_more": false}
			resp := json.RawMessage(`{"events":` + string(evs) + `,"has_more":false}`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(resp)

		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-session-sync",
		RefreshToken: "rt-session-sync",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	_ = stubTestSeed(t)

	c := makeTestClient(t, srv.URL)
	em := &integrationEmitter{}
	ss := NewSessionSyncer(c, em, nil)

	sessionID := "integ-session-001"
	t.Cleanup(func() { _ = setSessionSyncEnabled(sessionID, false) })

	events := []SessionEventRecord{
		{Seq: 1, Bytes: []byte(`{"role":"user","content":"ping"}`)},
		{Seq: 2, Bytes: []byte(`{"role":"assistant","content":"pong"}`)},
	}

	// Enable: backfill both events.
	if err := ss.EnableSync(context.Background(), sessionID, events); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}

	mu.Lock()
	got := storedWireEvents
	mu.Unlock()
	if got == nil {
		t.Error("EnableSync: no events were appended to fleet")
	}

	// Resume: replay events from fleet — verifies the round-trip encrypt/decrypt.
	var replayed []SessionEventRecord
	if err := ss.Resume(context.Background(), sessionID, 0, func(e SessionEventRecord) error {
		replayed = append(replayed, e)
		return nil
	}); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(replayed) != len(events) {
		t.Errorf("Resume: replayed %d events, want %d", len(replayed), len(events))
	}

	// Audit events should have been emitted.
	snap := em.snapshot()
	if len(snap) == 0 {
		t.Error("EnableSync: expected at least one audit event")
	}
}

// ── Sub-flow 2: Project sync ──────────────────────────────────────────────

func TestIntegration_ProjectSync_EnableAndSetArtifactClasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/replay":
			resp := replayResponse{Events: nil, HasMore: false}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-project-sync",
		RefreshToken: "rt-project-sync",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	_ = stubTestSeed(t)

	c := makeTestClient(t, srv.URL)
	em := &integrationEmitter{}
	ps := NewProjectSyncer(c, em, nil)

	projectID := "integ-project-001"
	t.Cleanup(func() { _ = setProjectSyncEnabled(projectID, false) })

	// Enable sync with default artifact class options.
	opts := DefaultArtifactClassOptions()
	if err := ps.EnableSync(context.Background(), projectID, nil, opts); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}

	// Verify sync is enabled via IsSyncEnabled.
	if !ps.IsSyncEnabled(projectID) {
		t.Error("expected project sync to be enabled after EnableSync")
	}

	// Override artifact class options: notes enabled, binaries disabled.
	custom := ArtifactClassOptions{Notes: true, Binaries: false, Memory: false}
	if err := ps.SetArtifactClassOptions(projectID, custom); err != nil {
		t.Fatalf("SetArtifactClassOptions: %v", err)
	}

	// Retrieve and verify.
	got := ps.GetArtifactClassOptions(projectID)
	if !got.Notes {
		t.Error("expected Notes to be enabled")
	}
	if got.Binaries {
		t.Error("expected Binaries to be disabled")
	}
	if got.Memory {
		t.Error("expected Memory to be disabled")
	}

	snap := em.snapshot()
	if len(snap) == 0 {
		t.Error("EnableSync: expected at least one audit event")
	}
}

// ── Sub-flow 3: Team handoff (send + inbox) ───────────────────────────────
//
// Tests ShareSession → fleet server → Inbox list.
//
// Note on full round-trip: AcceptShare uses a simplified receive-key derivation
// (seed XOR ephemeralPubKeyBytes, not standard ECDH). For a full round-trip both
// sides would need to share the same seed. The existing team_handoff_test.go unit
// tests cover AcceptShare in the internal package with matching setup.
// Here we test the observable behaviour: send posts successfully; inbox lists it.

func TestIntegration_TeamHandoff_ShareAndListInbox(t *testing.T) {
	type storedSend struct{ body []byte }
	var (
		sendsMu sync.Mutex
		sends   []storedSend
	)

	// Generate a valid X25519 recipient key pair for the test server.
	recipientPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate recipient X25519 key: %v", err)
	}
	recipientPubBytes := recipientPriv.PublicKey().Bytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/identity/public-key":
			// Return the valid X25519 public key for the test recipient.
			resp := publicKeyResponse{
				UserID:    r.URL.Query().Get("user_id"),
				PublicKey: recipientPubBytes,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/team/members":
			members := []TeamMember{
				{UserID: "u-recv", DisplayName: "Recv User"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(members)

		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/handoff/send":
			body, _ := io.ReadAll(r.Body)
			sendsMu.Lock()
			sends = append(sends, storedSend{body: body})
			sendsMu.Unlock()
			w.WriteHeader(http.StatusOK)

		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/handoff/inbox":
			sendsMu.Lock()
			n := len(sends)
			sendsMu.Unlock()
			inboxItems := make([]InboxItem, n)
			for i := range inboxItems {
				inboxItems[i] = InboxItem{
					InboxItemID:  "item-001",
					SessionID:    "shared-sess",
					SenderUserID: "u-sender",
					ReceivedAt:   time.Now(),
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(inboxItems)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-handoff",
		RefreshToken: "rt-handoff",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	_ = stubTestSeed(t)

	c := makeTestClient(t, srv.URL)
	em := &integrationEmitter{}
	hh := NewHandoffHandler(c, em, nil)

	// List team.
	members, err := hh.ListTeam(context.Background())
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(members) == 0 {
		t.Fatal("ListTeam: expected at least one member")
	}

	// Share a session.
	sessionEvents := []SessionEventRecord{
		{Seq: 1, Bytes: []byte(`{"role":"user","content":"shared msg"}`)},
	}
	if err := hh.ShareSession(context.Background(), "shared-sess", "u-recv", sessionEvents); err != nil {
		t.Fatalf("ShareSession: %v", err)
	}

	sendsMu.Lock()
	nSends := len(sends)
	sendsMu.Unlock()
	if nSends == 0 {
		t.Fatal("ShareSession: no POST to /api/v1/handoff/send observed")
	}

	// Inbox should contain the shared item.
	inbox, err := hh.Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(inbox) == 0 {
		t.Fatal("Inbox: expected at least one item after share")
	}

	// At least one audit event for the outbound share.
	snap := em.snapshot()
	if len(snap) == 0 {
		t.Error("ShareSession: expected at least one audit event")
	}
}

// ── Sub-flow 4: Recovery code round-trip ─────────────────────────────────

func TestIntegration_RecoveryCode_RoundTrip(t *testing.T) {
	// Install a known seed.
	originalSeed := stubTestSeed(t)

	// Mint a recovery code from the original seed.
	code, err := MintRecoveryCode(originalSeed)
	if err != nil {
		t.Fatalf("MintRecoveryCode: %v", err)
	}
	if code == "" {
		t.Fatal("MintRecoveryCode: empty code")
	}
	if len(code) < 20 {
		t.Errorf("MintRecoveryCode: code too short: %q", code)
	}

	// Decode the code back to a seed.
	recovered, err := UseRecoveryCode(code)
	if err != nil {
		t.Fatalf("UseRecoveryCode: %v", err)
	}
	if len(recovered) != seedSize {
		t.Fatalf("UseRecoveryCode: got %d bytes, want %d", len(recovered), seedSize)
	}
	if !bytes.Equal(recovered, originalSeed) {
		t.Error("UseRecoveryCode: recovered seed does not match original")
	}

	// Store the recovered seed and verify SeedKey() returns it.
	if err := StoreContextSeed(recovered); err != nil {
		t.Fatalf("StoreContextSeed (recovered): %v", err)
	}
	reSeed, err := SeedKey()
	if err != nil {
		t.Fatalf("SeedKey after recovery store: %v", err)
	}
	if !bytes.Equal(reSeed, recovered) {
		t.Error("SeedKey: stored seed mismatch after recovery")
	}
}
