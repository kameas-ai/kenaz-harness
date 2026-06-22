package units

import (
	"context"
	"time"
)

// SyncState is the per-unit sync sidecar row. It records the durable
// bookkeeping the fleet syncer needs so that re-syncing a unit requires
// no re-discovery and conflict checks have a stable baseline:
//
//   - NodeID is the fleet context-graph node id this unit maps to. For
//     units that originated locally and were pushed up, NodeID == the
//     unit id (the syncer reuses the unit id as the node id). For units
//     that arrived via pull, NodeID is the server's node id and the local
//     unit id may differ.
//   - SyncedVersion is the unit Version that was last confirmed in sync
//     with the server (the value the syncer last pushed-and-acked, or the
//     server version last pulled-and-applied). The conflict check compares
//     the server's reported version against SyncedVersion.
//   - Classification is the fleet-side sync classification at the time of
//     the last sync (mirrors the unit's classification; personal units are
//     never given a sync-state row because they never leave the machine).
//   - LastSynced is the wall-clock of the last successful push/pull for
//     this unit (UTC).
//
// DIRECTIVE_001 still holds: this sidecar is owned by the units package
// and is fleet-free. The fleet syncer reads/writes it only through the
// Manager API. The package deliberately stores the *fleet* classification
// string (team_shared / org_shared) opaquely as a plain string so that
// core/units need not know the fleet vocabulary — the mapper in core/fleet
// owns the translation.
type SyncState struct {
	// UnitID is the local unit this sidecar row belongs to (primary key).
	UnitID string
	// NodeID is the fleet context-graph node id mapped to this unit.
	NodeID string
	// SyncedVersion is the last unit Version confirmed in sync.
	SyncedVersion int
	// Classification is the opaque fleet classification string recorded at
	// last sync (e.g. "team_shared", "org_shared"). Never "personal" —
	// personal units are never synced and never get a sidecar row.
	Classification string
	// LastSynced is the wall-clock of the last successful sync (UTC).
	LastSynced time.Time
}

// SyncStateStore is the persistence contract for the sync sidecar. It is
// embedded in Store so both the in-memory and SQL stores implement it.
// All methods are safe for concurrent use.
type SyncStateStore interface {
	// UpsertSyncState inserts or replaces the sidecar row for st.UnitID.
	// LastSynced is set to the current wall clock when zero.
	UpsertSyncState(ctx context.Context, st SyncState) (SyncState, error)

	// GetSyncState returns the sidecar row for a unit. ErrSyncStateNotFound
	// when the unit has no sidecar row (i.e. has never synced).
	GetSyncState(ctx context.Context, unitID string) (SyncState, error)

	// GetSyncStateByNodeID returns the sidecar row whose NodeID matches.
	// ErrSyncStateNotFound when no row maps to that node id. Used by the
	// pull path to find the local unit a server node maps to without
	// re-discovery.
	GetSyncStateByNodeID(ctx context.Context, nodeID string) (SyncState, error)

	// ListSyncState returns every sidecar row, ordered by UnitID ASC.
	ListSyncState(ctx context.Context) ([]SyncState, error)

	// DeleteSyncState removes the sidecar row for a unit. A no-op (nil) when
	// the row does not exist.
	DeleteSyncState(ctx context.Context, unitID string) error
}
