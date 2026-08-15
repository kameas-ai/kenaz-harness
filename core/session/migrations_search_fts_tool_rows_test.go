package session_test

// migrations_search_fts_tool_rows_test.go — model-moves-transcript-01PMCH01
// WP06, the search-corpus half.
//
// The interesting assertion here is about a database that ALREADY has
// tool rows in its index, and the only honest way to produce one is to
// run migration 0335's Down, dirty the index through the restored 0312
// triggers, and then run its Up. A test that only opened a fresh DB
// would be asserting against a schema where the guard had always been
// present — it would pass whether or not the backfill existed, which is
// precisely the failure mode the WP was told to avoid ("a migration that
// only changes the trigger leaves old rows indexed").
//
// The migration is reached through session.Migrations() rather than by
// naming migration0335() directly: package session cannot be imported
// from an internal test that also needs storage/sqlite (import cycle),
// and looking it up by version is the same lookup the runner does.
//
// Everything here runs against a real sqlite file through the real
// session store. No memory store: the FTS index does not exist in one,
// so a memory-backed assertion about search would be vacuous by
// construction.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// ftsToolRowsMigration returns the sessions/0335 migration by version,
// the way the migration runner finds it.
func ftsToolRowsMigration(t *testing.T) migrations.Migration {
	t.Helper()
	for _, m := range session.Migrations() {
		if m.Version == 335 {
			return m
		}
	}
	t.Fatal("sessions migration 335 is not registered in session.Migrations()")
	return migrations.Migration{}
}

// migTx adapts a storage.WriteTx to the migrations.WriteTx a
// Migration.Up/Down expects. The two interfaces are method-for-method
// identical but nominally distinct, and the production adapter in
// core/storage/sqlite only bridges the other direction.
type migTx struct{ tx storage.WriteTx }

func (m migTx) Exec(ctx context.Context, q string, args ...any) (migrations.Result, error) {
	return m.tx.Exec(ctx, q, args...)
}

func (m migTx) QueryRow(ctx context.Context, q string, args ...any) migrations.Row {
	return m.tx.QueryRow(ctx, q, args...)
}

func (m migTx) Query(ctx context.Context, q string, args ...any) (migrations.Rows, error) {
	return m.tx.Query(ctx, q, args...)
}

func ftsToolRowsDB(t *testing.T) storage.DB {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

func runMigration(t *testing.T, db storage.DB, fn func(context.Context, migrations.WriteTx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		return fn(ctx, migTx{tx: tx})
	}); err != nil {
		t.Fatalf("migration: %v", err)
	}
}

// ftsMatchCount counts index hits for a term. It queries messages_fts
// directly rather than through the search RPC so the assertion is about
// the corpus, not about a filter layered on top of it — a role filter in
// SQL would make this pass with the whole tool corpus still indexed.
func ftsMatchCount(t *testing.T, db storage.DB, term string) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?", term).Scan(&n); err != nil {
		t.Fatalf("MATCH %q: %v", term, err)
	}
	return n
}

