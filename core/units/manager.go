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
	newUnit, err := m.store.Create(ctx, promoted)
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: Promote: create: %w", err)
	}

	edge, err := m.store.AddEdge(ctx, Edge{
		FromID: newUnit.ID,
		ToID:   srcID,
		Kind:   EdgePromotedFrom,
	})
	if err != nil {
		return Unit{}, Edge{}, fmt.Errorf("units: Promote: add edge: %w", err)
	}

	return newUnit, edge, nil
}
