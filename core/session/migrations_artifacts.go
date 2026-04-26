package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDArtifacts is the identifier for migration 0303 — the
// schema landing for the artifacts-storage mission. Adds the
// `artifacts` table that holds metadata for every captured assistant
// output (code blocks, file-shaped tool outputs, user pins). The bytes
// themselves live in the shared CAS at <DataDir>/media/<content_hash>
// laid down by 0302; this table only tracks the metadata + provenance
// rows.
const migrationIDArtifacts = "sessions/0303-artifacts"

// sqlArtifactsSchema is the canonical DDL for migration 0303. The
// CHECK constraints pin the source and scope_kind enums at the SQL
// boundary so no rogue writer can sneak unknown values in.
//
// FK semantics:
//   - session_id → sessions(id) ON DELETE CASCADE: deleting a session
//     drops every artifact captured under it (matches the spec's
//     cascade prompt at FR-014).
//   - project_id → projects(id) ON DELETE SET NULL: deleting a project
//     severs the rollup link without losing the artifact row; the
//     scope_kind value is left untouched so the caller can decide
//     whether to demote (per FR-015).
//
// Indexes match the spec's NFR-006 query budget: the (session_id,
// created_at DESC) index covers the per-session list; the partial
// (project_id, created_at DESC) covers the project rollup; and
// (content_hash) supports the refcount lookup the MediaStore composite
// performs through ArtifactsRefcountSource.
const sqlArtifactsSchema = `
        CREATE TABLE IF NOT EXISTS artifacts (
            id              TEXT PRIMARY KEY,
            session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            project_id      TEXT NULL REFERENCES projects(id) ON DELETE SET NULL,
            title           TEXT NOT NULL DEFAULT '',
            mime_type       TEXT NOT NULL,
            content_hash    TEXT NOT NULL,
            byte_size       INTEGER NOT NULL,
            source          TEXT NOT NULL CHECK (source IN ('code_block','tool_output','user_pin')),
            source_ref_json TEXT NOT NULL DEFAULT '{}',
            scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project')),
            created_at      INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_artifacts_session
            ON artifacts (session_id, created_at DESC);

        CREATE INDEX IF NOT EXISTS idx_artifacts_project
            ON artifacts (project_id, created_at DESC) WHERE project_id IS NOT NULL;

        CREATE INDEX IF NOT EXISTS idx_artifacts_content_hash
            ON artifacts (content_hash);
    `

// migration0303 returns the migration that lands the artifacts table.
// Down is a best-effort no-op for parity with 0302's documented stance:
// SQLite < 3.35 lacks DROP COLUMN; the embedded floor (3.51+) supports
// it but a clean rollback would have to coordinate with the
// MediaStore's CAS files. Operators wanting a downgrade restore from a
// pre-0303 backup.
func migration0303() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDArtifacts,
		Version:       303,
		OwningMission: OwningMission,
		UpSource:      sqlArtifactsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlArtifactsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			return nil
		},
	}
}
