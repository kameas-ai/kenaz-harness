package fleet_test

// integration_test.go exercises the capability surface end-to-end:
// fake fleet server → CapabilityPoller. This lives in the fleet_test
// package (external test package).
//
// The fleet-hosted-LLM adapter integration test was removed alongside the
// fleet-hosted-LLM surface (harness-fleet-sync-activation-01NSYNC01,
// dead-code cleanup); what remains is the capability-surface coverage.
//
// Mission: fleet-capability-surface-01NDFSEX09, WP07.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

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
		fleet.CapLauncherUpdates,
		fleet.CapEmergencyLockdown,
		fleet.CapSSOSAML,
		fleet.CapQuarterlyAttestationReports,
	}
	for _, cap := range required {
		if _, ok := seen[cap]; !ok {
			t.Errorf("AllCapabilities() missing required capability %q", cap)
		}
	}

	// Verify CapSitesHosting is present (sites-foundation-01NSITE04 WP01).
	if _, ok := seen[fleet.CapSitesHosting]; !ok {
		t.Errorf("AllCapabilities() missing CapSitesHosting")
	}

	// Total count gate: we defined exactly 22 (21 baseline + CapSitesHosting).
	if len(all) != 22 {
		t.Errorf("AllCapabilities() len = %d, want 22", len(all))
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
	if cur.Has(fleet.CapLauncherUpdates) {
		t.Error("default-deny: Has(CapLauncherUpdates) = true, want false")
	}
}
