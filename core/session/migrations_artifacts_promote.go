package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDArtifactsPromote identifies migration 0304 — the
// artifacts-storage WP02 schema relaxation that lets a session-delete
// promote-to-project flow leave the artifact rows alive.
//
// Background: WP01's 0303 migration set artifacts.session_id to
// NOT NULL with ON DELETE CASCADE because every captured artifact has
// a source session (FR-001). WP02's session-delete cascade extension
// (FR-014) adds a "promote orphan artifacts to project scope before
// deleting the session" branch — but with the cascade FK in place,
// the artifact rows go away the moment the session row is deleted,
// making the promote a no-op.
//
// This migration:
//   - Lifts the NOT NULL constraint on session_id.
//   - Switches the FK to ON DELETE SET NULL so a session-delete
//     leaves the artifact row alive with a NULL session_id; the
//     promote-then-delete path nulls session_id at session-delete
//     time and the artifact survives under its project_id.
//   - Preserves every other column, index, and CHECK constraint from
//     0303 verbatim.
//
// SQLite doesn't support ALTER COLUMN to drop a NOT NULL or change
// an FK clause — the standard recipe is rename-old / create-new /
// copy-rows / drop-old.
const migrationIDArtifactsPromote = "sessions/0304-artifacts-promote"

const sqlArtifactsPromoteUp = `
        CREATE TABLE IF NOT EXISTS artifacts_new (
            id              TEXT PRIMARY KEY,
            session_id      TEXT NULL REFERENCES sessions(id) ON DELETE SET NULL,
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

func migration0304() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDArtifactsPromote,
		Version:       304,
		OwningMission: OwningMission,
		UpSource:      sqlArtifactsPromoteUp,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlArtifactsPromoteUp) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		// Down is a best-effort no-op (we don't restore the NOT NULL
		// constraint; rolling back schema this granular requires
		// restoring from backup).
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			return nil
		},
	}
}
