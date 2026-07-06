package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

// setupSessionSyncServer creates an httptest server that accepts context
// append + delete calls and returns the SessionSyncer under test.
func setupSessionSyncServer(t *testing.T) (*httptest.Server, *SessionSyncer, *fakeEmitter) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/context/replay":
			// Return empty for now.
			resp := replayResponse{Events: nil, HasMore: false}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	stubTokens(t, TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	fe := &fakeEmitter{}
	ss := NewSessionSyncer(c, fe, nil)

	// Install a test seed so keyring lookups work.
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 3)
	}
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed: %v", err)
	}
	t.Cleanup(func() {
		_ = setSessionSyncEnabled("test-session-123", false)
	})

	return srv, ss, fe
}

// ── EnableSync ─────────────────────────────────────────────────────────────

func TestSessionSyncer_EnableSync_Backfill(t *testing.T) {
	var appendCount int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append" {
			mu.Lock()
			appendCount++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	fe := &fakeEmitter{}
	ss := NewSessionSyncer(c, fe, nil)

	seed := make([]byte, seedSize)
	_ = StoreContextSeed(seed)

	events := []SessionEventRecord{
		{Seq: 1, Bytes: []byte(`{"role":"user","content":"hello"}`)},
		{Seq: 2, Bytes: []byte(`{"role":"assistant","content":"hi"}`)},
	}

	sessionID := "session-backfill-test"
	t.Cleanup(func() { _ = setSessionSyncEnabled(sessionID, false) })

	if err := ss.EnableSync(context.Background(), sessionID, events); err != nil {
		t.Fatalf("EnableSync: %v", err)
	}

	mu.Lock()
	ac := appendCount
	mu.Unlock()

	if ac == 0 {
		t.Error("expected at least one append POST")
	}
	if !isSessionSyncEnabled(sessionID) {
		t.Error("expected sync flag to be set after EnableSync")
	}

	// Audit event should fire.
	evts := fe.snapshot()
	if len(evts) == 0 {
		t.Fatal("expected audit event for EnableSync")
	}
	if evts[0].Kind != contextaudit.KindFleetSessionSyncEnabled {
		t.Errorf("audit kind = %q, want fleet.session_sync_enabled", evts[0].Kind)
	}
}

// ── DisableSync ─────────────────────────────────────────────────────────────

func TestSessionSyncer_DisableSync(t *testing.T) {
	srv, ss, fe := setupSessionSyncServer(t)
	defer srv.Close()

	sessionID := "session-disable-test"
	_ = setSessionSyncEnabled(sessionID, true)
	t.Cleanup(func() { _ = setSessionSyncEnabled(sessionID, false) })

	if err := ss.DisableSync(context.Background(), sessionID); err != nil {
		t.Fatalf("DisableSync: %v", err)
	}
	if isSessionSyncEnabled(sessionID) {
		t.Error("expected sync flag to be cleared after DisableSync")
	}
	evts := fe.snapshot()
	if len(evts) == 0 || evts[0].Kind != contextaudit.KindFleetSessionSyncDisabled {
		t.Errorf("expected KindFleetSessionSyncDisabled audit event")
	}
}

// ── AppendEvent skips when sync is off ─────────────────────────────────────

func TestSessionSyncer_AppendEvent_SkipsWhenDisabled(t *testing.T) {
	var appendCalled bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/context/append" {
			appendCalled = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	ss := NewSessionSyncer(c, nil, nil)

	seed := make([]byte, seedSize)
	_ = StoreContextSeed(seed)

	sessionID := "session-skip-test"
	_ = setSessionSyncEnabled(sessionID, false)

	err := ss.AppendEvent(context.Background(), sessionID, SessionEventRecord{Seq: 1, Bytes: []byte("data")})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if appendCalled {
		t.Error("AppendEvent should not POST when sync is disabled")
	}
}

// ── AppendEvent fires when sync is on ──────────────────────────────────────

func TestSessionSyncer_AppendEvent_FiresWhenEnabled(t *testing.T) {
	var (
		appendCalls int
		mu          sync.Mutex
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/append" {
			mu.Lock()
			appendCalls++
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-append",
		RefreshToken: "rt-append",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	ss := NewSessionSyncer(c, nil, nil)

	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 7)
	}
	if err := StoreContextSeed(seed); err != nil {
		t.Fatalf("StoreContextSeed: %v", err)
	}

	sessionID := "session-append-enabled-test"
	// Enable sync so isSessionSyncEnabled returns true.
	if err := setSessionSyncEnabled(sessionID, true); err != nil {
		t.Fatalf("setSessionSyncEnabled: %v", err)
	}
	t.Cleanup(func() { _ = setSessionSyncEnabled(sessionID, false) })

	err := ss.AppendEvent(context.Background(), sessionID, SessionEventRecord{
		Seq:   1,
		Bytes: []byte(`{"role":"user","content":"<opaque>"}`),
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	mu.Lock()
	calls := appendCalls
	mu.Unlock()

	if calls == 0 {
		t.Error("AppendEvent should POST to fleet when session sync is enabled, but no POST was observed")
	}
}

// ── DeleteRemote ───────────────────────────────────────────────────────────

func TestSessionSyncer_DeleteRemote_EmitsAudit(t *testing.T) {
	srv, ss, fe := setupSessionSyncServer(t)
	defer srv.Close()

	sessionID := "session-delete-test"
	_ = setSessionSyncEnabled(sessionID, true)

	if err := ss.DeleteRemote(context.Background(), sessionID); err != nil {
		t.Fatalf("DeleteRemote: %v", err)
	}
	evts := fe.snapshot()
	found := false
	for _, e := range evts {
		if e.Kind == contextaudit.KindFleetSessionSyncRemoteDeleted {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected KindFleetSessionSyncRemoteDeleted audit event")
	}
}

// ── NopClient no-ops ────────────────────────────────────────────────────────

func TestSessionSyncer_NopClient_ReturnsDisabled(t *testing.T) {
	ss := NewSessionSyncer(nopClientInstance(), nil, nil)

	err := ss.EnableSync(context.Background(), "x", nil)
	if err != ErrFleetDisabled {
		t.Errorf("EnableSync on nop: got %v, want ErrFleetDisabled", err)
	}
	err = ss.DisableSync(context.Background(), "x")
	if err != ErrFleetDisabled {
		t.Errorf("DisableSync on nop: got %v, want ErrFleetDisabled", err)
	}
}
