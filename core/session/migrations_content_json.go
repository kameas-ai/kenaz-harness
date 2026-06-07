package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDContentJSON is the identifier for migration 0302.
//
// Lands the post-WP01 multimodal-io WP02 storage shape:
//
//   - session_messages.content_json — JSON-encoded []ContentBlock
//     written alongside the legacy `content` column. Readers prefer
//     content_json when non-null; legacy rows continue to round-trip
//     via the synthesized [{type:"text", text:content}] fallback. The
//     legacy `content` column is retained for one release as a compat
//     buffer (a follow-up mission drops it once every consumer is
//     migrated).
//
//   - media_artifacts — CAS metadata table for binary uploads. The
//     on-disk file lives at <DataDir>/media/<content_hash>; this row
//     carries the artifact id, mime type, byte size, and original
//     filename. Indexed on content_hash for the dedup + refcount
//     lookups MediaStore.RefcountFor performs.
//
//   - context_attachments.media_id — optional FK pointing at a
//     media_artifacts row. ON DELETE SET NULL so deleting a media row
//     never strands an attachment record.
const migrationIDContentJSON = "sessions/0302-content-json"

// sqlContentJSONSchema is the canonical DDL for migration 0302. Three
// independent shape changes ride together:
//
//  1. ALTER session_messages ADD COLUMN content_json TEXT NULL — the
//     persisted-message-shape change paired with WP01's
//     []ContentBlock surface.
//  2. CREATE TABLE media_artifacts (...) + index — the CAS metadata
//     table consumed by core/attachments/media.go.
//  3. ALTER context_attachments ADD COLUMN media_id TEXT NULL
//     REFERENCES media_artifacts(id) ON DELETE SET NULL — the
//     optional binary-back link added by WP02.
//
// SQLite < 3.35 lacks DROP COLUMN; the Down path is intentionally a
// no-op (documented below).
const sqlContentJSONSchema = `
        ALTER TABLE session_messages ADD COLUMN content_json TEXT NULL;

        CREATE TABLE IF NOT EXISTS media_artifacts (
            id            TEXT PRIMARY KEY,
            content_hash  TEXT NOT NULL,
            media_type    TEXT NOT NULL,
            byte_size     INTEGER NOT NULL,
            original_name TEXT NOT NULL DEFAULT '',
            created_at    INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_media_artifacts_content_hash
            ON media_artifacts (content_hash);

        ALTER TABLE context_attachments ADD COLUMN media_id TEXT NULL
            REFERENCES media_artifacts(id) ON DELETE SET NULL;
    `

// migration0302 returns the migration that lands content_json +
// media_artifacts + context_attachments.media_id. See the doc comment
// above for the rationale and the WP boundary notes.
func migration0302() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDContentJSON,
		Version:       302,
		OwningMission: OwningMission,
		UpSource:      sqlContentJSONSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlContentJSONSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// SQLite < 3.35 cannot DROP COLUMN; we run on the embedded
			// modernc.org/sqlite floor that supports it (3.51+) but a
			// rollback to a v1-shape DB would also have to handle the
			// media_artifacts table. Document Down as a best-effort
			// no-op rather than a half-baked DROP that diverges across
			// the matrix of supported SQLite builds. Operators wanting
			// a clean rollback restore from a backup taken pre-0302.
			return nil
		},
	}
}
