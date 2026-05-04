package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDWorkflows is the identifier for migration 0319 — the
// schema landing for user-defined workflows (workflows-01KQ8TDG WP06).
//
// Two tables:
//
//   - workflows: the current canonical record for a workflow. id is a
//     stable application-supplied identifier (not auto-incrementing),
//     yaml_source is the canonical YAML payload, hash is sha256(yaml_source)
//     used by the storage layer to detect no-op saves, and version
//     monotonically increments on every payload-changing Save.
//
//   - workflow_versions: append-only history of every distinct
//     yaml_source seen for a workflow id, indexed by (workflow_id,
//     version) for replay/diff tooling.
//
// Numbering: 0319, the next free version after 0318 (sessions.kind from
// harness-self-mcp-onboarding-01KQ8TDU WP03 — see migrations_kind.go).
//
// Down: drops both tables. The storage layer is the only writer so a
// down/up cycle simply reseeds from whatever the operator imports next.
const migrationIDWorkflows = "sessions/0319-workflows"

// sqlWorkflowsSchema is the canonical DDL body for migration 0319.
//
// The (workflow_id, version) UNIQUE constraint on workflow_versions
// keeps the append-only history idempotent: a repeated Save with the
// same payload hash short-circuits in the storage layer before it
// reaches the INSERT, but the constraint is a defensive belt regardless.
const sqlWorkflowsSchema = `
        CREATE TABLE IF NOT EXISTS workflows (
            id           TEXT PRIMARY KEY,
            name         TEXT NOT NULL DEFAULT '',
            description  TEXT NOT NULL DEFAULT '',
            yaml_source  TEXT NOT NULL,
            version      INTEGER NOT NULL DEFAULT 1,
            hash         TEXT NOT NULL,
            created_at   INTEGER NOT NULL,
            updated_at   INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_workflows_updated_at
            ON workflows (updated_at DESC);

        CREATE TABLE IF NOT EXISTS workflow_versions (
            id           INTEGER PRIMARY KEY AUTOINCREMENT,
            workflow_id  TEXT NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
            version      INTEGER NOT NULL,
            yaml_source  TEXT NOT NULL,
            hash         TEXT NOT NULL,
            created_at   INTEGER NOT NULL,
            UNIQUE (workflow_id, version)
        );

        CREATE INDEX IF NOT EXISTS idx_workflow_versions_workflow
            ON workflow_versions (workflow_id, version DESC);
    `

// migration0319 returns the migration that lands the workflows +
// workflow_versions tables.
func migration0319() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDWorkflows,
		Version:       319,
		OwningMission: OwningMission,
		UpSource:      sqlWorkflowsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlWorkflowsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_workflow_versions_workflow",
				"DROP TABLE IF EXISTS workflow_versions",
				"DROP INDEX IF EXISTS idx_workflows_updated_at",
				"DROP TABLE IF EXISTS workflows",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
