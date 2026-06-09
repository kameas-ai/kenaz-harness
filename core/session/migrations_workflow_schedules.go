package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDWorkflowSchedules is the identifier for migration 0321 —
// the schema landing for the cron-scheduler persistence table
// (workflows-agentic-01KW2D3X WP02).
//
// workflow_schedules stores one row per workflow that has a declared
// cron schedule. The schedule survives chassis restarts via this table;
// at boot the scheduler reloads all enabled rows and re-registers them
// with the cron engine.
//
// Columns:
//   - workflow_id (PK, FK → workflows.id ON DELETE CASCADE): ties the
//     schedule to its workflow record so a Delete cascades automatically.
//   - cron: standard 5-field cron expression as declared in the YAML.
//   - timezone: IANA timezone name (e.g. "America/New_York"). Empty /
//     NULL is treated as UTC by the scheduler.
//   - enabled: 0 or 1; the scheduler skips disabled rows at boot.
//   - last_fired_at: Unix timestamp of the last dispatch tick. NULL
//     before the first fire.
//   - last_run_id: the run ID produced by the last dispatch. NULL before
//     the first fire.
//
// Numbering: 0321, the next free version after 0320
// (workflow_runs_cache — see migrations_workflows_cache.go).
//
// Down: drops the table. Schedules are re-populated from workflow YAML
// on the next chassis boot that re-registers them.
const migrationIDWorkflowSchedules = "sessions/0321-workflow-schedules"

// sqlWorkflowSchedulesSchema is the canonical DDL for migration 0321.
const sqlWorkflowSchedulesSchema = `
        CREATE TABLE IF NOT EXISTS workflow_schedules (
            workflow_id   TEXT PRIMARY KEY
                REFERENCES workflows(id) ON DELETE CASCADE,
            cron          TEXT NOT NULL,
            timezone      TEXT NOT NULL DEFAULT '',
            enabled       INTEGER NOT NULL DEFAULT 1,
            last_fired_at INTEGER,
            last_run_id   TEXT
        );
    `

// migration0321 returns the migration that lands workflow_schedules.
func migration0321() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDWorkflowSchedules,
		Version:       321,
		OwningMission: OwningMission,
		UpSource:      sqlWorkflowSchedulesSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlWorkflowSchedulesSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP TABLE IF EXISTS workflow_schedules",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
