// Package bundle is the public façade of the kenaz-harness bundle layer.
//
// It exposes the typed error sentinels referenced across every subpackage —
// resolver, manifest, lockfile, cache, channels, and integrity. Sentinels are
// values (not strings) so operator UX (`harness bundle why`, `harness bundle
// status`) and SOC 2 audit replay can match on them stably.
package bundle

import "errors"

// Schema and manifest errors.
var (
	ErrSchemaUnsupported = errors.New("bundle: manifest schema_version unsupported by this harness")
	ErrManifestInvalid   = errors.New("bundle: manifest invalid")
	ErrLockfileInvalid   = errors.New("bundle: lockfile invalid")
)

// Validation errors.
var (
	ErrPathTraversal     = errors.New("bundle: artifact path escapes bundle root")
	ErrDuplicateArtifact = errors.New("bundle: duplicate (kind,name) within bundle")
	ErrCyclicDependency  = errors.New("bundle: circular bundle dependency")
)

// Channel errors.
var (
	ErrChannelUnreachable = errors.New("bundle: distribution channel unreachable")
	ErrChannelUnknown     = errors.New("bundle: unknown distribution channel kind")
)

// Integrity errors.
var (
	ErrIntegrityMismatch     = errors.New("bundle: content hash does not match lockfile")
	ErrPinnedArtifactMissing = errors.New("bundle: pinned artifact unavailable at any channel")
	ErrSignatureInvalid      = errors.New("bundle: signature verification failed")
	ErrSignatureRequired     = errors.New("bundle: operator policy requires signature; bundle is unsigned")
)

// Lifecycle errors.
var (
	ErrCancelled              = errors.New("bundle: resolution cancelled")
	ErrConflictUnresolved     = errors.New("bundle: artifact conflict requires precedence policy")
	ErrUnknownArtifactKind    = errors.New("bundle: unknown artifact kind")
	ErrDuplicateKindRegistration = errors.New("bundle: duplicate artifact-kind registration")
	ErrActivationFailed       = errors.New("bundle: artifact activation failed")
)

// Deferred / unimplemented features (intentional v1 scope cuts).
var (
	// ErrNotImplementedV1x is returned by features deferred to v1.x — most
	// notably the `kenaz lock --resolve-conflicts` subcommand. The error
	// message points operators at the deferred command name so the gap is
	// actionable rather than mysterious.
	ErrNotImplementedV1x = errors.New("bundle: feature deferred to v1.x (e.g., `kenaz lock --resolve-conflicts`)")
)
