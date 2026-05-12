package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDScheduledChatRuns identifies migration 0325 — the schema
// landing for the scheduled-chat-runs feature
// (scheduled-chat-runs-01KX5R8B, WP01).
//
// Two tables are created in a single migration:
//
//   - scheduled_chat_runs: stores the user-authored prompt template,
//     cron expression, target model, output sink, and enabled flag for
//     each scheduled chat run.
//
//   - scheduled_chat_run_history: one row per execution, linked to its
//     parent run via FK with ON DELETE CASCADE.
//
// Numbering: 0325, the next free version after 0324
// (artifact_versions — see migrations_artifact_versions.go).
const migrationIDScheduledChatRuns = "sessions/0325-scheduled-chat-runs"

const sqlScheduledChatRunsSchema = `
        CREATE TABLE IF NOT EXISTS scheduled_chat_runs (
            id               TEXT PRIMARY KEY,
            name             TEXT NOT NULL DEFAULT '',
            prompt_template  TEXT NOT NULL DEFAULT '',
            cron             TEXT NOT NULL,
            timezone         TEXT NOT NULL DEFAULT '',
            model            TEXT NOT NULL DEFAULT '',
            output_sink      TEXT NOT NULL DEFAULT 'banner',
            enabled          INTEGER NOT NULL DEFAULT 1,
            created_at       INTEGER NOT NULL,
            updated_at       INTEGER NOT NULL
        );

        CREATE TABLE IF NOT EXISTS scheduled_chat_run_history (
            id              TEXT PRIMARY KEY,
            chat_run_id     TEXT NOT NULL
                REFERENCES scheduled_chat_runs(id) ON DELETE CASCADE,
            session_id      TEXT NOT NULL DEFAULT '',
            status          TEXT NOT NULL DEFAULT 'completed',
            started_at      INTEGER NOT NULL,
            ended_at        INTEGER,
            output_snippet  TEXT NOT NULL DEFAULT '',
            error           TEXT NOT NULL DEFAULT ''
        );

        CREATE INDEX IF NOT EXISTS idx_schr_chat_run_started
            ON scheduled_chat_run_history (chat_run_id, started_at DESC);
    `

// migration0325 returns the migration that lands the scheduled_chat_runs
// and scheduled_chat_run_history tables.
func migration0325() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDScheduledChatRuns,
		Version:       325,
		OwningMission: OwningMission,
		UpSource:      sqlScheduledChatRunsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlScheduledChatRunsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_schr_chat_run_started",
				"DROP TABLE IF EXISTS scheduled_chat_run_history",
				"DROP TABLE IF EXISTS scheduled_chat_runs",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
