// Package events defines the bundle-layer event kinds and the Emitter
// interface the resolver publishes them through.
//
// The bundle layer never appends to the event log directly. It calls
// Emitter.Emit, and the event-log mission owns redaction (FR/US3 of
// event-log spec), append-only enforcement, and persistence.
//
// All payloads in this package are credential-free by construction —
// channel auth is by reference, and the resolved value is never carried
// across this boundary. The credential-leakage audit test in WP12 spot-
// checks every payload struct for accidental drift.
package events

// Kind constants. The set is closed for v1; new kinds require a plan
// amendment.
const (
	KindBundleResolved          = "bundle_resolved"
	KindArtifactFetched         = "artifact_fetched"
	KindArtifactVerified        = "artifact_verified"
	KindArtifactActivated       = "artifact_activated"
	KindArtifactDeactivated     = "artifact_deactivated"
	KindArtifactRejected        = "artifact_rejected"
	KindBundleSignatureVerified = "bundle_signature_verified"
	KindBundleSignatureFailed   = "bundle_signature_failed"
	KindLockfileUpdated         = "lockfile_updated"
	KindCacheHit                = "cache_hit"
	KindCacheMiss               = "cache_miss"
	KindChannelUnreachable      = "channel_unreachable"
)

// AllKinds is the canonical set of event kinds emitted by this package.
// Useful for test assertions and audit-coverage checks.
var AllKinds = []string{
	KindBundleResolved,
	KindArtifactFetched,
	KindArtifactVerified,
	KindArtifactActivated,
	KindArtifactDeactivated,
	KindArtifactRejected,
	KindBundleSignatureVerified,
	KindBundleSignatureFailed,
	KindLockfileUpdated,
	KindCacheHit,
	KindCacheMiss,
	KindChannelUnreachable,
}

// Emitter is the narrow interface the bundle layer publishes through.
// The event-log mission supplies a concrete implementation that
// persists to the harness's append-only log; tests may use
// TestEmitter.
type Emitter interface {
	Emit(kind string, payload any)
}
