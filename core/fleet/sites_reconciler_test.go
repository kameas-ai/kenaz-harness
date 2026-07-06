package fleet_test

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// fakeEnabled is a minimal EnabledStore for testing.
type fakeEnabled struct {
	entries map[string]bool
	saved   int
}

func newFakeEnabled() *fakeEnabled {
	return &fakeEnabled{entries: make(map[string]bool)}
}

func (f *fakeEnabled) Add(rec recipes.EnabledRecipe) {
	f.entries[rec.ID] = true
}

func (f *fakeEnabled) Remove(id string) bool {
	if f.entries[id] {
		delete(f.entries, id)
		return true
	}
	return false
}

func (f *fakeEnabled) Save(_ string) error {
	f.saved++
	return nil
}

func (f *fakeEnabled) Has(id string) bool { return f.entries[id] }

// TestSitesReconciler_EnableOnCapability verifies that the reconciler enables
// fleet-sites when sites_hosting appears and disables it when absent.
func TestSitesReconciler_EnableOnCapability(t *testing.T) {
	t.Parallel()
	poller := fleet.NewCapabilityPoller(nil, t.TempDir())
	enabled := newFakeEnabled()

	rec := fleet.NewSitesReconciler(poller, enabled, t.TempDir())
	rec.Start()

	// Simulate a capability snapshot WITH sites_hosting.
	withSites := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{fleet.CapSitesHosting: true},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	// Drive setCurrent indirectly through Refresh test hook — instead
	// test via the OnChange mechanism directly.
	//
	// Since setCurrent is unexported, we trigger it by calling the exported
	// test helper if available, or we use the internal struct.
	// Decision: expose a ForceSetCurrentForTesting method for tests only.
	poller.ForceSetCurrentForTesting(withSites)

	if !enabled.Has("fleet-sites") {
		t.Error("expected fleet-sites to be enabled after sites_hosting capability")
	}
	if enabled.saved == 0 {
		t.Error("expected Save to be called when enabling")
	}

	// Simulate a snapshot WITHOUT sites_hosting (expired / removed).
	withoutSites := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	poller.ForceSetCurrentForTesting(withoutSites)

	if enabled.Has("fleet-sites") {
		t.Error("expected fleet-sites to be disabled when sites_hosting absent")
	}
}

// TestSitesReconciler_Staleness verifies that a stale snapshot (>24h old)
// causes fleet-sites to be disabled.
func TestSitesReconciler_Staleness(t *testing.T) {
	t.Parallel()
	poller := fleet.NewCapabilityPoller(nil, t.TempDir())
	enabled := newFakeEnabled()

	rec := fleet.NewSitesReconciler(poller, enabled, t.TempDir())
	rec.Start()

	// Stale snapshot: FetchedAt > 24h ago, even though the key is present.
	stale := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{fleet.CapSitesHosting: true},
		FetchedAt: time.Now().Add(-25 * time.Hour), // older than capabilityTTL
		Source:    "cache",
	}
	poller.ForceSetCurrentForTesting(stale)

	// Has() returns false for stale snapshots, so reconciler should disable.
	if enabled.Has("fleet-sites") {
		t.Error("expected fleet-sites to be disabled for stale capability snapshot")
	}
}

// TestCapabilityPoller_OnChange_NoFire_WhenSetUnchanged verifies that listeners
// are NOT called when setCurrent is called with an identical enabled set.
func TestCapabilityPoller_OnChange_NoFire_WhenSetUnchanged(t *testing.T) {
	t.Parallel()
	poller := fleet.NewCapabilityPoller(nil, t.TempDir())
	fired := 0
	poller.OnChange(func(_ fleet.Capabilities) { fired++ })

	caps := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{fleet.CapSitesHosting: true},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	poller.ForceSetCurrentForTesting(caps)
	if fired != 1 {
		t.Errorf("expected 1 fire on first set, got %d", fired)
	}

	// Same enabled set — should NOT fire again.
	caps2 := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{fleet.CapSitesHosting: true},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	poller.ForceSetCurrentForTesting(caps2)
	if fired != 1 {
		t.Errorf("expected no additional fire on identical enabled set, got %d total", fired)
	}
}
