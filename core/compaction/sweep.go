package compaction

// sweep.go implements the soft-archive retention sweep (plan §2.7). The
// threshold-mode and rolling-mode engines (engine.go, rolling.go) only
// soft-archive originals — flipping `archived_at` and `compacted_into_id`
// — so the live transcript stays clean while the originals remain
// recoverable for audit / "Show full history" replay. After a configurable
// retention window those archived originals are tombstoned for good by
// this sweep.
//
// The sweep is a pure SQL-level deletion behind the SweepStore interface.
// Boundaries:
//
//   - Summary rows are NEVER deleted. The SQL filter requires
//     compacted_into_id IS NOT NULL on every deleted row, which is true
//     only for archived originals; summary rows have compacted_into_id
//     IS NULL and are excluded.
//
//   - Pagination keeps any single batched DELETE under ~200ms even on a
//     200k-row sweep. Default page size is 5000.
//
//   - retentionDays <= 0 disables the sweep (no SQL, no audit).
//
// The 100ms inter-batch cooldown mentioned in plan §3 risk register is
// NOT implemented here — the sweeper relies on SQLite's busy-handler /
// WAL for concurrency. See the TODO inside RunSweep.

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// defaultSweepPageLimit is the page size for the batched DELETE. 5000
// keeps a single batch's writer hold under ~200ms on the worst-case
// 200k-row sweep we've stress-tested against.
const defaultSweepPageLimit = 5000

// SweepStore is the minimal storage shape RunSweep needs. WP08 wires
// this to a concrete *sql.DB; here it's an interface so WP05 stays
// testable with an in-memory fake.
type SweepStore interface {
	// DeleteArchivedBefore deletes session_messages rows where
	//   archived_at IS NOT NULL
	//   AND archived_at < cutoff
	//   AND compacted_into_id IS NOT NULL
	// in a single batched DELETE that loops in pages of pageLimit until
	// the affected-row count is zero.
	//
	// Returns the total number of deleted rows, plus the oldest/newest
	// archived_at timestamps that were swept (zero values when nothing
	// was deleted).
	DeleteArchivedBefore(ctx context.Context, cutoff time.Time, pageLimit int) (
		deleted int, oldest, newest time.Time, err error)
}

// RunSweep deletes archived originals past the retention window and
// emits the KindCompactedOriginalsDeleted audit. Returns the total
// deleted rows.
//
// retentionDays must be > 0; values <= 0 return (0, nil) — disabled.
//
// The audit is emitted only when at least one row was deleted (a
// no-deletion sweep is silent — the scheduler tick itself is the
// observable cadence).
func RunSweep(ctx context.Context, store SweepStore, auditEm AuditEmitter,
	retentionDays int, now func() time.Time) (deletedCount int, err error) {

	if retentionDays <= 0 {
		// Sweep disabled. No SQL, no audit emit, no cursor cost.
		return 0, nil
	}

	if now == nil {
		now = time.Now
	}

	cutoff := now().Add(-time.Duration(retentionDays) * 24 * time.Hour)

	// TODO: cooldown — if testing reveals writer contention with the
	// chat runner, add a short (~100ms) sleep between paged DELETE
	// batches. Today the sweeper depends on SQLite's busy-handler / WAL
	// for concurrency.
	deleted, oldest, newest, err := store.DeleteArchivedBefore(ctx, cutoff, defaultSweepPageLimit)
	if err != nil {
		return 0, err
	}

	if deleted == 0 {
		// No rows past the cutoff — silent no-op.
		return 0, nil
	}

	if auditEm != nil {
		auditEm.Emit(ctx, audit.KindCompactedOriginalsDeleted, audit.CompactedOriginalsDeletedPayload{
			DeletedCount:     deleted,
			OldestArchivedAt: oldest,
			NewestArchivedAt: newest,
		})
	}

	return deleted, nil
}
