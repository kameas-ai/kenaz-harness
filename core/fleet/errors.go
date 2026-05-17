package fleet

import "errors"

// Sentinel errors returned by fleet operations.
var (
	// ErrFleetDisabled is returned by all Client methods when the kill switch
	// HARNESS_FLEET_DISABLED=1 is active or when a NopClient is in use.
	ErrFleetDisabled = errors.New("fleet: disabled by env")

	// ErrNotSignedIn is returned when an operation requires a valid identity
	// but no signed-in session exists.
	ErrNotSignedIn = errors.New("fleet: not signed in")

	// ErrTokenExpired is returned when the access token has expired and the
	// refresh token exchange also fails. The user must sign in again.
	ErrTokenExpired = errors.New("fleet: refresh failed; re-sign-in required")

	// ErrCapabilityNotInTier is a placeholder for tier-gated capability
	// enforcement. Populated by the capabilities mission.
	ErrCapabilityNotInTier = errors.New("fleet: capability not available in current tier")

	// ErrProfileNotConfigured is returned when the selected env profile has
	// empty key fields — the build pipeline has not populated the ldflag vars.
	ErrProfileNotConfigured = errors.New("fleet: env profile not populated at build time")
)
