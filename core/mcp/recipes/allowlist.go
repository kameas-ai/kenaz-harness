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
	"sync"
)

// AllowlistFilter is a thread-safe fleet-managed MCP recipe allow-list.
// The zero value (no allow-list set) permits all recipes.
type AllowlistFilter struct {
	mu     sync.RWMutex
	active bool              // true when a fleet allow-list is in effect
	ids    map[string]bool   // set of allowed recipe IDs
}

// globalAllowlist is the process-level singleton consulted by IsAllowed.
// Populated by ApplyFleetAllowlist; safe for concurrent use.
var globalAllowlist AllowlistFilter

// ApplyFleetAllowlist installs the fleet-managed allow-list globally.
// Passing nil clears any prior fleet restriction (all recipes become allowed).
// Passing a non-nil empty slice activates block-all mode (no recipes allowed).
//
// This function is called by the fleet config poller after each successful
// bundle apply. It is the only write path for the global allow-list.
func ApplyFleetAllowlist(allowedIDs []string) {
	globalAllowlist.Set(allowedIDs)
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
