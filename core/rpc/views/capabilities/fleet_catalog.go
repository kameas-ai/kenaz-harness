package capabilities

import (
	"context"
	"strconv"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/install"
)

// FleetCatalog is the fleet catalog seam fleet-backed providers use. core/rpc
// implements it over *fleet.Client (the only chassis package allowed to hold
// one — scripts/ci/check-no-fleet-imports.sh), so this package stays
// fleet-free.
type FleetCatalog interface {
	// List returns catalog metadata for kind (no payloads).
	List(ctx context.Context, kind string) ([]CatalogEntry, error)
	// Fetch returns the signed payload of id@version. Called only from a
	// provider's Verify step.
	Fetch(ctx context.Context, id, version string) (payload []byte, signature string, err error)
	// UnavailableReason classifies a List error into a stable reason code:
	// "signed_out", "fleet_disabled" or "error".
	UnavailableReason(err error) string
}

// CatalogEntry is one fleet catalog item as a provider needs it.
type CatalogEntry struct {
	ID          string
	Slug        string
	Version     string
	Description string
	// Visibility is "private" | "team" | "org_public".
	Visibility string
	// Lifecycle, LifecycleReason, SupersededBy: the version's org lifecycle
	// from the unsigned catalog wire (skill-library-01SKLIB01). Lifecycle
	// is "" for a pre-0114 fleet (active).
	Lifecycle       string
	LifecycleReason string
	SupersededBy    string
}

// applyLifecycle copies a catalog entry's lifecycle onto its item. "active"
// is not labelled (the UI chips only what deviates from normal).
func applyLifecycle(it *install.Item, e CatalogEntry) {
	if e.Lifecycle == "" || e.Lifecycle == "active" {
		return
	}
	it.Lifecycle = e.Lifecycle
	it.LifecycleReason = e.LifecycleReason
	it.SupersededBy = e.SupersededBy
}

// catalogSource maps fleet visibility onto the FR-4 source filter.
func catalogSource(visibility string) install.Source {
	if visibility == "org_public" {
		return install.SourceOrgCatalog
	}
	return install.SourceTeamCatalog
}

// fleetUnavailable is the pair of reason rows a fleet-backed provider
// returns when the catalog cannot be listed: one per fleet source, so the
// org and team source chips each say why they are empty (P-5).
func fleetUnavailable(cat FleetCatalog, err error) []install.Unavailable {
	reason := cat.UnavailableReason(err)
	return []install.Unavailable{
		{Source: install.SourceOrgCatalog, Reason: reason, Message: err.Error()},
		{Source: install.SourceTeamCatalog, Reason: reason, Message: err.Error()},
	}
}

// newestByID keeps the newest version of each catalog id.
func newestByID(entries []CatalogEntry) map[string]CatalogEntry {
	out := make(map[string]CatalogEntry, len(entries))
	for _, e := range entries {
		if cur, ok := out[e.ID]; !ok || versionLess(cur.Version, e.Version) {
			out[e.ID] = e
		}
	}
	return out
}

// versionLess orders dotted numeric versions ("1.2.10" > "1.2.9"), with a
// leading "v" ignored; a non-numeric segment falls back to string order.
func versionLess(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x == y {
			continue
		}
		xi, xerr := strconv.Atoi(x)
		yi, yerr := strconv.Atoi(y)
		if xerr == nil && yerr == nil {
			return xi < yi
		}
		return x < y
	}
	return false
}
