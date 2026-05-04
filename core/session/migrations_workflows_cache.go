package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDWorkflowsCache lands the workflow_runs_cache table that
// backs the rerun_policy resolver (workflows-01KQ8TDG WP08).
//
// The cache is keyed by (workflow_id, inputs_hash) so a re-invocation
// of the same workflow with byte-identical inputs can short-circuit
// against the most recent successful run rather than re-dispatching
// every step. The output_json column carries a serialised RunResult;
// the resolver decides what to do based on workflow.rerun_policy:
//
//   - "always" (alias: "fresh"): never consult cache; always re-run.
//   - "skip"   (alias: "continue"): if cached, return the cached result.
//   - "prompt" (alias: "ask"): if cached, surface a prompt request to
//     the caller so the UI can ask the user before reusing.
//
// The table is small (one row per (workflow, inputs) pair) and is
// idempotently upserted by the cache writer. completed_at lets a
// future janitor prune stale rows; WP08 does not yet schedule that.
//
// Numbering: 0320, the next free version after 0319 (workflows +
// workflow_versions — see migrations_workflows.go).
//
// Down: drops the table. The cache is purely advisory; rebuilding is
// just the next workflow run away.
const migrationIDWorkflowsCache = "sessions/0320-workflow-runs-cache"

// sqlWorkflowsCacheSchema is the canonical DDL body for migration 0320.
const sqlWorkflowsCacheSchema = `
        CREATE TABLE IF NOT EXISTS workflow_runs_cache (
            workflow_id   TEXT NOT NULL,
            inputs_hash   TEXT NOT NULL,
            output_json   TEXT NOT NULL,
            completed_at  INTEGER NOT NULL,
            PRIMARY KEY (workflow_id, inputs_hash)
        );

        CREATE INDEX IF NOT EXISTS idx_workflow_runs_cache_completed
            ON workflow_runs_cache (completed_at DESC);
    `

// migration0320 returns the migration that lands the
// workflow_runs_cache table.
func migration0320() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDWorkflowsCache,
		Version:       320,
		OwningMission: OwningMission,
		UpSource:      sqlWorkflowsCacheSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlWorkflowsCacheSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_workflow_runs_cache_completed",
				"DROP TABLE IF EXISTS workflow_runs_cache",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
