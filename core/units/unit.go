// Package units owns the harness's unified context+artifacts "Unit"
// store. A Unit is the single durable entity that spans the spectrum
// from a rich root context document all the way down to an atomic
// snippet or tool output. Every Unit carries a scope (global | project
// | session), a classification (personal | team | org), a monotonically
// increasing version counter, and a load policy so the runtime knows
// whether to pull it eagerly at session-start or on demand.
//
// Edges model typed directed relationships between Units (references,
// derived_from, promoted_from, supersedes). They carry their own
// version so the graph's lineage is also auditable.
//
// DIRECTIVE_001: this package is the single owner of the units and
// unit_edges tables; all read/write access from other packages must go
// through the public Manager API.
package units

import (
	"encoding/json"
	"errors"
	"time"
)

// Unit is the durable metadata row for one context/artifact unit.
// The body text lives inline in the row; binary payloads can reference
// external CAS hashes via Metadata.
type Unit struct {
	// ID is a 26-char Crockford-base32 ULID minted at Create time.
	ID string
	// Kind classifies the unit's role in the hierarchy.
	Kind Kind
	// Scope is the widest audience this unit is visible to.
	Scope Scope
	// ScopeID carries the project-id or session-id when Scope is
	// ScopeProject or ScopeSession respectively. Empty for ScopeGlobal.
	ScopeID string
	// Classification is the sharing/visibility tier within a scope.
	Classification Classification
	// Version is the monotonically increasing revision number scoped
	// per unit (first update bumps from 0 to 1). The create call
	// stores the unit at Version==0; each Update call increments by 1.
	Version int
	// LoadPolicy controls when the runtime injects this unit into
	// context: always at session-start, or on demand.
	LoadPolicy LoadPolicy
	// Title is a human-readable label.
	Title string
	// Body is the textual/markdown/code body of the unit. May be empty
	// for binary-only units that reference external CAS files via Metadata.
	Body string
	// Metadata is an arbitrary JSON object for extension data
	// (e.g. content_hash, byte_size, mime_type for binary units).
	// Stored as JSON text; nil encodes as "{}".
	Metadata json.RawMessage
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time
	// UpdatedAt is the wall-clock at the most recent version bump (UTC).
	// Equal to CreatedAt when Version==0.
	UpdatedAt time.Time
}

// UnitVersion is one entry in the append-only revision history for a
// unit. Written by Update; the parent Unit row is mutated (Version,
// Body, Metadata, UpdatedAt) AND a UnitVersion history row is appended.
// The (UnitID, Version) pair is unique.
type UnitVersion struct {
	// ID is the auto-incrementing row id.
	ID int64
	// UnitID is the parent unit this version belongs to.
	UnitID string
	// Version is the revision number (matches the Unit.Version at
	// the time of the update).
	Version int
	// Body is the body text at this version.
	Body string
	// Metadata is the metadata JSON at this version.
	Metadata json.RawMessage
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time
}

// Edge is a typed directed relationship between two Units.
type Edge struct {
	// ID is a 26-char Crockford-base32 ULID minted at AddEdge time.
	ID string
	// FromID is the source Unit id.
	FromID string
	// ToID is the target Unit id.
	ToID string
	// Kind classifies the relationship.
	Kind EdgeKind
	// Version is a monotonically increasing counter for the edge row
	// (currently unused but reserved for future conflict detection).
	Version int
	// CreatedAt is the wall-clock at insert time (UTC).
	CreatedAt time.Time
}

// Kind enumerates the structural role of a unit.
type Kind string

const (
	// KindRoot is a top-level context document (e.g. a project memory).
	KindRoot Kind = "root"
	// KindDoc is a rich text/markdown document.
	KindDoc Kind = "doc"
	// KindArtifact is a binary or text artifact (code, image, etc).
	KindArtifact Kind = "artifact"
	// KindSnippet is a short text fragment.
	KindSnippet Kind = "snippet"
	// KindToolOutput is the captured output of a tool call.
	KindToolOutput Kind = "tool_output"
)

// Scope enumerates the visibility audience of a unit.
type Scope string

const (
	// ScopeGlobal makes the unit visible to all sessions and projects.
	ScopeGlobal Scope = "global"
	// ScopeProject scopes the unit to one project (ScopeID = project id).
	ScopeProject Scope = "project"
	// ScopeSession scopes the unit to one session (ScopeID = session id).
	ScopeSession Scope = "session"
)

// Classification enumerates the sharing/visibility tier.
type Classification string

