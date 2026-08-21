// Package llm — fleet_prefs.go
//
// ApplyFleetModelPrefs installs the fleet-distributed model governance
// carried by a config bundle's model_prefs section
// (fleet.BundleModelPrefs, core/fleet/bundle.go). It is called from
// compositeConfigApplier.ApplyBundle (core/rpc/views/settings/fleet.go)
// after every successful bundle apply — mirroring the existing
// core/mcp/recipes.ApplyFleetAllowlist package-level-singleton pattern
// (core/mcp/recipes/allowlist.go), which is the structural precedent
// spec §5.8 names for this exact wire.
//
// Before fleet-enforcement-truth-01PMZ505 WP04, model_prefs was copied
// into a field (fleetState.fleetModelPrefs) with zero readers — a copy
// into another struct is not consumption. ListProviders and StartStream
// below are the two real consumers; profileKindAndModel is where
// DefaultModel reaches a run.
//
// D-3 (spec §6): DefaultModel SEEDS a run's resolution where the caller
// made no explicit per-run selection; an explicit selection always wins.
// ProviderAllowlist RESTRICTS and wins over user selection, because that
// is what an allow-list means — the opposite precedence from
// DefaultModel, by design.
package llm

import "sync"

var (
	fleetPrefsMu           sync.RWMutex
	fleetProviderAllowlist map[string]bool // nil = no fleet restriction
	fleetDefaultModelValue string
)

// ApplyFleetModelPrefs installs the fleet-managed model preferences
// globally. Called by compositeConfigApplier.ApplyBundle whenever a
// bundle carries a non-nil model_prefs section. Passing a nil or empty
// providerAllowlist clears any prior restriction (matches
// BundleModelPrefs.ProviderAllowlist's own "nil means no restriction"
// doc, core/fleet/bundle.go).
func ApplyFleetModelPrefs(defaultModel string, providerAllowlist []string) {
	fleetPrefsMu.Lock()
	defer fleetPrefsMu.Unlock()
	fleetDefaultModelValue = defaultModel
	if len(providerAllowlist) == 0 {
		fleetProviderAllowlist = nil
		return
	}
	m := make(map[string]bool, len(providerAllowlist))
	for _, p := range providerAllowlist {
		m[p] = true
	}
	fleetProviderAllowlist = m
}

// ClearFleetModelPrefs resets fleet-applied model governance to "no
// restriction, no default". Called from settings.API.StopFleetBackground
// on fleet sign-out so a signed-out device does not keep enforcing an
// allow-list or default model pushed before sign-out — the same
// session-scoped-cache clearing StopFleetBackground already does for
// telemetryOptIns. Also used by tests needing a clean slate between
// cases (package-level state persists across subtests otherwise).
func ClearFleetModelPrefs() {
	fleetPrefsMu.Lock()
	defer fleetPrefsMu.Unlock()
	fleetProviderAllowlist = nil
	fleetDefaultModelValue = ""
}

// fleetDefaultModel returns the current fleet-pushed default model, or
// "" when none is set.
func fleetDefaultModel() string {
	fleetPrefsMu.RLock()
	defer fleetPrefsMu.RUnlock()
	return fleetDefaultModelValue
}

// providerAllowed reports whether kind is permitted under the current
// fleet provider allow-list. No allow-list (nil) permits everything —
// the same "nil = unrestricted" convention as
// core/mcp/recipes.AllowlistFilter.
func providerAllowed(kind string) bool {
	fleetPrefsMu.RLock()
	defer fleetPrefsMu.RUnlock()
	if fleetProviderAllowlist == nil {
		return true
	}
	return fleetProviderAllowlist[kind]
}
