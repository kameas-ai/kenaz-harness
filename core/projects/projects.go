// Package projects owns the harness's top-level Project entity and its
// CRUD lifecycle. A Project groups related sessions; sessions outside
// any project are "loose". The mission delivers project metadata only —
// scoped attachments and scoped memory live in later WPs of the
// context-library mission (see kitty-specs/context-library-01KQ3MF1).
//
// DIRECTIVE_001: this package is the single owner of the projects table;
// all read/write access by other packages must go through Manager.
package projects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Project is the durable representation of a project entity.
type Project struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Sentinel errors. Stable typed errors so callers can errors.Is.
var (
	// ErrNotFound is returned when a project id has no matching row.
	ErrNotFound = errors.New("projects: not found")

	// ErrNameRequired is returned when Create or Rename receives an
	// empty / whitespace-only name.
	ErrNameRequired = errors.New("projects: name cannot be empty")

	// ErrProjectExists is returned when Create receives an id that is
	// already present (manager-supplied id collision).
	ErrProjectExists = errors.New("projects: already exists")
)

// Store is the persistence contract Manager consumes. Two implementations
// ship in this package: an in-memory store (memStore, returned by
// NewMemoryStore) used by unit tests, and a SQL-backed store
// (sqlStore, returned by NewSQLStore) for runtime.
//
// All methods are safe for concurrent use.
type Store interface {
	Create(ctx context.Context, p Project) error
	Get(ctx context.Context, id string) (Project, error)
	List(ctx context.Context) ([]Project, error)
	Rename(ctx context.Context, id, name string, now time.Time) error
	UpdateDescription(ctx context.Context, id, description string, now time.Time) error
	Delete(ctx context.Context, id string) error
}

// IDGen is the project-id generator. Tests override; production uses
// the default crypto-random hex.
type IDGen func() (string, error)

// Manager is the public facade for project persistence. Safe for
// concurrent use.
type Manager struct {
	store Store
	now   func() time.Time
	idGen IDGen

	mu sync.Mutex // serialize id assignment on Create
}

// ManagerOption configures a Manager at construction time.
type ManagerOption func(*Manager)

// WithClock overrides the wall-clock source. Tests pin this for
// deterministic timestamps.
func WithClock(now func() time.Time) ManagerOption {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithIDGen overrides the id generator.
func WithIDGen(gen IDGen) ManagerOption {
	return func(m *Manager) {
		if gen != nil {
			m.idGen = gen
		}
	}
}

// NewManager constructs a Manager. The Store is required.
func NewManager(store Store, opts ...ManagerOption) *Manager {
	if store == nil {
		panic("projects.NewManager: nil store")
	}
	m := &Manager{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
		idGen: defaultIDGen,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Create allocates a new project with the given name + description.
//
// TODO(audit-wired): emit a `project.created` audit event once a
// process-wide event.Emitter is threaded through Manager (the rpc
// layer marks the emitter nil today; see core/rpc/api.go newLLMStack
// audit comment). The event payload should carry {project_id, name,
// created_at}; chain consistency is the reason we do not synthesize
// a transient emitter at the call site.
func (m *Manager) Create(ctx context.Context, name, description string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Project{}, ErrNameRequired
	}
	id, err := m.idGen()
	if err != nil {
		return Project{}, fmt.Errorf("projects: id gen: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	p := Project{
		ID:          id,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := m.store.Create(ctx, p); err != nil {
		return Project{}, err
	}
	return p, nil
}

// Get returns one project by id.
func (m *Manager) Get(ctx context.Context, id string) (Project, error) {
	return m.store.Get(ctx, id)
}

// List returns every project in creation order (oldest first).
func (m *Manager) List(ctx context.Context) ([]Project, error) {
	return m.store.List(ctx)
}

// Rename changes a project's display name.
func (m *Manager) Rename(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	return m.store.Rename(ctx, id, name, m.now())
}

// UpdateDescription replaces the project's description (may be empty).
func (m *Manager) UpdateDescription(ctx context.Context, id, description string) error {
	return m.store.UpdateDescription(ctx, id, description, m.now())
}

// Delete removes a project. Sessions referencing the project must be
// detached or deleted by the caller before invoking Delete; the SQL
// FK is ON DELETE SET NULL so leftover sessions become loose, but the
// view-level surface coordinates the cascade explicitly.
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.store.Delete(ctx, id)
}

// defaultIDGen returns a 16-byte hex id (32 chars). Matches the
// session manager's id shape so the two namespaces look uniform in
// audit and storage rows.
func defaultIDGen() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
