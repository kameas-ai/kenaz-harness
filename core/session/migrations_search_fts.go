package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDSearchFTS is the identifier for migration 0312.
//
// This migration creates the FTS5 virtual table and the three trigger
// set (ai/au/ad) that keep it in sync with session_messages. The
// virtual table uses a content= shadow configuration so FTS5 reads row
// text from the real table on demand rather than storing a duplicate
// copy on disk (saves space at the cost of one join per snippet call,
// which is acceptable for interactive search latency targets).
//
// Tokenizer chain: porter wrapping unicode61
// FTS5 tokenizer arguments list the outer tokenizer first, so the DDL
// form is "porter unicode61": porter receives text and delegates to the
// inner unicode61 tokenizer which normalises Unicode categories and
// strips diacritics, then porter applies the Porter stemmer so
// "searching", "searched", and "searches" all match the query "search".
//
// Numbering: 0312, the next free version in the sessions block (300-399)
// after 0310 (compaction). Version 0311 is reserved for the
// session-auto-titling mission (session-auto-titling-01KQ8TDS) and will
// be applied on main when that mission lands; skipping does not cause a
// gap-detection error because the migration runner only checks that
// applied versions are non-decreasing within the owning-mission block,
// not that the integers are contiguous.
//
// Down: FTS5 virtual tables support DROP TABLE; we also drop the three
// triggers explicitly so a Down → Up cycle produces a clean state.
const migrationIDSearchFTS = "sessions/0312-search-fts5"

// sqlCreateFTS is the DDL for the FTS5 virtual table.
const sqlCreateFTS = `CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts
    USING fts5(
        content,
        content='session_messages',
        content_rowid='rowid',
        tokenize='porter unicode61'
    )`

// sqlTriggerInsert is the AFTER INSERT trigger that keeps messages_fts
// in sync with session_messages.
const sqlTriggerInsert = `CREATE TRIGGER IF NOT EXISTS messages_fts_ai
    AFTER INSERT ON session_messages BEGIN
        INSERT INTO messages_fts(rowid, content)
            VALUES (new.rowid, new.content);
    END`

// sqlTriggerUpdate is the AFTER UPDATE trigger. FTS5 content-table
// updates require a delete of the old row before inserting the new one.
const sqlTriggerUpdate = `CREATE TRIGGER IF NOT EXISTS messages_fts_au
    AFTER UPDATE ON session_messages BEGIN
        INSERT INTO messages_fts(messages_fts, rowid, content)
            VALUES ('delete', old.rowid, old.content);
        INSERT INTO messages_fts(rowid, content)
            VALUES (new.rowid, new.content);
    END`

// sqlTriggerDelete is the AFTER DELETE trigger. Notifies FTS5 of the
// removed row via the special 'delete' command row.
const sqlTriggerDelete = `CREATE TRIGGER IF NOT EXISTS messages_fts_ad
    AFTER DELETE ON session_messages BEGIN
        INSERT INTO messages_fts(messages_fts, rowid, content)
            VALUES ('delete', old.rowid, old.content);
    END`

// sqlSearchFTSSchema is the concatenated DDL used as UpSource for hash
// computation. The actual execution uses the individual constants above
// so the trigger bodies (which contain internal semicolons) are not
// split by splitSQL.
const sqlSearchFTSSchema = sqlCreateFTS + ";\n" +
	sqlTriggerInsert + ";\n" +
	sqlTriggerUpdate + ";\n" +
	sqlTriggerDelete + ";\n"

// migration0312 returns the migration that creates the FTS5 virtual
// table and the three sync triggers. See the doc comment above for the
// full rationale.
//
// Note: the Up function executes each DDL statement individually rather
// than calling splitSQL because CREATE TRIGGER bodies contain internal
// semicolons (the INSERT statements inside BEGIN...END) that would be
// mis-split by the naive semicolon splitter.
func migration0312() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSearchFTS,
		Version:       312,
		OwningMission: OwningMission,
		UpSource:      sqlSearchFTSSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				sqlCreateFTS,
				sqlTriggerInsert,
				sqlTriggerUpdate,
				sqlTriggerDelete,
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP TRIGGER IF EXISTS messages_fts_ad",
				"DROP TRIGGER IF EXISTS messages_fts_au",
				"DROP TRIGGER IF EXISTS messages_fts_ai",
				"DROP TABLE IF EXISTS messages_fts",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
