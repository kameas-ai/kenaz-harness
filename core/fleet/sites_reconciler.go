// Package fleet — sites_reconciler.go
//
// CapabilityRecipeReconciler (constructed via NewSitesReconciler for
// backward-compat naming with its original sites-only scope) listens to
// CapabilityPoller.OnChange and enables or disables every recipe whose
// declared Recipe.RequiredCapability matches a fleet capability's presence
// (including 24 h TTL expiry, which makes Has() return false).
//
// connector-lifecycle-truth-01PMZ303 UNIT-12 (FR-008, RequiredCapability):
// before this, the reconciler only ever consulted the hardcoded pair
// (fleetSitesRecipeID, CapSitesHosting) — Recipe.RequiredCapability's own
// docstring asserted "the capability reconciler enables the recipe when
// the capability is present" for ANY recipe that declares the field, but
// the reconciler never read the field at all. Generalized to iterate the
// recipe source's full catalog and key each recipe's enable/disable
// decision on its own RequiredCapability value, cast directly to
// fleet.Capability (both are plain strings — "sites_hosting" is the one
// value either side declares today).
//
// Wire-up (see core/rpc/api.go):
//
//	poller := fleet.NewCapabilityPoller(fc, dataDir)
//	enabled, _ := recipes.LoadEnabled(dataDir)
//	fleet.NewSitesReconciler(poller, enabled, dataDir, recipeSource).Start()
//	poller.Start(ctx)
//
// The reconciler holds no goroutine of its own — it fires synchronously
// inside CapabilityPoller.setCurrent under the poller's write lock.
// Implementations must be fast and non-blocking (disk writes on the
// reconciler path are acceptable because the poller background goroutine
// does not block the main request path).
//
// Mission: sites-mcp-server-01NSITE05 WP04; generalized by
// connector-lifecycle-truth-01PMZ303 UNIT-12.
package fleet

import (
	"log"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
)

// fleetSitesRecipeID names the one shipped recipe this reconciler governed
// before UNIT-12 generalized it. Retained only as a documentation anchor —
// no code branches on it anymore.
const fleetSitesRecipeID = "fleet-sites"

// EnabledStore is the subset of recipes.EnabledRecipes used by the reconciler.
// Defined as an interface so tests can substitute a fake.
type EnabledStore interface {
	Add(rec recipes.EnabledRecipe)
	Remove(id string) bool
	Save(dataDir string) error
}

// RecipeSource returns the current full recipe catalog (shipped + registry
// + user, in whatever precedence order the caller's merge applies) that
// the reconciler should scan for RequiredCapability declarations. Called
// fresh on every capability change so a catalog update (e.g. a curated
// registry refresh) is picked up without restarting the reconciler.
type RecipeSource func() []recipes.Recipe

// SitesReconciler observes capability changes and keeps every
// RequiredCapability-declaring recipe's enabled state in sync with the
// fleet capability set. The name predates UNIT-12's generalization
// (it started as a fleet-sites-only reconciler); kept to avoid an
// unrelated rename churning every call site.
type SitesReconciler struct {
	poller  *CapabilityPoller
	enabled EnabledStore
	dataDir string
	recipes RecipeSource
}

// NewSitesReconciler constructs a reconciler. Call Start() to register
// the OnChange listener. recipeSource may be nil, in which case the
// reconciler observes no recipes (matches the pre-UNIT-12 shape for any
// caller that has not been updated to supply one — none remain in
// production, but this keeps the zero value safe rather than panicking).
func NewSitesReconciler(poller *CapabilityPoller, enabled EnabledStore, dataDir string, recipeSource RecipeSource) *SitesReconciler {
	return &SitesReconciler{
		poller:  poller,
		enabled: enabled,
		dataDir: dataDir,
		recipes: recipeSource,
	}
}

// Start registers the OnChange listener on the poller. It is safe to call
// before or after Start(ctx) on the poller.
func (r *SitesReconciler) Start() {
	r.poller.OnChange(r.reconcile)
}

// reconcile is called by the poller whenever the enabled-capability set
// changes. For every recipe in the current catalog that declares a
// RequiredCapability, it enables the recipe when that capability is
// present and disables it when absent (including when the snapshot is
// stale — Has() returns false after 24 h per capabilityTTL).
func (r *SitesReconciler) reconcile(caps Capabilities) {
	if r.recipes == nil {
		return
	}
	dirty := false
	for _, rec := range r.recipes() {
		if rec.RequiredCapability == "" {
			continue
		}
		if caps.Has(Capability(rec.RequiredCapability)) {
			if r.enableRecipe(rec.ID, rec.RequiredCapability) {
				dirty = true
			}
		} else {
			if r.disableRecipe(rec.ID, rec.RequiredCapability) {
				dirty = true
			}
		}
	}
	if dirty {
		if err := r.enabled.Save(r.dataDir); err != nil {
			log.Printf("sites reconciler: save enabled recipes: %v", err)
		}
	}
}

// enableRecipe adds id to the enabled list. Returns true if this call
// changed anything worth persisting — always true today (Add is
// idempotent-looking but the reconciler still saves on every reconcile
// pass to match the pre-UNIT-12 per-event save behaviour for the single
// fleet-sites recipe).
func (r *SitesReconciler) enableRecipe(id, capability string) bool {
	r.enabled.Add(recipes.EnabledRecipe{
		ID:        id,
		EnabledAt: time.Now().UTC(),
	})
	log.Printf("sites reconciler: recipe %q enabled (%s capability granted)", id, capability)
	return true
}

// disableRecipe removes id from the enabled list. Returns true only when
// it was actually present (so reconcile does not log/save a no-op).
func (r *SitesReconciler) disableRecipe(id, capability string) bool {
	if r.enabled.Remove(id) {
		log.Printf("sites reconciler: recipe %q disabled (%s capability absent or stale)", id, capability)
		return true
	}
	return false
}
