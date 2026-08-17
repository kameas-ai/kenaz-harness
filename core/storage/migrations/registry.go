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

// Pending returns every registered migration that has no effective
// applied row in the ledger, in ascending version order.
//
// SELECTION IS SET MEMBERSHIP, NOT A HIGH-WATER MARK. This distinction is
// the whole point of the function and it is load-bearing, so it is worth
// stating why.
//
// The version space is SHARED across missions (see CanonicalBlocks): the
// sessions block is 300-399, user-slash-commands is 1000-1099, units is
// 1100-1199. Missions do not land in block order — units/1100..1103 landed
// in June 2026, sessions/0332..0335 landed after it. A max-based rule
// ("apply everything above max(applied)") reads maxApplied=1103 on any
// database that already carries the units rows and concludes that every
// sessions migration numbered 332+ is already done. They are never applied,
// on any upgraded install, ever. That shipped as v0.63.0 and made "Start
// session" fail with `no such column: move_history_mode`.
//
// Set membership has none of that coupling: a migration is pending iff the
// ledger has no applied row for its version, whatever else is in the ledger.
//
// Behaviour on the awkward ledger shapes:
//
//   - ROLLED-BACK entries. A version whose most recent row is
//     action=rolled_back is not applied, so it becomes pending again. That
//     matches Rollback's own "most recent row wins" rule.
//   - DUPLICATE rows for one version. Applied() returns rows in rowid
//     (insertion) order and the last write for a version wins, so a
//     rolled_back-then-reapplied version reads as applied.
//   - UNKNOWN entries — ledger rows for versions with no registered
//     migration (a mission whose code was removed). They are simply not
//     consulted: the selection walks the REGISTERED set and asks the ledger
//     about each one. An unknown row can never crash or skew it.
//     (VerifyLedger is the surface that reports those; see runner.go.)
func (r *Registry) Pending() ([]Migration, error) {
	rows, err := r.Applied()
	if err != nil {
		return nil, err
	}
	applied := effectiveAppliedVersions(rows)
	all := r.All() // already ascending by version
	out := make([]Migration, 0, len(all))
	for _, m := range all {
		if _, done := applied[m.Version]; done {
			continue
		}
		out = append(out, m)
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
