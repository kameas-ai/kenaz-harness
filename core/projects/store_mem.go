package projects

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
)

// memStore is the in-memory Store implementation. Backed by a map
// guarded by an RWMutex; appropriate for test scale.
type memStore struct {
	mu       sync.RWMutex
	projects map[string]Project
}

// NewMemoryStore returns an in-memory Store. Useful for tests.
func NewMemoryStore() Store {
	return &memStore{
		projects: map[string]Project{},
	}
}

func (s *memStore) Create(_ context.Context, p Project) error {
	if p.Name == "" {
		return ErrNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[p.ID]; ok {
		return ErrProjectExists
	}
	s.projects[p.ID] = p
	return nil
}

func (s *memStore) Get(_ context.Context, id string) (Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	return p, nil
}

func (s *memStore) List(_ context.Context) ([]Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) Rename(_ context.Context, id, name string, now time.Time) error {
	if name == "" {
		return ErrNameRequired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	p.Name = name
	p.UpdatedAt = now
	s.projects[id] = p
	return nil
}

func (s *memStore) UpdateDescription(_ context.Context, id, description string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	p.Description = description
	p.UpdatedAt = now
	s.projects[id] = p
	return nil
}

func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.projects[id]; !ok {
		return ErrNotFound
	}
	delete(s.projects, id)
	return nil
}

func (s *memStore) SetAutonomyProfile(_ context.Context, id string, layer autonomy.Layer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	if !ok {
		return ErrNotFound
	}
	p.AutonomyLevel, p.AutonomyOverrides = cloneAutonomyLayer(layer)
	s.projects[id] = p
	return nil
}

func (s *memStore) GetAutonomyProfile(_ context.Context, id string) (autonomy.Layer, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.projects[id]
	if !ok {
		return autonomy.Layer{}, ErrNotFound
	}
	return autonomyLayerFromProject(p), nil
}
