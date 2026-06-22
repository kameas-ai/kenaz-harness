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

	// AddEdge writes a new Edge row. If e.ID is empty a fresh ULID is
	// minted. Returns the inserted Edge. Validates EdgeKind and that
	// both FromID and ToID refer to existing units.
	AddEdge(ctx context.Context, e Edge) (Edge, error)

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
