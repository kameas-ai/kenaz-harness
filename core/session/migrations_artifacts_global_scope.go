package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDArtifactsGlobalScope identifies migration 0332 — the
// additive schema extension that adds "global" to the artifacts
// scope_kind CHECK constraint. Required by the unified-context-artifacts
// store (core/units package) so that artifacts can be promoted to
// global scope (visible across all sessions and projects, FR-003).
//
// SQLite CHECK constraints cannot be modified via ALTER TABLE — the
// standard rename/create/copy/drop recipe is used (same pattern as
// migrations 0304 and 0327).
//
// Migration 0332:
//   - Recreates the artifacts table with an extended CHECK on scope_kind:
//     IN ('session','project','global').
//   - Copies every existing row verbatim (no scope_kind value changes).
//   - Recreates all three indexes.
//
// Down is a best-effort no-op — operators wanting to downgrade past this
// migration must restore from a pre-0332 backup (same policy as 0303/0327).
const migrationIDArtifactsGlobalScope = "sessions/0332-artifacts-global-scope"

const sqlArtifactsGlobalScopeUp = `
    CREATE TABLE IF NOT EXISTS artifacts_new (
        id              TEXT PRIMARY KEY,
        session_id      TEXT NULL REFERENCES sessions(id) ON DELETE SET NULL,
        project_id      TEXT NULL REFERENCES projects(id) ON DELETE SET NULL,
        title           TEXT NOT NULL DEFAULT '',
        mime_type       TEXT NOT NULL,
        content_hash    TEXT NOT NULL,
        byte_size       INTEGER NOT NULL,
        source          TEXT NOT NULL CHECK (source IN ('code_block','tool_output','user_pin','model_output')),
        source_ref_json TEXT NOT NULL DEFAULT '{}',
        scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project','global')),
        created_at      INTEGER NOT NULL
    );

    INSERT INTO artifacts_new
        (id, session_id, project_id, title, mime_type, content_hash,
         byte_size, source, source_ref_json, scope_kind, created_at)
    SELECT id, session_id, project_id, title, mime_type, content_hash,
           byte_size, source, source_ref_json, scope_kind, created_at
    FROM artifacts;

    DROP INDEX IF EXISTS idx_artifacts_session;
    DROP INDEX IF EXISTS idx_artifacts_project;
    DROP INDEX IF EXISTS idx_artifacts_content_hash;

    DROP TABLE artifacts;

    ALTER TABLE artifacts_new RENAME TO artifacts;

    CREATE INDEX IF NOT EXISTS idx_artifacts_session
        ON artifacts (session_id, created_at DESC) WHERE session_id IS NOT NULL;

    CREATE INDEX IF NOT EXISTS idx_artifacts_project
        ON artifacts (project_id, created_at DESC) WHERE project_id IS NOT NULL;

    CREATE INDEX IF NOT EXISTS idx_artifacts_content_hash
        ON artifacts (content_hash);
`

func migration0332() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDArtifactsGlobalScope,
		Version:       332,
		OwningMission: OwningMission,
		UpSource:      sqlArtifactsGlobalScopeUp,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlArtifactsGlobalScopeUp) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			return nil
		},
	}
}
