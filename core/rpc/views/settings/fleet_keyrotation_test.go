package settings

// bundle-key-rotation WP01 (Settings surface): fleet.ErrSigningKeyUnknown
// projected into FleetHealth / FleetConfigPullStatus.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestFleetHealth_UnknownSigningKey_SurfacesAsUnknownKey drives a REAL
// fleet.ConfigPoller against a bundle whose signed key_id names a key this
// build never pinned, then asserts the Settings projections:
// FleetHealth.ConfigSource == "unknown-key" (parallel to "no-key" for
// ErrSigningKeyNotConfigured), ConfigLastError and
// FleetConfigPullStatus.LastError both say "bundle signed with an unknown
// key", and distribution stays reported as enabled (the binary HAS keys).
func TestFleetHealth_UnknownSigningKey_SurfacesAsUnknownKey(t *testing.T) {
	pinned, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	foreignPub, foreignPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	restore := fleet.SetSigningKeyForTesting(pinned)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/configs", func(w http.ResponseWriter, r *http.Request) {
		b := &fleet.Bundle{BundleID: 1, IssuedAt: time.Now(), KeyID: fleet.SigningKeyID(foreignPub)}
		if err := fleet.SignBundleForTesting(b, foreignPriv); err != nil {
			t.Errorf("sign: %v", err)
			return
		}
		data, _ := json.Marshal(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := fleet.SaveTokens(fleet.TokenSet{
		AccessToken:  "at-test",
		RefreshToken: "rt-test",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	defer func() { _ = fleet.ClearTokens() }()
	fleet.SeedFleetConfigForTesting(srv.URL, fleet.FleetConfig{
		Issuer:     srv.URL,
		ClientID:   "test",
		APIBaseURL: srv.URL,
		FetchedAt:  time.Now().UTC(),
	})
	client := fleet.NewClientForTesting(srv.URL)

	state := &fleetState{}
	poller := fleet.NewConfigPoller(client, t.TempDir(), &compositeConfigApplier{state: state})
	state.configPoller = poller
	api := &API{fleet: state}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	poller.Start(ctx)
	defer poller.Stop()

	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) && !poller.Status().SigningKeyUnknown {
		time.Sleep(10 * time.Millisecond)
	}

	h, err := api.FleetHealth(context.Background())
	if err != nil {
		t.Fatalf("FleetHealth: %v", err)
	}
	if h.ConfigSource != "unknown-key" {
		t.Errorf("FleetHealth.ConfigSource = %q, want %q", h.ConfigSource, "unknown-key")
	}
	if !h.ConfigDistributionEnabled {
		t.Error("ConfigDistributionEnabled = false; the binary has a pinned key, so it must stay true")
	}
	if !strings.Contains(h.ConfigLastError, "bundle signed with an unknown key") {
		t.Errorf("FleetHealth.ConfigLastError = %q, want it to say the bundle was signed with an unknown key", h.ConfigLastError)
	}

	cs, err := api.FleetConfigPullStatus(context.Background())
	if err != nil {
		t.Fatalf("FleetConfigPullStatus: %v", err)
	}
	if !strings.Contains(cs.LastError, "bundle signed with an unknown key") {
		t.Errorf("FleetConfigPullStatus.LastError = %q, want the unknown-key message", cs.LastError)
	}
	if cs.LastAppliedID != 0 {
		t.Errorf("LastAppliedID = %d, want 0 (nothing applied)", cs.LastAppliedID)
	}
}
