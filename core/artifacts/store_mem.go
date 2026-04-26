package artifacts

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// memStore is the in-memory Store implementation. Backed by a map
// guarded by an RWMutex; appropriate for test scale.
type memStore struct {
	mu         sync.RWMutex
	rows       map[string]Artifact
	now        func() time.Time
	idGen      func() (string, error)
	sessReader SessionProjectReader
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

// WithMemSessionProjectReader injects the session→project lookup.
func WithMemSessionProjectReader(r SessionProjectReader) MemStoreOption {
	return func(s *memStore) {
		s.sessReader = r
	}
}

// NewMemoryStore returns an in-memory Store. Useful for tests.
func NewMemoryStore(opts ...MemStoreOption) Store {
	s := &memStore{
		rows:  map[string]Artifact{},
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultArtifactID,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *memStore) Insert(_ context.Context, a Artifact) (Artifact, error) {
	if !validSource(a.Source) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedSource, a.Source)
	}
	if a.ScopeKind == "" {
		a.ScopeKind = ScopeKindSession
	}
	if !validScope(a.ScopeKind) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, a.ScopeKind)
	}
	if a.ID == "" {
		id, err := s.idGen()
		if err != nil {
			return Artifact{}, fmt.Errorf("artifacts: id gen: %w", err)
		}
		a.ID = id
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = s.now()
	}
	s.mu.Lock()
	s.rows[a.ID] = a
	s.mu.Unlock()
	return a, nil
}

func (s *memStore) Get(_ context.Context, id string) (Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.rows[id]
	if !ok {
		return Artifact{}, ErrArtifactNotFound
	}
	return a, nil
}

func (s *memStore) List(_ context.Context, filter ArtifactFilter) ([]Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Artifact, 0, len(s.rows))
	for _, a := range s.rows {
		if filter.SessionID != "" && a.SessionID != filter.SessionID {
			continue
		}
		if filter.ProjectID != "" {
			if a.ProjectID == nil || *a.ProjectID != filter.ProjectID {
				continue
			}
		}
		if filter.MimeTypePrefix != "" && !strings.HasPrefix(a.MimeType, filter.MimeTypePrefix) {
			continue
		}
		if filter.Source != "" && a.Source != filter.Source {
			continue
		}
		if filter.ScopeKind != "" && a.ScopeKind != filter.ScopeKind {
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (s *memStore) UpdateScope(ctx context.Context, id, scopeKind, scopeID string) (Artifact, error) {
	if !validScope(scopeKind) {
		return Artifact{}, fmt.Errorf("%w: %q", ErrUnsupportedScope, scopeKind)
	}
	s.mu.Lock()
	a, ok := s.rows[id]
	if !ok {
		s.mu.Unlock()
		return Artifact{}, ErrArtifactNotFound
	}
	s.mu.Unlock()

	switch scopeKind {
	case ScopeKindProject:
		if s.sessReader == nil {
			return Artifact{}, fmt.Errorf("%w: no session→project reader configured", ErrUnsupportedScope)
		}
		sessProj, err := s.sessReader.SessionProject(ctx, a.SessionID)
		if err != nil {
			return Artifact{}, fmt.Errorf("%w: %v", ErrUnsupportedScope, err)
		}
		if sessProj == "" {
			return Artifact{}, fmt.Errorf("%w: session %s has no project", ErrUnsupportedScope, a.SessionID)
		}
		if scopeID != "" && scopeID != sessProj {
			return Artifact{}, fmt.Errorf("%w: scope id %q does not match session's project %q",
				ErrUnsupportedScope, scopeID, sessProj)
		}
		a.ProjectID = &sessProj
	case ScopeKindSession:
		a.ProjectID = nil
	}
	a.ScopeKind = scopeKind
	s.mu.Lock()
	s.rows[id] = a
	s.mu.Unlock()
	return a, nil
}

func (s *memStore) Delete(_ context.Context, id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.rows[id]
	if !ok {
		return "", ErrArtifactNotFound
	}
	delete(s.rows, id)
	return a.ContentHash, nil
}

func (s *memStore) RefcountFor(_ context.Context, contentHash string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, a := range s.rows {
		if a.ContentHash == contentHash {
			n++
		}
	}
	return n, nil
}
