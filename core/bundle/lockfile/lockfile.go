// Package lockfile implements the kenaz.lock format.
//
// Format: TOML, Cargo-flavored, schema-versioned, byte-deterministic. The
// lockfile pins every bundle by content hash and records the channel that
// produced it so resolution is reproducible across machines and time.
//
// This package implements its own narrow TOML reader and a canonical
// writer rather than depending on a generic TOML library. The reader
// accepts the subset of TOML that the writer emits; this is intentional
// because byte-deterministic output is the central NFR (NFR-002), and a
// generic library's emit ordering and quoting choices are outside our
// control. See plan §5.2.
//
// Imports outside this package: only the parent core/bundle for typed
// errors (DIRECTIVE_001 isolation).
package lockfile

// CurrentSchemaVersion is the highest lockfile schema version this harness
// emits. Newer lockfiles are rejected with bundle.ErrSchemaUnsupported.
const CurrentSchemaVersion = 1

// MinSchemaVersion is the lowest version this harness can read.
const MinSchemaVersion = 1

// Lockfile is the parsed kenaz.lock document.
type Lockfile struct {
	SchemaVersion int             `toml:"schema_version"`
	Bundles       []LockedBundle  `toml:"bundle"`
	Universal     UniversalMarker `toml:"universal"`
}

// LockedBundle pins one bundle by content hash.
type LockedBundle struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	Source      string `toml:"source"` // channel-qualified URL
	ContentHash string `toml:"content_hash"`
	// SignatureRef is a locator reference only — its mere presence is
	// NOT proof of anything (a v0.64.0-and-earlier row could carry one
	// from a bundle whose signature was never actually checked). Do not
	// derive a trust tier from this field; use Verified below, which is
	// only ever set true by a real, positive VerifyManifestSignatures
	// result (bundle-download-and-verify-01PMZ909 UNIT-4, spec FR-006).
	SignatureRef string `toml:"signature_ref,omitempty"`
	// Verified records whether THIS install actually ran signature
	// verification and it passed. Absent (false) on every row a
	// pre-UNIT-4 release wrote, by construction — the zero value is the
	// honest default for "never verified", which is exactly what a
	// legacy row is. lockedToBundle derives the "signed" tier from this
	// field, never from SignatureRef's mere presence (UNIT-4 G-2).
	Verified     bool             `toml:"verified,omitempty"`
	Dependencies []LockedDep      `toml:"dependencies,omitempty"`
	Artifacts    []LockedArtifact `toml:"artifact,omitempty"`
}

// LockedDep is one entry in a bundle's transitive dependency pins.
type LockedDep struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	ContentHash string `toml:"content_hash"`
}

// LockedArtifact pins one named artifact within a bundle.
type LockedArtifact struct {
	Name        string `toml:"name"`
	Kind        string `toml:"kind"`
	ContentHash string `toml:"content_hash"`
}

// UniversalMarker is reserved for the uv.lock-style cross-platform variant
// model. It is intentionally empty in v1; presence of the table is allowed
// and parsed, but no fields are populated.
type UniversalMarker struct {
	// Reserved for v2: platform-conditional artifact entries.
}
