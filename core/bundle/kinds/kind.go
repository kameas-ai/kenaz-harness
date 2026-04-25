// Package kinds defines the harness's primary extension surface — the
// ArtifactKindHandler interface — and the registry that the resolver
// uses to dispatch to concrete handlers.
//
// DIRECTIVE_001 in the charter is enforced by the rule that handlers
// live in their own packages (e.g. core/llm/profilekind/, core/mcp/
// serverkind/). Adding a new artifact kind requires zero changes to any
// other core/bundle/ subpackage; the registry seam is the only contract.
//
// Stability commitments (v1):
//   - Handlers MUST NOT import core/bundle/resolver/ or other handler
//     packages. They depend on this package and on the kinds.Environment
//     for runtime services.
//   - The resolver supplies a deterministic activation order; handlers
//     may rely on the order being stable across runs but MUST NOT
//     depend on any specific position.
//   - Activations are transactional: every successful Activate is
//     paired with exactly one Deactivate on rollback or removal.
package kinds

import (
	"context"
	"io"
)

// ArtifactKindHandler is the contract for a registered artifact-kind.
// Implementers parse, validate, activate, and deactivate one named
// artifact at a time.
type ArtifactKindHandler interface {
	// Kind returns the registered kind id (e.g. "provider_profile").
	// MUST be stable for the lifetime of the harness instance.
	Kind() string

	// ParamSchema returns the JSON Schema bytes for any kind-specific
	// metadata fields the handler accepts. Empty bytes mean "no
	// additional metadata required".
	ParamSchema() []byte

	// Parse reads the artifact bytes from src and returns an opaque
	// Parsed value. Implementations MUST NOT activate side effects
	// during Parse — only read and validate structure.
	Parse(ctx context.Context, src ArtifactSource) (Parsed, error)

	// Validate enforces handler-specific invariants that cannot be
	// expressed in ParamSchema. Returns a typed error on rejection.
	Validate(ctx context.Context, p Parsed) error

	// Activate commits the artifact to the harness — installing a
	// hook, registering a provider profile, etc. Returns an opaque
	// Activation handle the resolver uses for later Deactivate.
	Activate(ctx context.Context, p Parsed, env Environment) (Activation, error)

	// Deactivate undoes a prior Activate. Called on rollback (failed
	// resolution), explicit Remove, or harness shutdown. MUST be
	// idempotent — calling twice yields the same final state.
	Deactivate(ctx context.Context, a Activation) error
}

// Parsed is an opaque value produced by ArtifactKindHandler.Parse. The
// resolver passes it back to Validate and Activate without inspection.
type Parsed any

// Activation is an opaque handle produced by ArtifactKindHandler.Activate.
// It carries enough state for the matching Deactivate to undo the change.
type Activation any

// ArtifactSource is the input handed to Parse.
type ArtifactSource struct {
	// Reader streams the artifact bytes. Always non-nil at call time.
	Reader io.Reader
	// BundleName names the parent bundle (for error messages).
	BundleName string
	// BundleVersion is the parent bundle's version.
	BundleVersion string
	// ArtifactName is the artifact's name within the bundle.
	ArtifactName string
	// BundleRoot is the absolute path to the bundle's root directory if
	// the channel exposed a filesystem layout. May be empty for
	// channels that streamed bytes only.
	BundleRoot string
	// KindMetadata mirrors ArtifactDescriptor.KindMetadata from the
	// manifest (parsed YAML, untyped).
	KindMetadata map[string]any
}

// Environment is the read-only runtime context handed to Activate. It
// exposes only the surface activations are allowed to touch — the data
// directory (for state files), the secrets resolver (for runtime
// credential lookups), and the event emitter (for audit-trail writes).
//
// Locked-down by design: handlers cannot reach into the resolver, the
// channel registry, or the lockfile. If a future kind needs more, it
// must extend this surface explicitly.
type Environment struct {
	// DataDir is the harness's data directory root.
	DataDir string
	// Secrets is a credential resolver (interface lives in core/secrets
	// to avoid forcing handlers to import the bundle layer).
	Secrets SecretsResolver
	// Emitter publishes activation events to the harness event log.
	Emitter EventEmitter
}

// SecretsResolver is a narrow interface mirroring the contract of the
// core/secrets package. Defined here so handlers do not need to import
// the secrets package directly.
type SecretsResolver interface {
	// Lookup confirms a credential ref exists without resolving its value.
	Lookup(ref string) (bool, error)
	// Resolve returns the secret value for ref. Callers MUST NOT log it.
	Resolve(ctx context.Context, ref string) (string, error)
}

// EventEmitter is a narrow interface mirroring the contract of
// core/bundle/events.Emitter. Defined locally to avoid an import cycle.
type EventEmitter interface {
	Emit(kind string, payload any)
}