const (
	// ClassPersonal is visible only to the owner.
	ClassPersonal Classification = "personal"
	// ClassTeam is visible to all members of the owner's team.
	ClassTeam Classification = "team"
	// ClassOrg is visible to all members of the owner's organisation.
	ClassOrg Classification = "org"
)

// LoadPolicy controls when the runtime injects the unit into context.
type LoadPolicy string

const (
	// LoadAlways injects the unit into every new session automatically.
	LoadAlways LoadPolicy = "always"
	// LoadOnDemand injects only when explicitly requested.
	LoadOnDemand LoadPolicy = "on_demand"
)

// EdgeKind enumerates the typed relationship between two units.
type EdgeKind string

const (
	// EdgeReferences is a loose citation: FromID cites ToID.
	EdgeReferences EdgeKind = "references"
	// EdgeDerivedFrom is a provenance edge: FromID was derived from ToID.
	EdgeDerivedFrom EdgeKind = "derived_from"
	// EdgePromotedFrom records scope promotion: FromID was promoted from ToID.
	EdgePromotedFrom EdgeKind = "promoted_from"
	// EdgeSupersedes marks that FromID replaces ToID.
	EdgeSupersedes EdgeKind = "supersedes"
)

// UnitFilter narrows the List query. Empty/zero fields match every row.
type UnitFilter struct {
	// Scope restricts to one scope value. Empty = all.
	Scope Scope
	// ScopeID restricts to one scope id. Empty = all.
	ScopeID string
	// Kind restricts to one kind. Empty = all.
	Kind Kind
	// Classification restricts to one classification. Empty = all.
	Classification Classification
	// LoadPolicy restricts to one load policy. Empty = all.
	LoadPolicy LoadPolicy
}

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrUnitNotFound is returned when an id has no matching row.
	ErrUnitNotFound = errors.New("units: not found")

	// ErrUnsupportedKind is returned when Create/Update receives a Kind
	// outside the valid set.
	ErrUnsupportedKind = errors.New("units: unsupported kind")

	// ErrUnsupportedScope is returned when Create/Update/Promote receives
	// a Scope outside the valid set.
	ErrUnsupportedScope = errors.New("units: unsupported scope")

	// ErrUnsupportedClassification is returned when Create/Update receives
	// a Classification outside the valid set.
	ErrUnsupportedClassification = errors.New("units: unsupported classification")

	// ErrUnsupportedLoadPolicy is returned when Create/Update receives a
	// LoadPolicy outside the valid set.
	ErrUnsupportedLoadPolicy = errors.New("units: unsupported load policy")

	// ErrUnsupportedEdgeKind is returned when AddEdge receives an
	// EdgeKind outside the valid set.
	ErrUnsupportedEdgeKind = errors.New("units: unsupported edge kind")

	// ErrEdgeNotFound is returned when an edge id has no matching row.
	ErrEdgeNotFound = errors.New("units: edge not found")

	// ErrVersionConflict is returned when a concurrent version bump
	// races. Callers may retry.
	ErrVersionConflict = errors.New("units: version conflict")

	// ErrSyncStateNotFound is returned when a unit (or node id) has no
	// sync sidecar row — i.e. it has never been synced to fleet.
	ErrSyncStateNotFound = errors.New("units: sync state not found")
)

// validKind reports whether k is a known Kind value.
func validKind(k Kind) bool {
	switch k {
	case KindRoot, KindDoc, KindArtifact, KindSnippet, KindToolOutput:
		return true
	}
	return false
}

// validScope reports whether s is a known Scope value.
func validScope(s Scope) bool {
	switch s {
	case ScopeGlobal, ScopeProject, ScopeSession:
		return true
	}
	return false
}

// validClassification reports whether c is a known Classification value.
func validClassification(c Classification) bool {
	switch c {
	case ClassPersonal, ClassTeam, ClassOrg:
		return true
	}
	return false
}

// validLoadPolicy reports whether p is a known LoadPolicy value.
func validLoadPolicy(p LoadPolicy) bool {
	switch p {
	case LoadAlways, LoadOnDemand:
		return true
	}
	return false
}

// validEdgeKind reports whether k is a known EdgeKind value.
func validEdgeKind(k EdgeKind) bool {
	switch k {
	case EdgeReferences, EdgeDerivedFrom, EdgePromotedFrom, EdgeSupersedes:
		return true
	}
	return false
}

// normaliseMetadata returns "{}" when m is nil/empty.
func normaliseMetadata(m json.RawMessage) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage("{}")
	}
	return m
}
