// Package contexts — fleet_entry.go
//
// FleetEntry is a mirror of fleet.ContextNodeEntry scoped to the core/contexts
// package. Defining a local copy avoids an import of core/fleet from core/contexts,
// preserving the OSS-first import boundary enforced by check-no-fleet-imports.sh.
//
// Conversions between FleetEntry and fleet.ContextNodeEntry live in
// core/rpc/views/contexts/impl.go (the only layer allowed to import both).
//
// (harness-fleet-sync-activation-01NSYNC01 FR-012)
package contexts

import (
	"encoding/json"

	contextpack "github.com/kameas-ai/kenaz-harness/core/context/pack"
)

// FleetEntry is the local mirror of fleet.ContextNodeEntry. Fields are
// deliberately kept minimal — only the fields consumed by MergeFleetEntries.
type FleetEntry struct {
	// ID is the stable UUID assigned on first publish.
	ID string
	// Layer is the local context pack layer (team or org; personal is rejected).
	Layer contextpack.Layer
	// Kind is the entry kind string (guidance, fact, snippet, …).
	Kind string
	// Title is the entry display name.
	Title string
	// Body is the entry text.
	Body string
	// Metadata is opaque JSON attached to the node.
	Metadata json.RawMessage
	// Version is the monotonic version counter from the fleet server.
	Version int
	// DeletedAt is non-nil when the entry is a server-side tombstone.
	// A tombstone causes MergeFleetEntries to remove the local entry.
	DeletedAt *string
}

// ContextConflict carries the information for a pull-time version conflict.
// The local edit is preserved; the caller (injected via SetConflictNotifier)
// is responsible for surfacing this to the user.
type ContextConflict struct {
	// NodeID is the ID of the conflicting entry.
	NodeID string
	// ServerVersion is the version received from the fleet server.
	ServerVersion int
	// ClientVersion is the local client version (the locally-edited version).
	ClientVersion int
}

// ConflictNotifier is the callback injected by the RPC layer so Library can
// emit conflict events without importing core/fleet or core/rpc.
// It is called with s.mu held for reading, so it must be non-blocking.
type ConflictNotifier func(conflict ContextConflict)
