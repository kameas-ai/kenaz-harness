package units

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// memStore is the in-memory Store implementation. Backed by maps
// guarded by an RWMutex; appropriate for test scale.
type memStore struct {
	mu       sync.RWMutex
	units    map[string]Unit
	versions []UnitVersion
	edges    []Edge
	now      func() time.Time
	idGen    func() (string, error)
}

// MemStoreOption configures a memStore at construction.
type MemStoreOption func(*memStore)

// WithMemClock overrides the wall-clock source for tests.
func WithMemClock(now func() time.Time) MemStoreOption {
	return func(s *memStore) {
		if now != nil {
			s.now = now
		}
	}
}

// WithMemIDGen overrides the id generator for tests.
func WithMemIDGen(gen func() (string, error)) MemStoreOption {
	return func(s *memStore) {
		if gen != nil {
			s.idGen = gen
		}
	}
}

// NewMemoryStore returns an in-memory Store. Useful for tests.
func NewMemoryStore(opts ...MemStoreOption) Store {
	s := &memStore{
		units: map[string]Unit{},
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultUnitID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *memStore) Create(_ context.Context, u Unit) (Unit, error) {
	if err := validateUnit(u); err != nil {
		return Unit{}, err
	}
	if u.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Unit{}, fmt.Errorf("units: id gen: %w", err)
		}
		u.ID = id
	}
	now := s.now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = u.CreatedAt
	u.Version = 0
	u.Metadata = normaliseMetadata(u.Metadata)

	s.mu.Lock()
	s.units[u.ID] = u
	s.mu.Unlock()
	return u, nil
}

func (s *memStore) Get(_ context.Context, id string) (Unit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.units[id]
	if !ok {
		return Unit{}, ErrUnitNotFound
	}
	return u, nil
}

func (s *memStore) List(_ context.Context, filter UnitFilter) ([]Unit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Unit, 0, len(s.units))
	for _, u := range s.units {
		if filter.Scope != "" && u.Scope != filter.Scope {
			continue
		}
		if filter.ScopeID != "" && u.ScopeID != filter.ScopeID {
			continue
		}
		if filter.Kind != "" && u.Kind != filter.Kind {
			continue
		}
		if filter.Classification != "" && u.Classification != filter.Classification {
			continue
		}
		if filter.LoadPolicy != "" && u.LoadPolicy != filter.LoadPolicy {
			continue
		}
		out = append(out, u)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) Update(_ context.Context, id, body string, metadata []byte) (Unit, error) {
	meta := normaliseMetadata(metadata)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.units[id]
	if !ok {
		return Unit{}, ErrUnitNotFound
	}
	newVersion := u.Version + 1

	// Append history.
	v := UnitVersion{
		ID:        int64(len(s.versions) + 1),
		UnitID:    id,
		Version:   newVersion,
		Body:      body,
		Metadata:  json.RawMessage(meta),
		CreatedAt: now,
	}
	s.versions = append(s.versions, v)

	// Mutate unit.
	u.Version = newVersion
	u.Body = body
	u.Metadata = json.RawMessage(meta)
	u.UpdatedAt = now
	s.units[id] = u
	return u, nil
}

func (s *memStore) AddEdge(ctx context.Context, e Edge) (Edge, error) {
	if !validEdgeKind(e.Kind) {
		return Edge{}, fmt.Errorf("%w: %q", ErrUnsupportedEdgeKind, e.Kind)
	}
	if e.FromID == "" || e.ToID == "" {
		return Edge{}, fmt.Errorf("units: AddEdge: FromID and ToID are required")
	}

	// Verify both units exist.
	if _, err := s.Get(ctx, e.FromID); err != nil {
		return Edge{}, fmt.Errorf("units: AddEdge: from unit: %w", err)
	}
	if _, err := s.Get(ctx, e.ToID); err != nil {
		return Edge{}, fmt.Errorf("units: AddEdge: to unit: %w", err)
	}

	if e.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Edge{}, fmt.Errorf("units: edge id gen: %w", err)
		}
		e.ID = id
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = s.now()
	}
	e.Version = 1

	s.mu.Lock()
	s.edges = append(s.edges, e)
	s.mu.Unlock()
	return e, nil
}

func (s *memStore) ListEdges(_ context.Context, unitID string) ([]Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Edge
	for _, e := range s.edges {
		if e.FromID == unitID || e.ToID == unitID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) ListVersions(_ context.Context, unitID string) ([]UnitVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []UnitVersion
	for _, v := range s.versions {
		if v.UnitID == unitID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// ensure memStore implements Store at compile time.
var _ Store = (*memStore)(nil)
