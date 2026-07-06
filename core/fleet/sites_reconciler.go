// Package fleet — sites_reconciler.go
//
// SitesReconciler listens to CapabilityPoller.OnChange and enables or
// disables the "fleet-sites" recipe in the EnabledRecipes list when the
// sites_hosting capability appears or disappears (including 24 h TTL
// expiry, which makes Has() return false).
//
// Wire-up (see core/core.go):
//
//	poller := fleet.NewCapabilityPoller(fc, dataDir)
//	enabled, _ := recipes.LoadEnabled(dataDir)
//	fleet.NewSitesReconciler(poller, enabled, dataDir).Start()
//	poller.Start(ctx)
//
// The reconciler holds no goroutine of its own — it fires synchronously
// inside CapabilityPoller.setCurrent under the poller's write lock.
// Implementations must be fast and non-blocking (disk writes on the
// reconciler path are acceptable because the poller background goroutine
// does not block the main request path).
//
// Mission: sites-mcp-server-01NSITE05 WP04.
package fleet

import (
	"log"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

const fleetSitesRecipeID = "fleet-sites"

// EnabledStore is the subset of recipes.EnabledRecipes used by the reconciler.
// Defined as an interface so tests can substitute a fake.
type EnabledStore interface {
	Add(rec recipes.EnabledRecipe)
	Remove(id string) bool
	Save(dataDir string) error
}

// SitesReconciler observes capability changes and keeps the fleet-sites
// recipe entry in sync with the sites_hosting capability state.
type SitesReconciler struct {
	poller  *CapabilityPoller
	enabled EnabledStore
	dataDir string
}

// NewSitesReconciler constructs a reconciler. Call Start() to register
// the OnChange listener.
func NewSitesReconciler(poller *CapabilityPoller, enabled EnabledStore, dataDir string) *SitesReconciler {
	return &SitesReconciler{
		poller:  poller,
		enabled: enabled,
		dataDir: dataDir,
	}
}

// Start registers the OnChange listener on the poller. It is safe to call
// before or after Start(ctx) on the poller.
func (r *SitesReconciler) Start() {
	r.poller.OnChange(r.reconcile)
}

// reconcile is called by the poller whenever the enabled-capability set changes.
// It enables the fleet-sites recipe when sites_hosting is present and disables
// it when absent (including when the snapshot is stale — Has() returns false
// after 24 h per capabilityTTL).
func (r *SitesReconciler) reconcile(caps Capabilities) {
	if caps.Has(CapSitesHosting) {
		r.enable()
	} else {
		r.disable()
	}
}

func (r *SitesReconciler) enable() {
	r.enabled.Add(recipes.EnabledRecipe{
		ID:        fleetSitesRecipeID,
		EnabledAt: time.Now().UTC(),
	})
	if err := r.enabled.Save(r.dataDir); err != nil {
		log.Printf("sites reconciler: save enabled recipes: %v", err)
	}
	log.Printf("sites reconciler: fleet-sites recipe enabled (sites_hosting capability granted)")
}

func (r *SitesReconciler) disable() {
	if r.enabled.Remove(fleetSitesRecipeID) {
		if err := r.enabled.Save(r.dataDir); err != nil {
			log.Printf("sites reconciler: save enabled recipes: %v", err)
		}
		log.Printf("sites reconciler: fleet-sites recipe disabled (sites_hosting capability absent or stale)")
	}
}
