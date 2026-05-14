package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDMediaArtifactMeta identifies migration 0328 — the schema
// extension for the multimodal-io-01KQ8TDF mission (FR-017). Adds
// three metadata columns to media_artifacts that store image pixel
// dimensions and PDF page count extracted at upload time by Put:
//
//   - image_width  INTEGER NOT NULL DEFAULT 0
//   - image_height INTEGER NOT NULL DEFAULT 0
//   - page_count   INTEGER NOT NULL DEFAULT 0
//
// Existing rows default to 0 (treated as "unknown" by the gate and
// frontend; the gate skips pixel-cap checks when dims are absent).
//
// Numbering: 0328, the next free version after 0327
// (source_model_output — see migrations_source_model_output.go).
const migrationIDMediaArtifactMeta = "sessions/0328-media-artifact-meta"

const sqlMediaArtifactMetaSchema = `
        ALTER TABLE media_artifacts ADD COLUMN image_width  INTEGER NOT NULL DEFAULT 0;
        ALTER TABLE media_artifacts ADD COLUMN image_height INTEGER NOT NULL DEFAULT 0;
        ALTER TABLE media_artifacts ADD COLUMN page_count   INTEGER NOT NULL DEFAULT 0;
    `

// migration0328 returns the media_artifacts metadata migration.
// Down is a no-op: SQLite < 3.35 lacks DROP COLUMN; zeroing the values
// would be data loss and existing code handles 0 as "unknown".
func migration0328() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDMediaArtifactMeta,
		Version:       328,
		OwningMission: OwningMission,
		UpSource:      sqlMediaArtifactMetaSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlMediaArtifactMetaSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(_ context.Context, _ migrations.WriteTx) error {
			// SQLite does not support DROP COLUMN on older versions;
			// leaving the columns in place is safe — existing code reads
			// them as 0 (unknown) which is the correct fall-back.
			return nil
		},
	}
}
