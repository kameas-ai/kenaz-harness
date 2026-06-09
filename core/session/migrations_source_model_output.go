package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDSourceModelOutput identifies migration 0327 — the schema
// extension that adds "model_output" to the artifacts.source CHECK
// constraint. Required by the multimodal-io-extended-01KQ8TD2 WP02
// auto-capture pipeline: model-generated images land as artifacts with
// Source=="model_output" so the Artifacts library can differentiate
// user pins / code-block captures / tool outputs from images the model
// produced via a generation API (DALL-E 3, gpt-image-1, Titan Image).
//
// SQLite CHECK constraints cannot be added via ALTER TABLE — the
// standard rename/create/copy/drop recipe is required (same pattern as
// migration 0304 / migrations_artifacts_promote.go).
//
// Migration 0327:
//   - Recreates the artifacts table with an extended CHECK on source:
//     IN ('code_block','tool_output','user_pin','model_output').
//   - Copies every existing row verbatim (no source value changes).
//   - Recreates the three indexes from migration 0304.
//
// Down is a best-effort no-op — operators wanting to downgrade past
// this migration must restore from a pre-0327 backup (same policy as
// 0303/0304).
const migrationIDSourceModelOutput = "sessions/0327-source-model-output"

const sqlSourceModelOutputUp = `
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
            scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project')),
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

func migration0327() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSourceModelOutput,
		Version:       327,
		OwningMission: OwningMission,
		UpSource:      sqlSourceModelOutputUp,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlSourceModelOutputUp) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		// Down is a best-effort no-op: rolling back a CHECK constraint
		// extension in SQLite requires restoring from a pre-0327 backup.
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			return nil
		},
	}
}
