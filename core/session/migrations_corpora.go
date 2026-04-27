package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDCorpora identifies migration 0307 — the schema landing for
// the agent-kernel-graph mission's corpora subsystem (Bundle C, WP10).
// Three tables back the bulk-ingest knowledge primitive:
//
//   - corpora: one row per named corpus + scope (global / project / session).
//   - corpus_files: per-file ingest manifest with sha256 hashing so a
//     repeat ingest can hash-check and skip unchanged files.
//   - corpus_chunks: line-bounded text fragments + their embedding bytes
//     (gob-encoded vector); the per-corpus on-disk vector store at
//     <DataDir>/corpora/<id>/vectors.gob mirrors this for fast retrieval.
//
// FK semantics:
//   - corpus_files / corpus_chunks both reference corpora(id) ON DELETE
//     CASCADE so DeleteCorpus drops the entire set in one shot.
//   - corpus_chunks references corpus_files(id) ON DELETE CASCADE so a
//     mid-ingest file rollback (NFR-006: atomic per-file commit) can
//     erase that file's chunks without leaking partial rows.
//
// Indexes:
//   - idx_corpora_scope on (scope, scope_id) drives the per-project /
//     per-session list.
//   - idx_corpus_files_corpus on (corpus_id, path) drives the
//     ingest-time hash-check / list view.
//   - idx_corpus_chunks_corpus on (corpus_id, file_id, chunk_seq) drives
//     ordered chunk replay during retrieval.
const migrationIDCorpora = "sessions/0307-corpora"

const sqlCorporaSchema = `
        CREATE TABLE IF NOT EXISTS corpora (
            id          TEXT PRIMARY KEY,
            name        TEXT NOT NULL,
            scope       TEXT NOT NULL CHECK (scope IN ('global','project','session')),
            scope_id    TEXT NOT NULL DEFAULT '',
            tag         TEXT NOT NULL DEFAULT '',
            created_at  INTEGER NOT NULL,
            updated_at  INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_corpora_scope
            ON corpora (scope, scope_id);

        CREATE TABLE IF NOT EXISTS corpus_files (
            id            TEXT PRIMARY KEY,
            corpus_id     TEXT NOT NULL REFERENCES corpora(id) ON DELETE CASCADE,
            path          TEXT NOT NULL,
            sha256        TEXT NOT NULL,
            file_size     INTEGER NOT NULL,
            line_count    INTEGER NOT NULL,
            ingested_at   INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_corpus_files_corpus
            ON corpus_files (corpus_id, path);

        CREATE TABLE IF NOT EXISTS corpus_chunks (
            id          TEXT PRIMARY KEY,
            corpus_id   TEXT NOT NULL REFERENCES corpora(id) ON DELETE CASCADE,
            file_id     TEXT NOT NULL REFERENCES corpus_files(id) ON DELETE CASCADE,
            chunk_seq   INTEGER NOT NULL,
            text        TEXT NOT NULL,
            embedding   BLOB NOT NULL,
            line_start  INTEGER NOT NULL,
            line_end    INTEGER NOT NULL,
            created_at  INTEGER NOT NULL
        );

        CREATE INDEX IF NOT EXISTS idx_corpus_chunks_corpus
            ON corpus_chunks (corpus_id, file_id, chunk_seq);
    `

// migration0307 returns the migration that lands the corpora tables.
// Down drops the tables (and indexes ride-along via SQLite's implicit
// drop-on-table-drop behaviour). Operators downgrading lose the corpus
// metadata but the on-disk vector files at <DataDir>/corpora/ remain;
// the next ingest will recreate the rows.
func migration0307() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDCorpora,
		Version:       307,
		OwningMission: OwningMission,
		UpSource:      sqlCorporaSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlCorporaSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_corpus_chunks_corpus",
				"DROP TABLE IF EXISTS corpus_chunks",
				"DROP INDEX IF EXISTS idx_corpus_files_corpus",
				"DROP TABLE IF EXISTS corpus_files",
				"DROP INDEX IF EXISTS idx_corpora_scope",
				"DROP TABLE IF EXISTS corpora",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
