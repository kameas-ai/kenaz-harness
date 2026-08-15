package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDSearchFTSToolRows is the identifier for migration 0335.
//
// WHY THIS EXISTS (model-moves-transcript-01PMCH01 WP06).
//
// Migration 0312 attached three triggers to session_messages with no
// role predicate, because at the time every row in that table was
// conversation: a user turn, an assistant turn, or a system note. The
// moves mission changed what the table holds. Since WP02 a single human
// turn also persists a `tool_call` row per invocation and a
// `tool_result` row per return, both with role='tool', and the
// tool_result row's content is the tool's RAW OUTPUT — a whole file from
// a read, a whole build log from a bash run.
//
// Nothing about 0312 noticed. The corpus Cmd-F searches quietly became
// mostly machine output:
//
//   - VOLUME. Prose contributes a couple of KB to a turn; one file read
//     contributes the file. BM25 ranks by term frequency against a
//     corpus that is now dominated by rows nobody wrote, so a search for
//     an ordinary word returns tool dumps ahead of the sentence the user
//     is actually looking for.
//   - NOISE WITH NO LANGUAGE IN IT. A `tool_call` row's content is
//     displayArgsSummary's rendering — `kenaz__write_file(content=
//     <string>, path=<string>)`. It is a synthetic display string
//     containing no user language at all, repeated dozens of times per
//     session. There is no query for which it is the right answer.
//   - REACH. The transcript already shows tool output in the session
//     that produced it. Indexing it turns one session's raw output into
//     a cross-session grep over every tool payload the harness ever
//     received, which is the widest display surface in the app —
//     while spec §4's redaction contract has the DISPLAY layer refusing
//     to carry even a tool ARGUMENT's value.
//
// SO TOOL ROWS COME OUT OF THE INDEX. Read as a capability question this
// removes nothing: before WP02 tool output never reached
// session_messages, so it was never searchable, and this restores the
// corpus that shipped for every release up to this one. Read as a
// regression it is the fix. The alternative — index them and let the UI
// filter — was rejected because it keeps the ranking damage (an excluded
// role still competes for the LIMIT when the filter is off, and off is
// the default) and buys a filter for content that has no query.
//
// The role filter in SearchModal.vue offers User / Assistant / System.
// After this migration that list is exactly the corpus, so the UI needs
// no Tool option and — importantly — does not acquire one that would
// return nothing.
//
// THE BACKFILL IS THE POINT. A migration that only swapped the triggers
// would leave every tool row ingested since WP02 in the index forever:
// FTS5 keeps its own term store, and nothing re-derives it from the
// content table unless asked. Note that 'rebuild' is NOT the tool for
// this — it re-reads every row of session_messages, tool rows included,
// and would put them straight back. The Up below issues an explicit
// per-row 'delete' for exactly the rows the new triggers would have
// skipped, which is the only form that shrinks the index.
//
// Down restores 0312's unguarded triggers and re-indexes the tool rows
// so a Down → Up cycle is lossless in both directions.
//
// Numbering: 0335, the next free version in the sessions block after
// 0334 (move fidelity).
const migrationIDSearchFTSToolRows = "sessions/0335-search-fts-exclude-tool-rows"

// sqlDropFTSTriggers removes 0312's unguarded trigger set. Listed as
// individual statements because CREATE TRIGGER bodies below carry
// internal semicolons and the two lists are executed the same way.
var sqlDropFTSTriggers = []string{
	"DROP TRIGGER IF EXISTS messages_fts_ai",
	"DROP TRIGGER IF EXISTS messages_fts_au",
	"DROP TRIGGER IF EXISTS messages_fts_ad",
	"DROP TRIGGER IF EXISTS messages_fts_au_del",
	"DROP TRIGGER IF EXISTS messages_fts_au_ins",
}

// sqlTriggerInsertNoTool is the AFTER INSERT trigger, guarded so a
// role='tool' row never enters the index.
const sqlTriggerInsertNoTool = `CREATE TRIGGER IF NOT EXISTS messages_fts_ai
    AFTER INSERT ON session_messages WHEN new.role <> 'tool' BEGIN
        INSERT INTO messages_fts(rowid, content)
            VALUES (new.rowid, new.content);
    END`

