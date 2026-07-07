package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// makeOnboardingTestServer returns a test HTTP server that handles the
// fleet onboarding endpoints and a cleanup function.
func makeOnboardingTestServer(t *testing.T, getBody any, patchSink *OnboardingStateWire) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me/onboarding" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(getBody)
		case http.MethodPatch:
			if patchSink != nil {
				_ = json.NewDecoder(r.Body).Decode(patchSink)
			}
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

// makeOnboardingClient builds a fleet Client pointed at srvURL with
// a fake token pre-seeded in the keychain.
func makeOnboardingClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	SeedFleetConfigForTesting(srvURL, FleetConfig{
		Issuer:     srvURL,
		ClientID:   "test",
		APIBaseURL: srvURL,
		FetchedAt:  time.Now().UTC(),
	})
	if err := SaveTokens(TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })
	return &Client{
		profile: EnvProfile{
			Name:           EnvLocal,
			ZitadelIssuer:  srvURL,
			NativeClientID: "test",
			FleetBaseURL:   srvURL,
			OIDCScopes:     DefaultOIDCScopes,
		},
		httpClient:  &http.Client{},
		httpTimeout: 5 * time.Second,
	}
}

func TestGetOnboardingState_OK(t *testing.T) {
	// Not parallel: uses keychain singleton.
	want := OnboardingStateWire{
		Schema:             1,
		ProviderConfigured: true,
		AccountConnected:   true,
	}
	srv := makeOnboardingTestServer(t, want, nil)
	defer srv.Close()

	c := makeOnboardingClient(t, srv.URL)
	got, err := c.GetOnboardingState(context.Background())
	if err != nil {
		t.Fatalf("GetOnboardingState: %v", err)
	}
	if !got.ProviderConfigured || !got.AccountConnected {
		t.Errorf("got %+v, want provider_configured=true account_connected=true", got)
	}
}

func TestGetOnboardingState_NopClient(t *testing.T) {
	t.Parallel()
	c := nopClientInstance()
	_, err := c.GetOnboardingState(context.Background())
	if err != ErrFleetDisabled {
		t.Errorf("want ErrFleetDisabled, got %v", err)
	}
}

func TestPatchOnboardingState_OK(t *testing.T) {
	// Not parallel: uses keychain singleton.
	var received OnboardingStateWire
	srv := makeOnboardingTestServer(t, nil, &received)
	defer srv.Close()

	c := makeOnboardingClient(t, srv.URL)
	err := c.PatchOnboardingState(context.Background(), OnboardingStateWire{
		Schema:        1,
		ContextSynced: true,
	})
	if err != nil {
		t.Fatalf("PatchOnboardingState: %v", err)
	}
	if !received.ContextSynced {
		t.Errorf("server did not receive context_synced=true; got %+v", received)
	}
}

func TestPatchOnboardingState_NopClient(t *testing.T) {
	t.Parallel()
	c := nopClientInstance()
	err := c.PatchOnboardingState(context.Background(), OnboardingStateWire{Schema: 1})
	if err != ErrFleetDisabled {
		t.Errorf("want ErrFleetDisabled, got %v", err)
	}
}

// TestContextGraphSyncer_FirstPushHook verifies that SetFirstPushHook fires
// exactly once after the first successful PushEntry, even on multiple pushes.
func TestContextGraphSyncer_FirstPushHook(t *testing.T) {
	// Not parallel: uses keychain singleton.

	var fireCount atomic.Int32

	// Build a server that handles context pushes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/context/push" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ContextPushResult{AcceptedNodes: 1})
			return
		}
		// Stub token refresh endpoint so the auth flow doesn't fail.
		if r.URL.Path == "/oauth/v2/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "at-refreshed",
				"refresh_token": "rt-refreshed",
				"expires_in":    3600,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := makeOnboardingClient(t, srv.URL)

	// Build a CapabilityPoller seeded with CapSharedTeamGraph enabled.
	caps := NewCapabilityPoller(c, t.TempDir())
	caps.ForceSetCurrentForTesting(Capabilities{
		Tier: "team",
		Enabled: map[Capability]bool{
			CapSharedTeamGraph: true,
		},
		FetchedAt: time.Now(),
		Source:    "test",
	})

	syncer := NewContextGraphSyncer(c, t.TempDir(), caps)

	done := make(chan struct{}, 1)
	syncer.SetFirstPushHook(func() {
		if fireCount.Add(1) == 1 {
			close(done)
		}
	})

	entry := ContextNodeEntry{
		ID:    "node-1",
		Layer: "team",
		Kind:  "entity",
		Title: "Test",
		Body:  "body",
	}

	// First push: hook should fire.
	_, err := syncer.PushEntry(context.Background(), entry, nil)
	if err != nil {
		t.Fatalf("PushEntry #1: %v", err)
	}

	// Wait for hook.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first-push hook did not fire within 2s")
	}

	// Second push: hook must NOT fire again.
	entry.ID = "node-2"
	_, err = syncer.PushEntry(context.Background(), entry, nil)
	if err != nil {
		t.Fatalf("PushEntry #2: %v", err)
	}

	if n := fireCount.Load(); n != 1 {
		t.Errorf("hook fired %d times, want exactly 1", n)
	}
}
