package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDCompaction lands the per-message bookkeeping columns the
// compaction-strategy-ui mission (compaction-strategy-ui-01KQ8TDI WP01)
// needs to drive summarize-then-replace compaction:
//
//   - session_messages.compacted_into_id — TEXT NULL FK pointer to the
//     synthetic summary row that replaces this message in the live
//     transcript. NULL on rows that have never been compacted.
//
//   - session_messages.compacted_at — INTEGER NULL unix-nanos timestamp
//     captured at the moment the engine wrote the summary row. NULL on
//     rows the engine never touched.
//
//   - session_messages.archived_at — INTEGER NULL unix-nanos timestamp
//     captured when the originals_deleted retention sweep tombstoned the
//     row. NULL on live rows; non-NULL rows are excluded from default
//     reads (FR-? — see plan §5.4).
//
// Two indexes ride along:
//
//   - idx_session_messages_compacted_into — accelerates the
//     "list every original that fed this summary" reverse-lookup the
//     compaction UI uses to render expand/collapse summary panels.
//
//   - idx_session_messages_archived_at — partial index keyed on
//     archived_at IS NOT NULL so the retention sweep's cursor query
//     stays cheap even on very long sessions.
//
// Numbering: 0310, the next free version in the sessions block (300-399)
// after 0309 (agent_graph_events).
//
// Down: SQLite's embedded floor (modernc.org/sqlite 1.50.0 — SQLite 3.x)
// supports DROP COLUMN, but the existing column-add migrations in this
// package (0302, 0303) document Down as a best-effort no-op rather than
// gamble on operator-side SQLite builds. We follow the same stance:
// operators wanting a clean rollback restore from a backup taken pre-0310.
const migrationIDCompaction = "sessions/0310-compaction"

// sqlCompactionSchema is the canonical DDL for migration 0310. Three
// nullable columns + two indexes; every existing row stays untouched
// because the columns default to NULL.
const sqlCompactionSchema = `
        ALTER TABLE session_messages ADD COLUMN compacted_into_id TEXT NULL;

        ALTER TABLE session_messages ADD COLUMN compacted_at INTEGER NULL;

        ALTER TABLE session_messages ADD COLUMN archived_at INTEGER NULL;

        CREATE INDEX IF NOT EXISTS idx_session_messages_compacted_into
            ON session_messages (compacted_into_id);

        CREATE INDEX IF NOT EXISTS idx_session_messages_archived_at
            ON session_messages (archived_at) WHERE archived_at IS NOT NULL;
    `

// migration0310 returns the migration that lands the compaction
// bookkeeping columns + indexes. See the doc comment above for the
// rationale and the down-migration stance.
func migration0310() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDCompaction,
		Version:       310,
		OwningMission: OwningMission,
		UpSource:      sqlCompactionSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlCompactionSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// Drop the indexes (cheap, always safe). The columns are
			// left in place to match the package convention for
			// column-add migrations (0302, 0303): SQLite supports
			// DROP COLUMN on the embedded floor (3.51+) but a clean
			// downgrade also has to coordinate with the compaction
			// engine's transactional writes, so operators wanting a
			// rollback restore from a pre-0310 backup.
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_session_messages_archived_at",
				"DROP INDEX IF EXISTS idx_session_messages_compacted_into",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
