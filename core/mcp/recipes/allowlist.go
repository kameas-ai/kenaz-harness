// Package recipes — allowlist.go
//
// FleetAllowlist applies a fleet-distributed MCP recipe allow-list filter.
// When a fleet allow-list is active, only recipe IDs explicitly present in
// the list may be installed or enabled. An empty/nil allow-list means "no
// fleet restriction" — all recipes are permitted.
//
// The allow-list is stored in-memory (populated by the fleet config poller)
// and consulted at recipe-install and recipe-load time by calling
// IsAllowed(recipeID).
//
// (fleet-config-pull-01NDFSEX10 WP04)
package recipes

import (
	"log"
	"sync"
)

// AllowlistFilter is a thread-safe fleet-managed MCP recipe allow-list.
// The zero value (no allow-list set) permits all recipes.
type AllowlistFilter struct {
	mu     sync.RWMutex
	active bool              // true when a fleet allow-list is in effect
	ids    map[string]bool   // set of allowed recipe IDs
	sealed bool              // true in served mode — fleet writes are ignored
}

// globalAllowlist is the process-level singleton consulted by IsAllowed.
// Populated by ApplyFleetAllowlist; safe for concurrent use.
var globalAllowlist AllowlistFilter

// ApplyFleetAllowlist installs the fleet-managed allow-list globally.
// Passing nil clears any prior fleet restriction (all recipes become allowed).
// Passing a non-nil empty slice activates block-all mode (no recipes allowed).
//
// This function is called by the fleet config poller after each successful
// bundle apply. It is the only write path for the global allow-list in HOST
// mode. In SERVED mode the profile-derived whitelist installed via
// ApplyServedAllowlist is the sole writer (spec 091 FR-004 / the
// ADR-connector-catalog-consumption sole-writer rule): once the allow-list
// is sealed, calls here are ignored — never a silent last-writer-wins on a
// process-global security singleton.
func ApplyFleetAllowlist(allowedIDs []string) {
	if globalAllowlist.Sealed() {
		log.Printf("recipes: fleet allow-list write ignored — allow-list is sealed (served mode; profile whitelist is the sole writer)")
		return
	}
	globalAllowlist.Set(allowedIDs)
}

// ApplyServedAllowlist installs the profile-derived served-mode allow-list
// globally and seals it against fleet writes. In served mode the harness
// default is INVERTED: absence of a whitelist means block-all, so callers
// must pass a non-nil slice — an empty slice activates block-all mode.
// Passing nil is coerced to the empty slice so this entry point can never
// re-open the host-mode "nil = unrestricted" meaning (spec 091 FR-004).
//
// Safe to call more than once (the served boot path is the only caller);
// each call replaces the previous served list and keeps the seal.
func ApplyServedAllowlist(allowedIDs []string) {
	if allowedIDs == nil {
		allowedIDs = []string{}
	}
	globalAllowlist.setAndSeal(allowedIDs)
}

// AllowlistSealed reports whether the global allow-list has been sealed by
// ApplyServedAllowlist (served mode). While sealed, ApplyFleetAllowlist is
// a no-op.
func AllowlistSealed() bool {
	return globalAllowlist.Sealed()
}

// IsAllowed reports whether recipeID is permitted under the current
// fleet allow-list. Returns true when no fleet allow-list is active.
//
// This function is the read path used at install and load time.
func IsAllowed(recipeID string) bool {
	return globalAllowlist.IsAllowed(recipeID)
}

// Set replaces the current allow-list.
//   - nil allowedIDs clears the restriction (all recipes allowed).
//   - A non-nil empty slice activates block-all mode (no recipes allowed).
//   - A non-empty slice restricts to the listed recipe IDs.
func (f *AllowlistFilter) Set(allowedIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if allowedIDs == nil {
		// nil → no fleet restriction.
		f.active = false
		f.ids = nil
		return
	}
	// Non-nil (including empty) → fleet restriction active.
	f.active = true
	f.ids = make(map[string]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		f.ids[id] = true
	}
}

// setAndSeal replaces the current allow-list and seals the filter so
// subsequent Set calls routed through ApplyFleetAllowlist are refused.
// allowedIDs must be non-nil (the caller coerces).
func (f *AllowlistFilter) setAndSeal(allowedIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.active = true
	f.sealed = true
	f.ids = make(map[string]bool, len(allowedIDs))
	for _, id := range allowedIDs {
		f.ids[id] = true
	}
}

// Sealed reports whether the filter has been sealed by setAndSeal.
func (f *AllowlistFilter) Sealed() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.sealed
}

// IsAllowed reports whether recipeID is permitted. Returns true when no
// allow-list is active (f.active == false).
func (f *AllowlistFilter) IsAllowed(recipeID string) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.active {
		return true
	}
	return f.ids[recipeID]
}

// ActiveIDs returns the current set of allowed recipe IDs, or nil when no
// fleet restriction is active. Returned slice is a copy — safe for the caller
// to retain.
func (f *AllowlistFilter) ActiveIDs() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.active {
		return nil
	}
	out := make([]string, 0, len(f.ids))
	for id := range f.ids {
		out = append(out, id)
	}
	return out
}
