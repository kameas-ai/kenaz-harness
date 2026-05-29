package fleet_test

// integration_test.go exercises the capability surface end-to-end:
// fake fleet server → CapabilityPoller → ConditionalAdapter gate in
// core/llm/registry. This lives in the fleet_test package (external
// test package) so it can import both core/fleet and core/llm/registry
// without creating an import cycle.
//
// Tests that start goroutines are skipped under -short.
//
// Mission: fleet-capability-surface-01NDFSEX09, WP07.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/fleet"
	"github.com/sigil-tech/kaneaz-harness/core/llm/fleet_hosted"
	"github.com/sigil-tech/kaneaz-harness/core/llm/registry"
)

// fakeFleetServer is a minimal httptest fleet server that serves the
// capabilities endpoint. The tier and enabled map are swappable at
// runtime (atomic) to simulate tier changes.
type fakeFleetServer struct {
	srv     *httptest.Server
	tier    atomic.Value // stores string
	enabled atomic.Value // stores map[string]bool
	// calls counts how many times GET /api/v1/me/capabilities was hit.
	calls atomic.Int64
}

func newFakeFleetServer(t *testing.T) *fakeFleetServer {
	t.Helper()
	fs := &fakeFleetServer{}
	fs.tier.Store("pro")
	fs.enabled.Store(map[string]bool{string(fleet.CapHostedInference): true})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fs.calls.Add(1)
		tier := fs.tier.Load().(string)
		enabled := fs.enabled.Load().(map[string]bool)
		wire := map[string]interface{}{
			"tier":         tier,
			"capabilities": enabled,
			"fetched_at":   time.Now().UTC(),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wire)
	})
	fs.srv = httptest.NewServer(mux)
	t.Cleanup(fs.srv.Close)
	return fs
}

// setEnabled replaces the capabilities map entirely.
func (fs *fakeFleetServer) setEnabled(m map[string]bool) { fs.enabled.Store(m) }

