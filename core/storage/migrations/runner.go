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
//   - The applied versions must be a contiguous prefix of registered
//     migrations starting at min(applied). Gaps in [min,max] ->
//     ErrSchemaGap.
//
// Rollback rows are tolerated and used to compute the "current" applied
// set: a rolled-back version is no longer "applied" for the purposes of
// the contiguous-prefix check.
func (r *Registry) VerifyLedger(ctx context.Context) error {
	rows, err := r.Applied()
	if err != nil {
		return err
	}
	// Compute the effective applied set: a version is applied if its
	// most recent row is action=applied; rolled_back if the most recent
	// is rolled_back.
	latest := map[int]LedgerEntry{}
	for _, e := range rows {
		latest[e.Version] = e // rows are insertion-order, last wins
	}

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

	// Gap check on effective applied set.
	versions := make([]int, 0, len(latest))
	for v, e := range latest {
		if e.Action == LedgerActionApplied {
			versions = append(versions, v)
		}
	}
	if len(versions) == 0 {
		return nil
	}
	sort.Ints(versions)
	min := versions[0]
	max := versions[len(versions)-1]
	// We expect all registered migrations between min and max to be
	// present in the applied set. Otherwise, a migration was created
	// out of order or a row is missing.
	all := r.All()
	for _, m := range all {
		if m.Version < min || m.Version > max {
			continue
		}
		found := false
		for _, v := range versions {
			if v == m.Version {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: registered migration v%d (%s) not present in ledger between [%d,%d]",
				ErrSchemaGap, m.Version, m.ID, min, max)
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
	latest := map[int]LedgerEntry{}
	for _, e := range rows {
		latest[e.Version] = e
	}
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
