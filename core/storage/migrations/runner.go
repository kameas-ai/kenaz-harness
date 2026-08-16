package migrations

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// VerifyLedger runs the startup checks against the registered migrations
// and the on-disk ledger:
//
//   - Every applied row's content_hash must match the registered
//     migration's canonical ContentHash. Mismatch -> ErrLedgerHashMismatch.
//   - Every applied row must reference a registered migration. Missing
//     -> ErrSchemaGap (with a "unknown ledger entry" message).
//   - PER OWNING MISSION, the applied versions must be a contiguous prefix
//     of that mission's registered migrations. A registered migration below
//     its own mission's highest applied version but absent from the ledger
//     -> ErrSchemaGap.
//
// WHY PER-MISSION AND NOT GLOBAL [min,max]. The original rule required every
// registered migration between min(applied) and max(applied) to be applied,
// across all missions at once. The version space is shared (CanonicalBlocks)
// and missions do NOT land in block order: units/1100..1103 shipped before
// sessions/0332..0335 was written. On any database carrying the units rows,
// max(applied)=1103, so a freshly-added and entirely legitimate sessions/0332
// sits inside [min,max] unapplied — the global rule calls that a schema gap
// on every healthy install, which is the same shared-version-space blindness
// that broke Pending (registry.go).
//
// A migration is only ever ordered against its own mission's migrations, so
// that is the only axis on which "a migration was skipped" is meaningful.
// Within a mission the check is exactly as strong as before: sessions/0334
// applied while sessions/0333 is missing is still ErrSchemaGap.
//
// Rollback rows are tolerated and used to compute the "current" applied
// set: a rolled-back version is no longer "applied" for the purposes of
// the contiguity check.
func (r *Registry) VerifyLedger(ctx context.Context) error {
	rows, err := r.Applied()
	if err != nil {
		return err
	}
	// Compute the effective applied set: a version is applied if its
	// most recent row is action=applied; rolled_back if the most recent
	// is rolled_back.
	latest := latestLedgerRows(rows)

	for v, e := range latest {
		if e.Action != LedgerActionApplied {
			continue
		}
		mig, ok := r.Lookup(v)
		if !ok {
			return fmt.Errorf("%w: ledger has applied entry for version %d (id=%q) but no registered migration",
				ErrSchemaGap, v, e.ID)
		}
		if mig.ContentHash != e.ContentHash {
			return fmt.Errorf("%w: version %d (id=%q): registered hash %s, ledger hash %s",
				ErrLedgerHashMismatch, v, e.ID, mig.ContentHash, e.ContentHash)
		}
	}

	// Per-mission contiguity check on the effective applied set.
	applied := effectiveAppliedVersions(rows)
	if len(applied) == 0 {
		return nil
	}
	// Highest applied version per owning mission, attributed via the
	// REGISTERED migration (authoritative) rather than the ledger row's
	// owning_mission text, so a drifted row cannot re-file a version under
	// the wrong mission and hide a gap.
	all := r.All() // ascending by version
	highWater := map[string]int{}
	for _, m := range all {
		if _, ok := applied[m.Version]; !ok {
			continue
		}
		if m.Version > highWater[m.OwningMission] {
			highWater[m.OwningMission] = m.Version
		}
	}
	for _, m := range all {
		if _, ok := applied[m.Version]; ok {
			continue
		}
		// Unapplied ABOVE its mission's high-water mark is the normal
		// pending state; unapplied BELOW it means a migration was skipped.
		if m.Version < highWater[m.OwningMission] {
			return fmt.Errorf("%w: registered migration v%d (%s) is not applied, but %s/%d above it is",
				ErrSchemaGap, m.Version, m.ID, m.OwningMission, highWater[m.OwningMission])
		}
	}
	return nil
}

