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

	// ErrSigningKeyNotConfigured is returned when FleetSigningKeys() is empty
	// because the build-time ldflags were not populated (or the pin list was
	// malformed and rejected whole). All bundle verification fails hard in
	// this case — no config is applied.
	ErrSigningKeyNotConfigured = errors.New("fleet: config bundle signing key not configured at build time")

	// ErrSigningKeyUnknown is returned by VerifyWithKeySet when a bundle names
	// its signer via the signed "key_id" field and NO pinned key in this
	// binary has that key_id. Operationally this means the install's pins
	// predate the newest fleet signing key (fleet was flipped to a key this
	// release never pinned) — the remedy is updating the harness, not
	// retrying. Distinct from ErrInvalidSignature (a pinned key was selected
	// and the signature did not verify) so Settings can say which happened.
	ErrSigningKeyUnknown = errors.New("fleet: bundle signed with an unknown key")

	// ErrConfigBundleTooLarge is returned by the config poller when the
	// GET /api/v1/configs body exceeds maxConfigBundleBytes (4 MiB). The
	// body is rejected unparsed and unverified; nothing is applied and the
	// config-pull status carries this error.
	ErrConfigBundleTooLarge = errors.New("fleet: config bundle too large")

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

	// ErrFleetAPINotRouted is returned by API calls that hit the fleet
	// base URL but receive an HTML response instead of JSON. This almost
	// always means CloudFront / the fleet ingress is misconfigured — the
	// dashboard SPA is being served for /api/v1/* paths because no
	// behavior routes API calls to the Go backend. The harness can't do
	// anything about this server-side; surface it so users know to ping
	// fleet infra ownership.
	ErrFleetAPINotRouted = errors.New("fleet: API endpoint returned HTML (dashboard SPA fall-through) — fleet infra needs to route /api/v1/* to the backend")

	// ErrFleetUnreachable is returned by API calls that fail at the
	// transport layer — DNS resolution failure, connection refused,
	// connection reset, timeout. Typical cause: VPN not active, or
	// the fleet deployment is offline.
	ErrFleetUnreachable = errors.New("fleet: server unreachable (check VPN connection, then retry)")

	// ErrBootstrapRunFinalized is returned by PatchBootstrapRun /
	// ResumeBootstrapRun when the server replies 409 because the run has
	// already reached a terminal (completed/failed) state or is already
	// running. Advance-only semantics: a finalized run cannot be re-patched.
	// (context-bootstrap-harness-integration WP01)
	ErrBootstrapRunFinalized = errors.New("fleet: bootstrap run is finalized (409 run_finalized)")

	// ErrUserNotProvisioned is returned by enrollIdentity when POST
	// /api/v1/enroll responds 403 with code "user_not_provisioned": the
	// caller authenticated with Zitadel (valid access token), but that
	// identity has no corresponding Fleet account. This is TERMINAL — the
	// same tokens will 403 on every retry until the user finishes signup
	// out-of-band at the SPA host. Callers that poll (e.g. UserMenu's
	// identity refresh) must treat this as a stop condition, not a
	// transient failure to back off from.
	ErrUserNotProvisioned = errors.New("fleet: this Zitadel user has no Fleet account; finish signup at the SPA host")
)