// skipIfNoKeychain installs a fake token set and skips the test when the
// OS keychain is unavailable (CI without a keychain daemon).
func skipIfNoKeychain(t *testing.T) {
	t.Helper()
	ts := fleet.TokenSet{
		AccessToken:  "integration-fake-token",
		RefreshToken: "integration-fake-refresh",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := fleet.SaveTokens(ts); err != nil {
		t.Skipf("skip: OS keychain unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = fleet.ClearTokens() })
}

// buildIntegrationPoller constructs a CapabilityPoller that talks to srv
// using an httpClient wired directly to the httptest server (no TLS).
// It uses fleet.BuildClientForTesting which is declared in export_test.go.
func buildIntegrationPoller(t *testing.T, srv *httptest.Server) *fleet.CapabilityPoller {
	t.Helper()
	c := fleet.BuildClientForTesting(srv.URL, srv.Client())
	dir := t.TempDir()
	return fleet.NewCapabilityPoller(c, dir)
}

// TestIntegration_CapabilityGatesFleetHostedAdapter is the primary
// end-to-end test:
//
//  1. Fake fleet returns hosted_inference=true.
//  2. Poller.Refresh() populates the in-memory snapshot.
//  3. A fleet_hosted.Adapter wired via the EnabledFunc reads the
//     snapshot — Enabled() should be true.
//  4. Server changes hosted_inference=false; another Refresh().
//  5. Adapter.Enabled() now returns false — the capability change
//     propagated without restart.
func TestIntegration_CapabilityGatesFleetHostedAdapter(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test skipped in -short mode")
	}

	fs := newFakeFleetServer(t)
	skipIfNoKeychain(t)

	poller := buildIntegrationPoller(t, fs.srv)

	// Step 1: Refresh with hosted_inference=true.
	caps, err := poller.Refresh(context.Background())
	if err != nil {
		t.Fatalf("initial Refresh: %v", err)
	}
	if !caps.Has(fleet.CapHostedInference) {
		t.Fatalf("after first Refresh: CapHostedInference = false, want true (tier=%q, source=%q)", caps.Tier, caps.Source)
	}

	// Step 2: Wire a fleet_hosted adapter whose enabledFn reads from the
	// poller's current snapshot.
	enabledFn := func() bool {
		cur := poller.Current()
		return cur.Has(fleet.CapHostedInference)
	}
	bearerFn := func() (string, error) { return "integration-fake-token", nil }
	adapter := fleet_hosted.New(fs.srv.URL, bearerFn, enabledFn)

	if !adapter.Enabled() {
		t.Error("adapter.Enabled() = false after pro-tier refresh, want true")
	}

	// Step 3: Register in a real registry and confirm Adapter() returns it.
	reg, err := registry.New(registry.Options{})
	if err != nil {
		t.Fatalf("registry.New: %v", err)
	}
	reg.RegisterAdapter(adapter)

	if got := reg.Adapter(fleet_hosted.Kind); got == nil {
		t.Error("registry.Adapter(fleet-hosted) = nil, want non-nil (capability is enabled)")
	}

	// Step 4: Simulate tier downgrade — fleet no longer grants hosted_inference.
	fs.setEnabled(map[string]bool{string(fleet.CapHostedInference): false})

	_, err = poller.Refresh(context.Background())
	if err != nil {
		t.Fatalf("post-downgrade Refresh: %v", err)
	}

	// Step 5: Adapter.Enabled() propagates at resolve time — no restart needed.
	if adapter.Enabled() {
		t.Error("adapter.Enabled() = true after downgrade, want false")
	}

	// registry.Adapter should now return nil because ConditionalAdapter.Enabled() is false.
	if got := reg.Adapter(fleet_hosted.Kind); got != nil {
		t.Error("registry.Adapter(fleet-hosted) != nil after downgrade, want nil")
	}

	if calls := fs.calls.Load(); calls < 2 {
		t.Errorf("server was called %d times, want ≥ 2", calls)
	}
}

// TestIntegration_AllCapabilityKeys verifies AllCapabilities() for uniqueness
// and count, and checks sentinel keys are present.
func TestIntegration_AllCapabilityKeys(t *testing.T) {
	all := fleet.AllCapabilities()
	if len(all) == 0 {
		t.Fatal("AllCapabilities() returned empty")
	}

	// Build a set for uniqueness checking.
	seen := make(map[fleet.Capability]int, len(all))
	for i, c := range all {
		if prev, dup := seen[c]; dup {
			t.Errorf("AllCapabilities()[%d] = %q is a duplicate of [%d]", i, c, prev)
		}
		seen[c] = i
	}

	// Verify known sentinel keys are present (guards against accidental deletion).
	required := []fleet.Capability{
		fleet.CapHostedInference,
		fleet.CapEmergencyLockdown,
		fleet.CapSSOSAML,
		fleet.CapQuarterlyAttestationReports,
	}
	for _, cap := range required {
		if _, ok := seen[cap]; !ok {
			t.Errorf("AllCapabilities() missing required capability %q", cap)
		}
	}

	// Total count gate: we defined exactly 25.
	if len(all) != 25 {
		t.Errorf("AllCapabilities() len = %d, want 25", len(all))
	}
}

// TestIntegration_PollerDefaultDenyBeforeRefresh verifies that a freshly
// constructed poller (not yet refreshed) returns default-deny.
func TestIntegration_PollerDefaultDenyBeforeRefresh(t *testing.T) {
	dir := t.TempDir()
	p := fleet.NewCapabilityPoller(nil, dir)
	cur := p.Current()
	if cur.Source != "default-deny" {
		t.Errorf("Source = %q, want 'default-deny'", cur.Source)
	}
	if cur.Has(fleet.CapHostedInference) {
		t.Error("default-deny: Has(CapHostedInference) = true, want false")
	}
}
