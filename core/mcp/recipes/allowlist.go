// Package recipes — allowlist.go
//
// The MCP recipe allow-list has two independent layers, composed by
// INTERSECTION (a recipe must pass every active layer):
//
//   - the served layer: the workbench profile whitelist (served mode,
//     ApplyServedAllowlist). Sealed: only the served boot path writes it,
//     and absence means block-all (spec 091 FR-004).
//   - the fleet layer: the org allow-list from the signed fleet config
//     bundle (ApplyFleetAllowlist). nil = no fleet restriction, [] =
//     block all, list = replace.
//
// Because the layers intersect, the fleet layer can only NARROW what the
// profile allows — it can never widen a sealed served whitelist, which is
// what the sole-writer rule protects. (Before the fleet-policy push-down
// the fleet write was dropped entirely in served mode, so an org's MCP
// allow-list had no effect inside a workbench.)
//
// Consulted at recipe-install and recipe-load time via IsAllowed.
//
// (fleet-config-pull-01NDFSEX10 WP04; fleet-policy push-down 2026-10-31)
package recipes

import (
	"sync"
)

// AllowlistFilter is a thread-safe two-layer MCP recipe allow-list.
// The zero value (no layer active) permits all recipes.
type AllowlistFilter struct {
	mu sync.RWMutex
	// fleet layer
	active bool            // true when a fleet allow-list is in effect
	ids    map[string]bool // fleet-allowed recipe IDs
	// served layer (sealed)
	sealed    bool            // true in served mode
	servedIDs map[string]bool // profile-allowed recipe IDs
}

// globalAllowlist is the process-level singleton consulted by IsAllowed.
var globalAllowlist AllowlistFilter

// ApplyFleetAllowlist installs the fleet-managed layer globally, REPLACING
// the previous fleet layer. nil clears the fleet restriction; a non-nil
// empty slice blocks all; otherwise only the listed IDs pass the fleet
// layer. In served mode the sealed profile layer still applies on top.
func ApplyFleetAllowlist(allowedIDs []string) {
	globalAllowlist.Set(allowedIDs)
}

// ApplyServedAllowlist installs the profile-derived served-mode layer and
// seals it. In served mode the harness default is INVERTED: absence of a
// whitelist means block-all, so nil is coerced to the empty slice (spec 091
// FR-004). Safe to call more than once; each call replaces the served layer.
func ApplyServedAllowlist(allowedIDs []string) {
	if allowedIDs == nil {
		allowedIDs = []string{}
	}
	globalAllowlist.setAndSeal(allowedIDs)
}

// AllowlistSealed reports whether the served layer is installed (served
// mode).
func AllowlistSealed() bool {
	return globalAllowlist.Sealed()
}

// FleetAllowlistActive reports whether a fleet allow-list layer is in
// effect (status surfaces), and its IDs (nil when inactive).
func FleetAllowlistActive() (bool, []string) {
	globalAllowlist.mu.RLock()
	defer globalAllowlist.mu.RUnlock()
	if !globalAllowlist.active {
		return false, nil
	}
	return true, keys(globalAllowlist.ids)
}

// IsAllowed reports whether recipeID passes every active layer.
func IsAllowed(recipeID string) bool {
	return globalAllowlist.IsAllowed(recipeID)
}

// Set replaces the fleet layer.
//   - nil allowedIDs clears the fleet restriction.
//   - A non-nil empty slice activates block-all mode.
//   - A non-empty slice restricts to the listed recipe IDs.
func (f *AllowlistFilter) Set(allowedIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if allowedIDs == nil {
		f.active = false
		f.ids = nil
		return
	}
	f.active = true
	f.ids = toSet(allowedIDs)
}

// setAndSeal replaces the served layer and seals the filter.
func (f *AllowlistFilter) setAndSeal(allowedIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sealed = true
	f.servedIDs = toSet(allowedIDs)
}

// Sealed reports whether the served layer is installed.
func (f *AllowlistFilter) Sealed() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.sealed
}

// IsAllowed reports whether recipeID passes every active layer.
func (f *AllowlistFilter) IsAllowed(recipeID string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.sealed && !f.servedIDs[recipeID] {
		return false
	}
	if f.active && !f.ids[recipeID] {
		return false
	}
	return true
}

// ActiveIDs returns the effective allowed IDs (the intersection of the
// active layers), or nil when no layer is active. Returned slice is a copy.
func (f *AllowlistFilter) ActiveIDs() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	switch {
	case !f.active && !f.sealed:
		return nil
	case f.active && !f.sealed:
		return keys(f.ids)
	case !f.active && f.sealed:
		return keys(f.servedIDs)
	}
	out := []string{}
	for id := range f.ids {
		if f.servedIDs[id] {
			out = append(out, id)
		}
	}
	return out
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	return out
}

// ResetAllowlistForTesting clears both layers and the seal. Test seam only:
// production never unseals (cross-package tests cannot reset the singleton
// otherwise).
func ResetAllowlistForTesting() {
	globalAllowlist.mu.Lock()
	defer globalAllowlist.mu.Unlock()
	globalAllowlist.active, globalAllowlist.ids = false, nil
	globalAllowlist.sealed, globalAllowlist.servedIDs = false, nil
}