// Apply runs every pending migration in version order, each inside its
// own write transaction. On failure, the failing transaction rolls back
// (no ledger row written) and Apply returns ErrMigrationFailed wrapping
// the underlying error.
//
// Apply is safe to call repeatedly; it is a no-op when no pending
// migrations remain.
func (r *Registry) Apply(ctx context.Context) error {
	if r.exec == nil {
		return fmt.Errorf("%w: no executor", ErrMigrationFailed)
	}
	pending, err := r.Pending()
	if err != nil {
		return err
	}
	for _, m := range pending {
		start := time.Now()
		err := r.exec.WriteTx(ctx, func(tx WriteTx) error {
			if err := m.Up(ctx, tx); err != nil {
				return err
			}
			return appendLedger(ctx, tx, LedgerEntry{
				Version:       m.Version,
				ID:            m.ID,
				AppliedAt:     r.now(),
				ContentHash:   m.ContentHash,
				OwningMission: m.OwningMission,
				Action:        LedgerActionApplied,
			})
		})
		if err != nil {
			r.emit(ctx, "migration_failed", map[string]any{
				"version":        m.Version,
				"id":             m.ID,
				"owning_mission": m.OwningMission,
				"error":          err.Error(),
			})
			return fmt.Errorf("%w: %s/%d (%s): %v",
				ErrMigrationFailed, m.OwningMission, m.Version, m.ID, err)
		}
		r.emit(ctx, "migration_applied", map[string]any{
			"version":        m.Version,
			"id":             m.ID,
			"owning_mission": m.OwningMission,
			"content_hash":   m.ContentHash,
			"duration_ms":    time.Since(start).Milliseconds(),
		})
	}
	return nil
}

// Rollback runs Down for every applied migration with version > toVersion,
// in reverse version order, each in its own transaction. Each step
// appends an action=rolled_back ledger entry. The original applied row
// is preserved (the ledger is append-only).
func (r *Registry) Rollback(ctx context.Context, toVersion int) error {
	if r.exec == nil {
		return fmt.Errorf("%w: no executor", ErrMigrationFailed)
	}
	rows, err := r.Applied()
	if err != nil {
		return err
	}
	// Build effective applied set: most recent row per version.
	latest := latestLedgerRows(rows)
	type todo struct {
		mig    Migration
		ledger LedgerEntry
	}
	var work []todo
	for v, e := range latest {
		if e.Action != LedgerActionApplied {
			continue
		}
		if v <= toVersion {
			continue
		}
		mig, ok := r.Lookup(v)
		if !ok {
			return fmt.Errorf("%w: cannot rollback version %d — no registered migration",
				ErrSchemaGap, v)
		}
		if mig.Down == nil {
			return fmt.Errorf("%w: migration %s (v%d) has no Down function",
				ErrInvalidMigration, mig.ID, mig.Version)
		}
		work = append(work, todo{mig: mig, ledger: e})
	}
	sort.Slice(work, func(i, j int) bool {
		return work[i].mig.Version > work[j].mig.Version // reverse order
	})

	for _, t := range work {
		start := time.Now()
		from := t.mig.Version
		err := r.exec.WriteTx(ctx, func(tx WriteTx) error {
			if err := t.mig.Down(ctx, tx); err != nil {
				return err
			}
			return appendLedger(ctx, tx, LedgerEntry{
				Version:               t.mig.Version,
				ID:                    t.mig.ID,
				AppliedAt:             r.now(),
				ContentHash:           t.mig.ContentHash,
				OwningMission:         t.mig.OwningMission,
				Action:                LedgerActionRolledBack,
				RolledBackFromVersion: &from,
			})
		})
		if err != nil {
			r.emit(ctx, "migration_failed", map[string]any{
				"version":        t.mig.Version,
				"id":             t.mig.ID,
				"owning_mission": t.mig.OwningMission,
				"error":          err.Error(),
			})
			return fmt.Errorf("%w: rollback %s/%d (%s): %v",
				ErrMigrationFailed, t.mig.OwningMission, t.mig.Version, t.mig.ID, err)
		}
		r.emit(ctx, "migration_rolled_back", map[string]any{
			"version":        t.mig.Version,
			"id":             t.mig.ID,
			"owning_mission": t.mig.OwningMission,
			"content_hash":   t.mig.ContentHash,
			"duration_ms":    time.Since(start).Milliseconds(),
		})
	}
	return nil
}

func defaultNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