// seedMoveBearingSession writes one human turn's worth of rows: a user
// turn, an assistant answer, and the tool_call / tool_result pair WP02
// introduced. Every row carries the term "quixotic" so a single query
// distinguishes "which roles are indexed" from "is anything indexed".
func seedMoveBearingSession(t *testing.T, db storage.DB, sessionID string) {
	t.Helper()
	ctx := context.Background()
	store := session.NewSQLStore(session.NewStorageDB(db))
	now := time.Now().UTC()
	if err := store.Create(ctx, session.Record{
		ID: sessionID, Name: "moves", CreatedAt: now, UpdatedAt: now,
		LastActiveAt: now, ContextKind: session.ContextKindSystem,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rows := []session.Message{
		{ID: sessionID + "-u", SessionID: sessionID, Role: session.RoleUser,
			Content: "quixotic user question", CreatedAt: now},
		{ID: sessionID + "-tc", SessionID: sessionID, Role: session.RoleTool,
			Content: "bash(cmd=<string>) quixotic", CreatedAt: now},
		{ID: sessionID + "-tr", SessionID: sessionID, Role: session.RoleTool,
			Content: "quixotic quixotic quixotic raw tool output dump", CreatedAt: now},
		{ID: sessionID + "-a", SessionID: sessionID, Role: session.RoleAssistant,
			Content: "quixotic assistant answer", CreatedAt: now},
	}
	for _, m := range rows {
		if _, err := store.AppendMessage(ctx, m); err != nil {
			t.Fatalf("AppendMessage %s: %v", m.ID, err)
		}
	}
}

// TestMigration0335_ToolRowsNeverEnterTheIndex is the forward case: on a
// database created after this migration, writing a move-bearing turn
// indexes the user and assistant rows and neither tool row.
//
// Mutation evidence: drop `WHEN new.role <> 'tool'` from
// sqlTriggerInsertNoTool and this reports 4 hits instead of 2.
func TestMigration0335_ToolRowsNeverEnterTheIndex(t *testing.T) {
	t.Parallel()
	db := ftsToolRowsDB(t)
	seedMoveBearingSession(t, db, "s-fresh")

	if got := ftsMatchCount(t, db, "quixotic"); got != 2 {
		t.Errorf("indexed rows = %d, want 2 (the user turn and the assistant answer). "+
			"A tool_call row's content is displayArgsSummary output and a tool_result "+
			"row's is raw tool output; neither belongs in the corpus Cmd-F searches.", got)
	}
	// Positive control: the corpus is not simply empty.
	if got := ftsMatchCount(t, db, "answer"); got != 1 {
		t.Errorf("assistant answer hits = %d, want 1 — the guard removed conversation too", got)
	}
}

// TestMigration0335_EvictsRowsIndexedBeforeIt is the case the WP brief
// named explicitly: an installation that ran WP02 through WP05 already
// has tool output in its index, and a migration that only swapped the
// triggers would leave every one of those rows searchable forever.
//
// The test manufactures that exact database by rolling 0335 back (which
// restores 0312's unguarded triggers AND re-indexes the tool rows),
// writing the turn, asserting the dirty state is real, then rolling
// forward again.
//
// Mutation evidence: delete sqlPurgeToolRowsFromFTS from
// ftsToolRowsMigration(t).Up's statement list and the post-Up assertion reports
// 4 hits — the triggers are correct for new rows and the old ones never
// leave.
func TestMigration0335_EvictsRowsIndexedBeforeIt(t *testing.T) {
	t.Parallel()
	db := ftsToolRowsDB(t)

	// Roll back to the pre-WP06 world.
	runMigration(t, db, ftsToolRowsMigration(t).Down)

	seedMoveBearingSession(t, db, "s-legacy")

	// Anti-vacuity: the rolled-back triggers really did index the tool
	// rows. Without this the test below would pass on a database where
	// nothing was ever indexed.
	if got := ftsMatchCount(t, db, "quixotic"); got != 4 {
		t.Fatalf("pre-migration index = %d rows, want 4 — the Down did not restore "+
			"0312's unguarded triggers, so the rest of this test proves nothing", got)
	}

	// Roll forward. This is the migration under test.
	runMigration(t, db, ftsToolRowsMigration(t).Up)

	if got := ftsMatchCount(t, db, "quixotic"); got != 2 {
		t.Errorf("post-migration index = %d rows, want 2. Rows ingested before the "+
			"migration are still in the FTS term store: the trigger swap alone does "+
			"not evict them, and 'rebuild' would re-add them from the content table.", got)
	}
	// The conversation half must be untouched by the purge.
	if got := ftsMatchCount(t, db, "answer"); got != 1 {
		t.Errorf("assistant answer hits = %d, want 1 — the purge took conversation with it", got)
	}
	if got := ftsMatchCount(t, db, "dump"); got != 0 {
		t.Errorf("raw tool output still matches 'dump' (%d hits) after the purge", got)
	}
}

// TestMigration0335_IndexSurvivesAnUpdateOfAToolRow guards the reason
// the update trigger is split in two.
//
// FTS5's 'delete' command subtracts the supplied terms from the index.
// Issuing it for a row that was never indexed drives term counts
// negative and the next MATCH fails with a malformed-database error. A
// tool row IS updated in normal operation — ApplyCompaction stamps
// archived_at on it — so a single update trigger guarded on new.role
// alone would corrupt the index on the first compaction of a
// move-bearing session.
//
// Mutation evidence: replace the two _au_del / _au_ins triggers with
// 0312's single unguarded messages_fts_au and this test fails on the
// MATCH after the update.
func TestMigration0335_IndexSurvivesAnUpdateOfAToolRow(t *testing.T) {
	t.Parallel()
	db := ftsToolRowsDB(t)
	ctx := context.Background()
	seedMoveBearingSession(t, db, "s-upd")

	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx,
			"UPDATE session_messages SET archived_at = ? WHERE id = ?",
			time.Now().UTC().UnixNano(), "s-upd-tr")
		return err
	}); err != nil {
		t.Fatalf("archive the tool row: %v", err)
	}

	// The index must still be queryable and still hold exactly the two
	// conversation rows.
	if got := ftsMatchCount(t, db, "quixotic"); got != 2 {
		t.Errorf("after updating a never-indexed tool row the index holds %d rows, want 2", got)
	}
}
