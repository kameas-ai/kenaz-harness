// Package middleware provides per-request guards that run inside Wails binding
// methods before any side-effecting work. All guards are stateless functions
// (no struct, no interface) so callers can early-return with a typed error
// without importing the full fleet package.
package middleware

import (
	"github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// CheckLockdown returns fleet.ErrLockdownActive when a fleet-issued emergency
// lockdown is in effect and the bypass env var is NOT set. Call this at the
// top of every state-mutating RPC binding that must freeze during a lockdown:
//
//	func (b *Bindings) Sessions_SendMessageWithBlocks(...) (..., error) {
//	    if err := middleware.CheckLockdown(); err != nil { return ..., err }
//	    ...
//	}
//
// Read-only bindings (list queries, status reads) must NOT call CheckLockdown
// so the frontend can still render current state during a lockdown.
//
// (fleet-emergency-lockdown-01NDFSEX12 WP04)
func CheckLockdown() error {
	if fleet.LockdownActive() && !fleet.LockdownBypassed() {
		return fleet.ErrLockdownActive
	}
	return nil
}