// sqlTriggerUpdateDeleteNoTool is the delete half of the update path.
//
// The update trigger is SPLIT in two rather than guarded once, and the
// split is load-bearing. FTS5's 'delete' command on an external-content
// table subtracts the supplied terms from the index; issuing it for a
// row that was never indexed drives the term counts negative and the
// next MATCH reports the database as malformed. So the delete half must
// key on old.role and the insert half on new.role, independently. One
// trigger guarded by either alone corrupts the index the moment a row's
// role changes — and "role never changes today" is exactly the kind of
// incidental truth this mission has already been bitten by once.
const sqlTriggerUpdateDeleteNoTool = `CREATE TRIGGER IF NOT EXISTS messages_fts_au_del
    AFTER UPDATE ON session_messages WHEN old.role <> 'tool' BEGIN
        INSERT INTO messages_fts(messages_fts, rowid, content)
            VALUES ('delete', old.rowid, old.content);
    END`

// sqlTriggerUpdateInsertNoTool is the insert half of the update path.
const sqlTriggerUpdateInsertNoTool = `CREATE TRIGGER IF NOT EXISTS messages_fts_au_ins
    AFTER UPDATE ON session_messages WHEN new.role <> 'tool' BEGIN
        INSERT INTO messages_fts(rowid, content)
            VALUES (new.rowid, new.content);
    END`

// sqlTriggerDeleteNoTool is the AFTER DELETE trigger, guarded for the
// same reason as the update path's delete half.
const sqlTriggerDeleteNoTool = `CREATE TRIGGER IF NOT EXISTS messages_fts_ad
    AFTER DELETE ON session_messages WHEN old.role <> 'tool' BEGIN
        INSERT INTO messages_fts(messages_fts, rowid, content)
            VALUES ('delete', old.rowid, old.content);
    END`

// sqlPurgeToolRowsFromFTS evicts every already-indexed tool row. The
// (rowid, content) pairs come from the content table itself, so they are
// exactly the values FTS5 holds for those rows — the precondition the
// 'delete' command requires.
const sqlPurgeToolRowsFromFTS = `INSERT INTO messages_fts(messages_fts, rowid, content)
    SELECT 'delete', rowid, content FROM session_messages WHERE role = 'tool'`

// sqlBackfillToolRowsIntoFTS is the Down-side inverse of the purge.
const sqlBackfillToolRowsIntoFTS = `INSERT INTO messages_fts(rowid, content)
    SELECT rowid, content FROM session_messages WHERE role = 'tool'`

// sqlSearchFTSToolRowsSchema is the concatenated DDL used as UpSource
// for hash computation.
const sqlSearchFTSToolRowsSchema = sqlTriggerInsertNoTool + ";\n" +
	sqlTriggerUpdateDeleteNoTool + ";\n" +
	sqlTriggerUpdateInsertNoTool + ";\n" +
	sqlTriggerDeleteNoTool + ";\n" +
	sqlPurgeToolRowsFromFTS + ";\n"

// migration0335 re-guards the FTS5 sync triggers so role='tool' rows
// stay out of the search corpus, and evicts the ones already in it. See
// the doc comment above for the full rationale.
func migration0335() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSearchFTSToolRows,
		Version:       335,
		OwningMission: OwningMission,
		UpSource:      sqlSearchFTSToolRowsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			stmts := append([]string{}, sqlDropFTSTriggers...)
			stmts = append(stmts,
				sqlTriggerInsertNoTool,
				sqlTriggerUpdateDeleteNoTool,
				sqlTriggerUpdateInsertNoTool,
				sqlTriggerDeleteNoTool,
				// Purge AFTER the triggers are swapped: the purge is
				// itself an INSERT into messages_fts and touches no row of
				// session_messages, so ordering is not a correctness
				// requirement here — but doing it last means a partial
				// apply can never leave the old triggers re-indexing what
				// the purge just removed.
				sqlPurgeToolRowsFromFTS,
			)
			for _, stmt := range stmts {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			stmts := append([]string{}, sqlDropFTSTriggers...)
			stmts = append(stmts,
				sqlBackfillToolRowsIntoFTS,
				sqlTriggerInsert,
				sqlTriggerUpdate,
				sqlTriggerDelete,
			)
			for _, stmt := range stmts {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
