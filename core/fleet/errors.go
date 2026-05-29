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

	// ErrSigningKeyNotConfigured is returned when FleetSigningKey() returns nil
	// because the build-time ldflags were not populated. All bundle verification
	// fails hard in this case — no config is applied.
	ErrSigningKeyNotConfigured = errors.New("fleet: config bundle signing key not configured at build time")

	// ErrInvalidSignature is returned by Verify when the ed25519 signature in a
	// config bundle does not match the canonical JSON payload.
	ErrInvalidSignature = errors.New("fleet: config bundle signature invalid")

	// ErrBundleIDNonMonotonic is returned by Verify when the incoming bundle_id
	// is less than or equal to the last-applied bundle_id (replay guard).
	ErrBundleIDNonMonotonic = errors.New("fleet: config bundle_id is not monotonically increasing (possible replay)")

	// ErrLockdownActive is returned by state-mutating RPC bindings when a
	// fleet-issued emergency lockdown is currently in effect and
	// HARNESS_FLEET_LOCKDOWN_BYPASS is not set. The frontend surfaces this
	// as a "locked" state in the LockdownBanner and disables the composer.
	// (fleet-emergency-lockdown-01NDFSEX12 WP04)
	ErrLockdownActive = errors.New("fleet: emergency lockdown is active")
)
