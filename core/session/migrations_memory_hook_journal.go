package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDMemoryHookJournal lands the memory hook journal table
// (FR-027 + FR-058 — agent-kernel-graph mission). Greedy memory hooks
// fire on every kernel boundary; this table records every fire
// (written / dedup-skip / policy-blocked) so the inspector RPC can
// audit hook activity.
//
// Numbering: 0306 reserved for branches (Bundle B), 0307 for corpora
// (Bundle C), 0308 lands here for the memory-hook journal — matches
// spec.md §4.11 / FR-058.
const migrationIDMemoryHookJournal = "sessions/0308-memory-hook-journal"

const sqlMemoryHookJournalSchema = `
        CREATE TABLE IF NOT EXISTS memory_hook_journal (
            id            TEXT PRIMARY KEY,
            run_id        TEXT,
            session_id    TEXT,
            node_id       TEXT,
            boundary      TEXT NOT NULL,
            scope         TEXT NOT NULL,
            scope_id      TEXT,
            chunk_id      TEXT,
            written       INTEGER NOT NULL DEFAULT 0,
            deduped       INTEGER NOT NULL DEFAULT 0,
            skipped       INTEGER NOT NULL DEFAULT 0,
            skip_reason   TEXT NOT NULL DEFAULT '',
            content_hash  TEXT NOT NULL DEFAULT '',
            ts_ns         INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_memory_hook_journal_session
            ON memory_hook_journal (session_id, ts_ns);

        CREATE INDEX IF NOT EXISTS idx_memory_hook_journal_run
            ON memory_hook_journal (run_id, ts_ns);

        CREATE INDEX IF NOT EXISTS idx_memory_hook_journal_boundary
            ON memory_hook_journal (boundary, ts_ns);
    `

// migration0308 returns the memory hook journal migration.
func migration0308() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDMemoryHookJournal,
		Version:       308,
		OwningMission: OwningMission,
		UpSource:      sqlMemoryHookJournalSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlMemoryHookJournalSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_memory_hook_journal_boundary",
				"DROP INDEX IF EXISTS idx_memory_hook_journal_run",
				"DROP INDEX IF EXISTS idx_memory_hook_journal_session",
				"DROP TABLE IF EXISTS memory_hook_journal",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
