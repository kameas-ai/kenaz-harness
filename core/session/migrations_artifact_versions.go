package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDArtifactVersions identifies migration 0324 — the schema
// landing for the update-artifact-tool mission
// (update-artifact-tool-01KQ8TD4). Adds the `artifact_versions`
// append-only history table that records every content revision written
// by `kaneaz__update_artifact`.
//
// Design rationale:
//   - The parent `artifacts` row is NEVER mutated by the update tool.
//     All content history lives in `artifact_versions`; the parent row
//     stays as the stable identity (used by cross-session refs, the UI
//     Artifacts tab, and foreign-key chains).
//   - `artifact_versions` references `artifacts(id)` with ON DELETE
//     CASCADE so deleting the parent artifact sweeps its history.
//   - `content_hash` mirrors the MediaStore CAS key; the version bytes
//     ride in the same CAS directory as the original capture — no
//     second storage location.
//   - `summary` is a nullable free-text description of what changed.
//     The model can supply a one-line edit summary; the UI may render
//     it in a version history popover.
//   - `path` is the optional updated canonical path for file-source
//     artifacts (AbsolutePath in ArtifactSourceRef). NULL means "same
//     path as the parent row's source_ref_json.absolute_path".
//   - `version` is a monotonically increasing integer scoped per
//     artifact; the first row written for an artifact gets version 1.
//     The (artifact_id, version) UNIQUE constraint makes the history
//     idempotent under retries.
const migrationIDArtifactVersions = "sessions/0324-artifact-versions"

const sqlArtifactVersionsSchema = `
        CREATE TABLE IF NOT EXISTS artifact_versions (
            id           INTEGER PRIMARY KEY AUTOINCREMENT,
            artifact_id  TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
            version      INTEGER NOT NULL,
            content_hash TEXT NOT NULL,
            byte_size    INTEGER NOT NULL,
            mime_type    TEXT NOT NULL DEFAULT '',
            summary      TEXT,
            path         TEXT,
            created_at   INTEGER NOT NULL,
            UNIQUE (artifact_id, version)
        );

        CREATE INDEX IF NOT EXISTS idx_artifact_versions_artifact
            ON artifact_versions (artifact_id, version DESC);
    `

// migration0324 returns the migration that lands the artifact_versions
// table.
func migration0324() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDArtifactVersions,
		Version:       324,
		OwningMission: OwningMission,
		UpSource:      sqlArtifactVersionsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlArtifactVersionsSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_artifact_versions_artifact",
				"DROP TABLE IF EXISTS artifact_versions",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
