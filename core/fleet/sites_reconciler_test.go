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

// fleetSitesOnlySource reproduces the pre-UNIT-12 single-recipe scope for
// the two tests that predate the generalization — a fixture recipe named
// "fleet-sites" declaring the sites_hosting capability, so their
// assertions keep exercising the same recipe id they always have.
func fleetSitesOnlySource() []recipes.Recipe {
	return []recipes.Recipe{
		{ID: "fleet-sites", RequiredCapability: string(fleet.CapSitesHosting)},
	}
}

// TestSitesReconciler_EnableOnCapability verifies that the reconciler enables
// fleet-sites when sites_hosting appears and disables it when absent.
func TestSitesReconciler_EnableOnCapability(t *testing.T) {
	t.Parallel()
	poller := fleet.NewCapabilityPoller(nil, t.TempDir())
	enabled := newFakeEnabled()

	rec := fleet.NewSitesReconciler(poller, enabled, t.TempDir(), fleetSitesOnlySource)
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

	rec := fleet.NewSitesReconciler(poller, enabled, t.TempDir(), fleetSitesOnlySource)
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

// TestSitesReconciler_GenericRequiredCapability_NotFleetSites is
// connector-lifecycle-truth-01PMZ303 UNIT-12 / AC-009: a DIFFERENT recipe
// (not fleet-sites) declaring required_capability must be enabled and
// disabled by the same reconciler that governs fleet-sites — proving the
// reconciler actually reads Recipe.RequiredCapability generically rather
// than special-casing the one hardcoded id.
//
// Mutation (spec.md §7 AC-009): revert the reconciler to matching only
// fleetSitesRecipeID. This test's fixture recipe id
// ("second-capability-recipe") is deliberately different from
// "fleet-sites", so a hardcoded-id reconciler would never enable it and
// this test would fail.
func TestSitesReconciler_GenericRequiredCapability_NotFleetSites(t *testing.T) {
	t.Parallel()
	poller := fleet.NewCapabilityPoller(nil, t.TempDir())
	enabled := newFakeEnabled()

	const otherRecipeID = "second-capability-recipe"
	source := func() []recipes.Recipe {
		return []recipes.Recipe{
			{ID: otherRecipeID, RequiredCapability: string(fleet.CapSitesHosting)},
		}
	}
	rec := fleet.NewSitesReconciler(poller, enabled, t.TempDir(), source)
	rec.Start()

	withSites := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{fleet.CapSitesHosting: true},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	poller.ForceSetCurrentForTesting(withSites)
	if !enabled.Has(otherRecipeID) {
		t.Fatalf("expected %q to be enabled after its required capability appeared", otherRecipeID)
	}

	withoutSites := fleet.Capabilities{
		Enabled:   map[fleet.Capability]bool{},
		FetchedAt: time.Now(),
		Source:    "fleet",
	}
	poller.ForceSetCurrentForTesting(withoutSites)
	if enabled.Has(otherRecipeID) {
		t.Fatalf("expected %q to be disabled once its required capability disappeared", otherRecipeID)
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
