package migrations

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Registry is the concrete MigrationRegistry. Consuming missions call
// Register to declare their migrations; the runner calls Apply to bring
// the database up to date.
//
// Registry is safe for concurrent use during boot.
type Registry struct {
	mu sync.Mutex

	// migrations is keyed by Version for O(1) collision check.
	migrations    map[int]Migration
	byID          map[string]int
	registerOrder []int // versions in registration order (used for stable Pending output on ties)

	exec Executor
	now  Now
	emit EmitFunc
}

// NewRegistry constructs a Registry bound to an Executor (typically the
// storage package's libSQL connection adapter). emit is invoked once per
// migration apply/rollback; pass a no-op when not yet wired (the storage
// Open path injects the real one during boot).
func NewRegistry(exec Executor, now Now, emit EmitFunc) *Registry {
	if now == nil {
		now = defaultNow
	}
	if emit == nil {
		emit = func(ctx context.Context, kind string, payload map[string]any) {}
	}
	return &Registry{
		migrations: map[int]Migration{},
		byID:       map[string]int{},
		exec:       exec,
		now:        now,
		emit:       emit,
	}
}

// Register validates and adds a migration. It returns:
//
//   - ErrInvalidMigration on missing required fields
//   - ErrUnknownOwningMission if OwningMission has no reserved block
//   - ErrVersionOutOfBlock if Version falls outside that block
//   - ErrVersionCollision if Version is already registered
//   - ErrMigrationIDCollision if ID is already registered
//
// On success the migration is stored and (if ContentHash was empty) the
// hash is computed from UpSource.
func (r *Registry) Register(m Migration) error {
	if m.ID == "" {
		return fmt.Errorf("%w: ID required", ErrInvalidMigration)
	}
	if m.Version <= 0 {
		return fmt.Errorf("%w: Version must be > 0", ErrInvalidMigration)
	}
	if m.OwningMission == "" {
		return fmt.Errorf("%w: OwningMission required", ErrInvalidMigration)
	}
	if m.Up == nil {
		return fmt.Errorf("%w: Up function required", ErrInvalidMigration)
	}
	// Down is optional; rollback is only available for migrations that
	// declare a Down.

	block, ok := LookupBlock(m.OwningMission)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOwningMission, m.OwningMission)
	}
	if !block.Contains(m.Version) {
		return fmt.Errorf("%w: %s/%d outside [%d,%d]",
			ErrVersionOutOfBlock, m.OwningMission, m.Version, block.Min, block.Max)
	}

	if m.ContentHash == "" {
		if m.UpSource == "" {
			return fmt.Errorf("%w: either ContentHash or UpSource required", ErrInvalidMigration)
		}
		m.ContentHash = HashSQL(m.UpSource)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.migrations[m.Version]; exists {
		return fmt.Errorf("%w: version %d", ErrVersionCollision, m.Version)
	}
	if _, exists := r.byID[m.ID]; exists {
		return fmt.Errorf("%w: %s", ErrMigrationIDCollision, m.ID)
	}
	r.migrations[m.Version] = m
	r.byID[m.ID] = m.Version
	r.registerOrder = append(r.registerOrder, m.Version)
	return nil
}

// All returns a sorted-by-version slice of all registered migrations.
func (r *Registry) All() []Migration {
	r.mu.Lock()
	versions := make([]int, 0, len(r.migrations))
	for v := range r.migrations {
		versions = append(versions, v)
	}
	r.mu.Unlock()
	sort.Ints(versions)
	out := make([]Migration, 0, len(versions))
	r.mu.Lock()
	for _, v := range versions {
		out = append(out, r.migrations[v])
	}
	r.mu.Unlock()
	return out
}

// Lookup returns the migration registered at version v.
func (r *Registry) Lookup(version int) (Migration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.migrations[version]
	return m, ok
}

// Pending returns the migrations whose version is greater than the
// highest version present in the ledger, in ascending order.
func (r *Registry) Pending() ([]Migration, error) {
	applied, err := r.Applied()
	if err != nil {
		return nil, err
	}
	maxApplied := 0
	for _, e := range applied {
		if e.Action == LedgerActionApplied && e.Version > maxApplied {
			maxApplied = e.Version
		}
	}
	all := r.All()
	out := make([]Migration, 0)
	for _, m := range all {
		if m.Version > maxApplied {
			out = append(out, m)
		}
	}
	return out, nil
}

// Reset (test-only) clears all registrations.
func (r *Registry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.migrations = map[int]Migration{}
	r.byID = map[string]int{}
	r.registerOrder = nil
}
