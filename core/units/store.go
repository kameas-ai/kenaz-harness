package units

import (
	"context"
)

// Store is the persistence contract Manager consumes. Two
// implementations ship in this package: an in-memory store (memStore,
// returned by NewMemoryStore) used by unit tests, and a SQL-backed
// store (sqlStore, returned by NewSQLStore) for runtime.
//
// All methods are safe for concurrent use.
type Store interface {
	// SyncStateStore holds the per-unit fleet sync sidecar (node_id,
	// synced_version, classification, last_synced). It is part of the
	// store contract so re-sync needs no re-discovery and conflict checks
	// have a durable baseline. core/units stays fleet-free: the sidecar
	// stores the fleet classification opaquely as a string.
	SyncStateStore

	// Create writes a new Unit row. If u.ID is empty a fresh ULID is
	// minted; if u.CreatedAt/UpdatedAt are zero the current wall clock
	// is used. Version is forced to 0 on create. Returns the inserted
	// Unit (with id and timestamps populated).
	Create(ctx context.Context, u Unit) (Unit, error)

	// Get returns one row by id. ErrUnitNotFound when the id has no
	// match.
	Get(ctx context.Context, id string) (Unit, error)

	// List returns rows matching filter, ordered by created_at DESC.
	List(ctx context.Context, filter UnitFilter) ([]Unit, error)

	// Update bumps the unit's Version (max+1), writes the new Body,
	// Metadata and UpdatedAt to the units row, and appends a history
	// row to unit_versions. Returns the updated Unit.
	// ErrUnitNotFound when id has no match.
	Update(ctx context.Context, id string, body string, metadata []byte) (Unit, error)

	// UpdateAtVersion atomically refuses a stale base with ErrVersionConflict.
	UpdateAtVersion(ctx context.Context, id string, baseVersion int, body string, metadata []byte) (Unit, error)

	// AddEdge writes a new Edge row. If e.ID is empty a fresh ULID is
	// minted. Returns the inserted Edge. Validates EdgeKind and that
	// both FromID and ToID refer to existing units.
	AddEdge(ctx context.Context, e Edge) (Edge, error)

	// CreateWithEdge inserts a new Unit AND an Edge originating from it in
	// a single atomic operation: the edge's FromID is set to the newly
	// minted unit id, and if either write fails NEITHER is persisted (no
	// orphaned unit without its lineage edge). e.ToID must reference an
	// existing unit; e.Kind must be valid. Returns the inserted Unit + Edge.
	// This is the transactional primitive behind Promote.
	CreateWithEdge(ctx context.Context, u Unit, e Edge) (Unit, Edge, error)

	// CreateWithSyncState inserts a new Unit (Create semantics) AND its
	// fleet sync sidecar row in a single atomic operation: if either write
	// fails NEITHER is persisted. st.UnitID is set to the created unit's id
	// and st.SyncedLocalVersion to its Version (always 0 after a create);
	// the caller supplies NodeID, SyncedServerVersion and Classification.
	// This is the transactional primitive behind the fleet pull path's
	// first-sight create (units-debt-01UNITD01 FR-4): a unit without its
	// baseline is read as locally-new by the next push and is unreachable
	// by node id on the next pull.
	CreateWithSyncState(ctx context.Context, u Unit, st SyncState) (Unit, SyncState, error)

	// UpdateWithSyncState applies an Update (version bump + history row)
	// AND upserts the sync sidecar row in a single atomic operation, but
	// only if the persisted Version still equals baseVersion — checked
	// inside the same transaction; otherwise ErrVersionConflict and
	// nothing is written. st.UnitID is set to id and st.SyncedLocalVersion
	// to the unit's Version after the bump. The transactional primitive
	// behind the fleet pull path's clean fast-forward
	// (units-debt-01UNITD01 FR-4): the base check stops a local edit made
	// after the syncer's read from being overwritten and baselined.
	UpdateWithSyncState(ctx context.Context, id string, baseVersion int, body string, metadata []byte, st SyncState) (Unit, SyncState, error)

	// DeleteWithSyncState removes a sync-managed (team/org) unit together
	// with its version history, every edge touching it and its sync
	// sidecar row, in a single atomic operation. It is the transactional
	// primitive behind the fleet pull path's tombstone for a node this user
	// does NOT own (a shared node that was deleted, or quarantined and so
	// withdrawn from this member): the stored body AND every historical
	// body must be gone, and the sidecar must go with them so a later clean
	// re-appearance is a first sight. hadUnpushedEdits reports, read inside
	// the same transaction, whether the unit's Version was ahead of the
	// sidecar's SyncedLocalVersion (local edits never synced). ErrPersonalUnit
	// (nothing written) when the unit is personal. When the unit row is
	// already gone, any orphaned sidecar row is still removed and the call
	// returns (false, nil).
	DeleteWithSyncState(ctx context.Context, unitID string) (hadUnpushedEdits bool, err error)

	// DemoteToPersonal reclassifies a sync-managed unit as personal and
	// removes its sync sidecar row, in a single atomic operation. Body,
	// version, history and edges are kept. It is the pull path's tombstone
	// for a node this user OWNS (deleted server-side by someone else, e.g.
	// a project delete or a retention downgrade): the user's own work stays
	// on the machine, is never pushed again (personal), and the node id no
	// longer maps to it. ErrUnitNotFound when the unit does not exist.
	DemoteToPersonal(ctx context.Context, unitID string) error

	// ListEdges returns all edges where FromID or ToID matches id,
	// ordered by created_at ASC.
	ListEdges(ctx context.Context, unitID string) ([]Edge, error)

	// ListVersions returns all version history rows for a unit, ordered
	// by version ASC. Returns an empty slice (not ErrUnitNotFound) when
	// the unit exists but has no version rows.
	ListVersions(ctx context.Context, unitID string) ([]UnitVersion, error)

	// RegisterMigrations is intentionally NOT part of Store — migrations
	// are registered separately via RegisterMigrations(registry).
}
