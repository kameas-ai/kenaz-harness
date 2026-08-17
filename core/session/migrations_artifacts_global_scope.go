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

// artifactVersionsBackup0332 is the scratch table the rebuild parks
// artifact_versions in. Named after the migration so a crashed run leaves
// an obviously-attributable artefact — though it cannot: the whole Up runs
// in one WriteTx, so a failure rolls the scratch table back with everything
// else.
const artifactVersionsBackup0332 = "artifact_versions_0332_backup"

// artifactVersionsColumns0332 is artifact_versions as migration 0324
// created it. Listed explicitly rather than relying on `SELECT *` column
// order so the round-trip is pinned to a schema, not to a runtime accident.
const artifactVersionsColumns0332 = "id, artifact_id, version, content_hash, byte_size, mime_type, summary, path, created_at"

// tableExists reports whether a table of that name is present. Used by 0332
// to stay correct on a database where migration 0324 has not run — the
// canonical registry always orders 0324 before 0332, but a migration that
// silently assumes its predecessors is a migration that breaks the first
// time someone builds a partial registry.
func tableExists(ctx context.Context, tx migrations.WriteTx, name string) (bool, error) {
	var n int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func migration0332() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDArtifactsGlobalScope,
		Version:       332,
		OwningMission: OwningMission,
		UpSource:      sqlArtifactsGlobalScopeUp,
		// THE DROP TABLE BELOW CASCADES. artifact_versions (migration 0324)
		// declares `artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON
		// DELETE CASCADE`, and SQLite runs an implicit `DELETE FROM artifacts`
		// as part of `DROP TABLE artifacts` whenever foreign_keys is ON — which
		// it always is here, the DSN in core/storage/sqlite/sqlite.go sets
		// `_pragma=foreign_keys(1)`. That implicit delete fires the CASCADE and
		// empties artifact_versions. Measured on a real database: 2 version rows
		// in, 0 out, integrity_check clean, foreign_key_check clean. Silent.
		//
		// It never bit anyone before now for one reason only: this migration has
		// never actually run on an upgraded install. The v0.63.0 max(applied)
		// selection bug (see core/storage/migrations/registry.go) made every
		// sessions migration numbered 332+ invisible to the runner, and on a
		// fresh install artifacts and artifact_versions are both empty when 0332
		// executes. Repairing the selection is what points this migration at
		// populated tables for the first time, so the two must land together.
		//
		// PRAGMA foreign_keys is a no-op inside a transaction, so the enforcement
		// cannot be turned off for the rebuild. The child rows are parked in a
		// scratch table instead and restored after the RENAME, at which point the
		// FK resolves against the new artifacts table. Every id in
		// artifact_versions.artifact_id survives the rebuild — the INSERT above
		// copies artifacts verbatim — so the restore cannot violate the FK.
		//
		// UpSource is deliberately left as the original DDL: it is the migration's
		// content hash, already recorded in the ledger of every install where
		// 0332 did run, and changing it would trip ErrLedgerHashMismatch on all
		// of them. The statements below are that DDL with the save/restore pair
		// interleaved; the schema they produce is identical.
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			stmts := []string{
				`CREATE TABLE IF NOT EXISTS artifacts_new (
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
                )`,
				`INSERT INTO artifacts_new
                    (id, session_id, project_id, title, mime_type, content_hash,
                     byte_size, source, source_ref_json, scope_kind, created_at)
                 SELECT id, session_id, project_id, title, mime_type, content_hash,
                        byte_size, source, source_ref_json, scope_kind, created_at
                 FROM artifacts`,
			}
			// Park the CASCADE-exposed child rows, if 0324 has run.
			hasVersions, err := tableExists(ctx, tx, "artifact_versions")
			if err != nil {
				return err
			}
			if hasVersions {
				stmts = append(stmts,
					`DROP TABLE IF EXISTS `+artifactVersionsBackup0332,
					`CREATE TABLE `+artifactVersionsBackup0332+` AS
                     SELECT `+artifactVersionsColumns0332+` FROM artifact_versions`,
				)
			}
			stmts = append(stmts,
				`DROP INDEX IF EXISTS idx_artifacts_session`,
				`DROP INDEX IF EXISTS idx_artifacts_project`,
				`DROP INDEX IF EXISTS idx_artifacts_content_hash`,
				`DROP TABLE artifacts`,
				`ALTER TABLE artifacts_new RENAME TO artifacts`,
			)
			if hasVersions {
				stmts = append(stmts,
					`INSERT INTO artifact_versions (`+artifactVersionsColumns0332+`)
                     SELECT `+artifactVersionsColumns0332+` FROM `+artifactVersionsBackup0332,
					`DROP TABLE `+artifactVersionsBackup0332,
				)
			}
			stmts = append(stmts,
				`CREATE INDEX IF NOT EXISTS idx_artifacts_session
                    ON artifacts (session_id, created_at DESC) WHERE session_id IS NOT NULL`,
				`CREATE INDEX IF NOT EXISTS idx_artifacts_project
                    ON artifacts (project_id, created_at DESC) WHERE project_id IS NOT NULL`,
				`CREATE INDEX IF NOT EXISTS idx_artifacts_content_hash
                    ON artifacts (content_hash)`,
			)
			for _, stmt := range stmts {
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
