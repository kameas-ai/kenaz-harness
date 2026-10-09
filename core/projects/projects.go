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

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// Project is the durable representation of a project entity.
type Project struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// AutonomyLevel is this project's tier override
	// (autonomy-dial-01KR3M2A WP02). nil means "inherit from global."
	// Persisted in projects.autonomy_level via migration 0316.
	AutonomyLevel *autonomy.Tier `json:"autonomyLevel,omitempty"`
	// AutonomyOverrides are the per-knob overrides this project pinned.
	// Empty / nil means "no overrides at this layer." Persisted as a
	// JSON blob in projects.autonomy_overrides via migration 0316.
	AutonomyOverrides map[autonomy.Knob]any `json:"autonomyOverrides,omitempty"`
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

	// SetAutonomyProfile persists the per-project autonomy.Layer
	// (autonomy-dial-01KR3M2A WP02). An empty Layer (nil Level + empty
	// Overrides) round-trips as both columns NULL — the upstream
	// resolver then falls back to the global / tier-default chain.
	SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error
	// GetAutonomyProfile loads the per-project autonomy.Layer. Returns
	// the empty Layer when both columns are NULL.
	GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error)

	// SetToolExposure persists the project's tool-exposure override layer
	// (projects.tool_exposure, migration sessions/0347-tool-exposure). A
	// zero Exposure clears it (NULL). Callers validate (Manager does).
	SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error
	// GetToolExposure loads the project's override layer; the zero
	// Exposure when none is set.
	GetToolExposure(ctx context.Context, id string) (toolexposure.Exposure, error)
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

	// deleteObservers run after Delete removes the project row
	// (artifacts-as-units-01DOGF0C WP03, spec FR-6): dependants that
	// reference a project id without a foreign key (core/units scope_id)
	// clean themselves up here.
	deleteObserversMu sync.Mutex
	deleteObservers   []DeleteObserver
}

// DeleteObserver is called after a project row has been deleted. A
// returned error is surfaced from Manager.Delete.
type DeleteObserver func(ctx context.Context, projectID string) error

// AddDeleteObserver registers fn to run after every successful Delete.
// Append-only, safe for concurrent use. A nil fn is ignored.
func (m *Manager) AddDeleteObserver(fn DeleteObserver) {
	if m == nil || fn == nil {
		return
	}
	m.deleteObserversMu.Lock()
	m.deleteObservers = append(m.deleteObservers, fn)
	m.deleteObserversMu.Unlock()
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
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	m.deleteObserversMu.Lock()
	observers := append([]DeleteObserver(nil), m.deleteObservers...)
	m.deleteObserversMu.Unlock()
	var errs []error
	for _, fn := range observers {
		if err := fn(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("projects: %s deleted, but dependant cleanup failed: %w", id, errors.Join(errs...))
	}
	return nil
}

// SetAutonomyProfile persists the per-project autonomy.Layer
// (autonomy-dial-01KR3M2A WP03 RPC plumbing). An empty Layer (nil
// Level + empty Overrides) clears both columns so the resolver folds
// through to the global / tier-default chain.
func (m *Manager) SetAutonomyProfile(ctx context.Context, id string, layer autonomy.Layer) error {
	return m.store.SetAutonomyProfile(ctx, id, layer)
}

// GetAutonomyProfile loads the per-project autonomy.Layer. Returns the
// empty Layer when the project has no overrides.
func (m *Manager) GetAutonomyProfile(ctx context.Context, id string) (autonomy.Layer, error) {
	return m.store.GetAutonomyProfile(ctx, id)
}

// SetToolExposure validates and persists the project's tool-exposure
// override layer; a zero Exposure clears it.
func (m *Manager) SetToolExposure(ctx context.Context, id string, e toolexposure.Exposure) error {
	if err := e.Validate(); err != nil {
		return err
	}
	return m.store.SetToolExposure(ctx, id, e)
}

// ProjectToolExposure loads the project's override layer. *Manager
// satisfies toolexposure.ProjectSource with it.
func (m *Manager) ProjectToolExposure(ctx context.Context, id string) (toolexposure.Exposure, error) {
	return m.store.GetToolExposure(ctx, id)
}

var _ toolexposure.ProjectSource = (*Manager)(nil)

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
