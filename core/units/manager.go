package units

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Manager is the public API surface over the units Store. It enforces
// validation, version monotonicity, and the promoted_from edge contract
// on Promote. No fleet sync or RPC wiring — that is a later phase.
type Manager struct {
	store Store
}

// NewManager constructs a Manager. store is required.
func NewManager(store Store) *Manager {
	if store == nil {
		panic("units.NewManager: nil store")
	}
	return &Manager{store: store}
}

// Create validates and persists a new Unit. All enum fields must be
// populated. ID and timestamps are auto-generated when zero.
func (m *Manager) Create(ctx context.Context, u Unit) (Unit, error) {
	return m.store.Create(ctx, u)
}

// Get retrieves a single Unit by id.
func (m *Manager) Get(ctx context.Context, id string) (Unit, error) {
	return m.store.Get(ctx, id)
}

// List returns units matching filter, ordered by created_at DESC.
func (m *Manager) List(ctx context.Context, filter UnitFilter) ([]Unit, error) {
	return m.store.List(ctx, filter)
}

// Update bumps the unit's version, writes the new body and metadata,
// and appends a history row to unit_versions. metadata may be nil
// (stored as "{}").
func (m *Manager) Update(ctx context.Context, id, body string, metadata json.RawMessage) (Unit, error) {
	if id == "" {
		return Unit{}, errors.New("units: empty id")
	}
	return m.store.Update(ctx, id, body, []byte(metadata))
}

// AddEdge creates a directed edge between two units.
func (m *Manager) AddEdge(ctx context.Context, e Edge) (Edge, error) {
	return m.store.AddEdge(ctx, e)
}

// ListEdges returns all edges where the given unit is either the source
// or the target, ordered by created_at ASC.
func (m *Manager) ListEdges(ctx context.Context, unitID string) ([]Edge, error) {
	if unitID == "" {
		return nil, errors.New("units: empty unitID")
	}
	return m.store.ListEdges(ctx, unitID)
}

// ListVersions returns the full revision history for a unit, ordered
// by version ASC. Returns an empty slice when no history exists yet.
func (m *Manager) ListVersions(ctx context.Context, unitID string) ([]UnitVersion, error) {
	if unitID == "" {
		return nil, errors.New("units: empty unitID")
	}
	return m.store.ListVersions(ctx, unitID)
}

// ── Sync sidecar (Phase-2) ──────────────────────────────────────────────
//
// The fleet UnitSyncer reaches the sidecar exclusively through these
// Manager methods (DIRECTIVE_001: core/units is the single owner of the
// unit_sync_state table). core/units stays fleet-free — the classification
// recorded here is an opaque string the fleet mapper supplies.

// UpsertSyncState records (or replaces) the sync sidecar for a unit:
// the fleet node id it maps to, the version last confirmed in sync, the
// opaque fleet classification, and the last-sync time. Personal units must
// never be passed here — the syncer is responsible for skipping them.
func (m *Manager) UpsertSyncState(ctx context.Context, st SyncState) (SyncState, error) {
	if st.UnitID == "" {
		return SyncState{}, errors.New("units: UpsertSyncState: empty UnitID")
	}
	return m.store.UpsertSyncState(ctx, st)
}

// GetSyncState returns the sidecar for a unit, or ErrSyncStateNotFound when
// the unit has never been synced.
func (m *Manager) GetSyncState(ctx context.Context, unitID string) (SyncState, error) {
	if unitID == "" {
		return SyncState{}, errors.New("units: GetSyncState: empty unitID")
	}
	return m.store.GetSyncState(ctx, unitID)
}

// GetSyncStateByNodeID resolves a fleet node id back to its sidecar row
// (and thus its local unit) without re-discovery. ErrSyncStateNotFound
// when no local unit maps to that node id yet.
func (m *Manager) GetSyncStateByNodeID(ctx context.Context, nodeID string) (SyncState, error) {
	if nodeID == "" {
		return SyncState{}, errors.New("units: GetSyncStateByNodeID: empty nodeID")
	}
	return m.store.GetSyncStateByNodeID(ctx, nodeID)
}

// ListSyncState returns every sidecar row, ordered by unit id.
func (m *Manager) ListSyncState(ctx context.Context) ([]SyncState, error) {
	return m.store.ListSyncState(ctx)
}

// DeleteSyncState drops the sidecar row for a unit.
func (m *Manager) DeleteSyncState(ctx context.Context, unitID string) error {
	if unitID == "" {
		return errors.New("units: DeleteSyncState: empty unitID")
	}
	return m.store.DeleteSyncState(ctx, unitID)
}

// ListDirty returns the units whose local Version is ahead of the version
// last confirmed in sync (or which have no sidecar yet), restricted to the
// given classification. It is the push-up worklist: every dirty team/org
// unit the syncer must offer to fleet. Personal units are never returned
// even if classification is ClassPersonal — they are local-only (NFR-005).
//
// A unit is dirty when:
//   - it has no sidecar row (never synced), OR
//   - its current Version > sidecar.SyncedVersion.
func (m *Manager) ListDirty(ctx context.Context, classification Classification) ([]Unit, error) {
	if classification == ClassPersonal {
		return nil, nil // personal units never sync
	}
	all, err := m.store.List(ctx, UnitFilter{Classification: classification})
	if err != nil {
		return nil, err
	}
	out := make([]Unit, 0, len(all))
	for _, u := range all {
		st, err := m.store.GetSyncState(ctx, u.ID)
		if err != nil {
			if errors.Is(err, ErrSyncStateNotFound) {
				out = append(out, u) // never synced → dirty
				continue
			}
			return nil, err
		}
		if u.Version > st.SyncedVersion {
			out = append(out, u)
		}
	}
	return out, nil
}

// Promote creates a new Unit that is a copy of src with the supplied
// newScope and newClassification, then adds a promoted_from edge from
// the new unit to the original. The original unit is left untouched.
//
// The new unit inherits Kind, LoadPolicy, Title, Body, and Metadata
// from src. newScopeID must be set when newScope is ScopeProject or
// ScopeSession; it is ignored for ScopeGlobal.
//
// Returns (newUnit, edge, nil) on success.
func (m *Manager) Promote(ctx context.Context, srcID string, newScope Scope, newScopeID string, newClass Classification) (Unit, Edge, error) {
	if srcID == "" {
		return Unit{}, Edge{}, errors.New("units: empty srcID")
	}
	if !validScope(newScope) {
		return Unit{}, Edge{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, newScope)
	}
	if !validClassification(newClass) {
		return Unit{}, Edge{}, fmt.Errorf("%w: %q", ErrUnsupportedClassification, newClass)
	}

	src, err := m.store.Get(ctx, srcID)
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: Promote: get source: %w", err)
	}

	promoted := Unit{
		Kind:           src.Kind,
		Scope:          newScope,
		ScopeID:        newScopeID,
		Classification: newClass,
		LoadPolicy:     src.LoadPolicy,
		Title:          src.Title,
		Body:           src.Body,
		Metadata:       src.Metadata,
	}
	// Atomic: the promoted unit and its promoted_from lineage edge are
	// written in one transaction, so a failure never leaves an orphaned
	// unit without its provenance edge. (CreateWithEdge sets the edge's
	// FromID to the newly-minted unit id.)
	newUnit, edge, err := m.store.CreateWithEdge(ctx, promoted, Edge{
		ToID: srcID,
		Kind: EdgePromotedFrom,
	})
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: Promote: %w", err)
	}

	return newUnit, edge, nil
}
