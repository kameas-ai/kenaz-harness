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
//
// THE CASCADE FIX (upgrade-path-coverage-01PMUG01 WP03).
//
// `DROP TABLE artifacts` below cascades: artifact_versions (migration
// 0324, which registers BELOW 0327 in the same block) declares
// `artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE
// CASCADE`, and the production DSN
// (core/storage/sqlite/sqlite.go) always sets
// `_pragma=foreign_keys(1)`. SQLite's DROP TABLE performs an implicit
// DELETE against every row of the table being dropped, and that DELETE
// fires the CASCADE — silently: foreign_key_check and integrity_check
// both stay clean while artifact_versions empties (spec.md §1.3).
//
// Unlike migration 0332 (which never ran on a populated install before
// the v0.63.1 Pending() selection fix, so this hazard was theoretical
// for it until this mission), 0327 shipped BEFORE the units block
// existed and the max-based selection bug had not yet started skipping
// anything — it ran, cascade and all, on every install that had
// artifact_versions rows at the moment it upgraded through 327. That
// historical damage is real and unrecoverable (spec §8) and is
// explicitly out of scope here. What this fix closes is the FORWARD
// risk: any database whose ledger still stops at <=326 has 0327
// pending today, and it will run again.
//
// The fix parks artifact_versions in a scratch table before the
// rebuild and restores it after — the exact recipe migration 0332
// uses (migrations_artifacts_global_scope.go), reusing its tableExists
// probe and its artifactVersionsColumns0332 column list (the shape has
// not changed between 0324 and 0332, so the same explicit column list
// applies here too).
//
// UpSource IS DELIBERATELY LEFT UNCHANGED. It is the migration's
// content hash (registry.go Register / HashSQL), already recorded in
// the ledger of every install where 0327 has run, and editing it would
// trip ErrLedgerHashMismatch on all of them (spec §4) — every existing
// user's ledger would mismatch and the app would refuse to boot. The
// statements actually executed below are that same DDL with the
// save/restore pair interleaved; the resulting schema is identical.
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

// artifactVersionsBackup0327 is the scratch table 0327's fix parks
// artifact_versions in during the rebuild. Named after this migration
// so a leaked scratch table (there should never be one — the whole Up
// runs in one WriteTx, so a failure rolls it back with everything else)
// is attributable at a glance.
const artifactVersionsBackup0327 = "artifact_versions_0327_backup"

func migration0327() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSourceModelOutput,
		Version:       327,
		OwningMission: OwningMission,
		// UpSource stays the original DDL — see the doc comment above.
		UpSource: sqlSourceModelOutputUp,
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
                    scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project')),
                    created_at      INTEGER NOT NULL
                )`,
				`INSERT INTO artifacts_new
                    (id, session_id, project_id, title, mime_type, content_hash,
                     byte_size, source, source_ref_json, scope_kind, created_at)
                 SELECT id, session_id, project_id, title, mime_type, content_hash,
                        byte_size, source, source_ref_json, scope_kind, created_at
                 FROM artifacts`,
			}
			// Park the CASCADE-exposed child rows, if 0324 has run (the
			// canonical registry always orders 0324 before 0327, but a
			// migration that silently assumes its predecessors is one
			// that breaks the first time someone builds a partial
			// registry — same defensive shape as 0332).
			hasVersions, err := tableExists(ctx, tx, "artifact_versions")
			if err != nil {
				return err
			}
			if hasVersions {
				stmts = append(stmts,
					`DROP TABLE IF EXISTS `+artifactVersionsBackup0327,
					`CREATE TABLE `+artifactVersionsBackup0327+` AS
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
                     SELECT `+artifactVersionsColumns0332+` FROM `+artifactVersionsBackup0327,
					`DROP TABLE `+artifactVersionsBackup0327,
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
		// Down is a best-effort no-op: rolling back a CHECK constraint
		// extension in SQLite requires restoring from a pre-0327 backup.
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			return nil
		},
	}
}
