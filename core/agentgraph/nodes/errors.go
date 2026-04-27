package nodes

import "errors"

// Sentinel errors surfaced by the loader and resolver. Callers MAY use
// errors.Is to discriminate; messages always wrap one of these
// sentinels with file path + manifest ID context.
var (
	// ErrUnknownKind is returned when Catalog.Get / Resolve cannot find
	// the requested ID (after alias resolution, in later WPs).
	ErrUnknownKind = errors.New("nodes: unknown kind")

	// ErrCycleDetected is returned when the inheritance resolver finds
	// a cycle in the `extends:` chain (FR-004 cycle detection).
	ErrCycleDetected = errors.New("nodes: extends-chain cycle")

	// ErrArchetypeNotCallable is returned when a graph attempts to
	// instantiate an archetype directly (FR-007 / FR-016). v1
	// archetypes are abstract.
	ErrArchetypeNotCallable = errors.New("nodes: archetype is not directly callable in v1")

	// ErrForbiddenOverrideField is returned by the user-override merge
	// path (WP07) when a user manifest changes a forbidden field
	// (FR-021). Surfaced here in WP01 so the merge helper can use the
	// sentinel; the WP07 wiring exercises it.
	ErrForbiddenOverrideField = errors.New("nodes: user override changed a forbidden field")

	// ErrManifestParse is the catch-all for YAML parse + meta-schema
	// failures during load. Always wrapped with file path context.
	ErrManifestParse = errors.New("nodes: manifest parse error")

	// ErrDuplicateID is returned when two shipped manifests declare the
	// same `id` (filename collision after normalisation).
	ErrDuplicateID = errors.New("nodes: duplicate manifest id")
)
