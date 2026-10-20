package sqlite_test

// sessions/0348-scheduled-chat-runs-session-id adds session_id to
// scheduled_chat_runs: the ONE conversation a schedule's runs continue.
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.99.0, which predates 0348) with its seeded schedule row,
// and only then opens under HEAD. Asserts 0348 applies over a populated
// table, the old row survives reading as "no conversation yet", a stored
// conversation round-trips through SQL across a close/reopen, an inline
// edit (Update) keeps it, and clearing it reads back empty.
//
// Falsifiable: drop migration0348 from Migrations() and Get fails (no
// such column: session_id).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

func TestMigration0348_ScheduledChatRunsSession_UpgradesPopulatedV0990(t *testing.T) {
	ctx := context.Background()
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.99.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.99.0 fixture: %v", err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.99.0 snapshot: %v", err)
	}
	if columnExists(t, raw, "scheduled_chat_runs", "session_id") {
		t.Fatal("v0.99.0 snapshot already has scheduled_chat_runs.session_id — the fixture no longer predates 0348")
	}
	var seeded int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_chat_runs`).Scan(&seeded); err != nil || seeded == 0 {
		t.Fatalf("fixture sanity: %d seeded scheduled_chat_runs rows (err %v), want at least one", seeded, err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO scheduled_chat_runs
		(id, name, prompt_template, cron, created_at, updated_at) VALUES ('cr-old', 'Sentinel', 'ping', '*/30 * * * *', 1, 1)`); err != nil {
		t.Fatalf("plant pre-0348 schedule: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.99.0 snapshot: %v", err)
	}
	if pending, perr := db.Migrations().Pending(); perr != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, perr)
	}
	store := scheduler.NewSQLiteChatStore(db)
	all, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List on the upgraded table: %v", err)
	}
	if len(all) != seeded+1 {
		t.Fatalf("List = %d rows, want %d (seeded + planted)", len(all), seeded+1)
	}
	for _, r := range all {
		if r.SessionID != "" {
			t.Fatalf("pre-0348 row %s SessionID = %q, want empty (no conversation yet)", r.ID, r.SessionID)
		}
	}

	if err := store.SetSessionID(ctx, "cr-old", "s-conversation"); err != nil {
		t.Fatalf("SetSessionID: %v", err)
	}
	if err := store.SetSessionID(ctx, "cr-missing", "s-x"); err != scheduler.ErrChatRunNotFound {
		t.Fatalf("SetSessionID on a missing row = %v, want ErrChatRunNotFound", err)
	}
	// An inline edit must not drop the conversation link.
	old, err := store.Get(ctx, "cr-old")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	old.PromptTemplate = "ping, edited"
	old.UpdatedAt = time.Unix(2, 0).UTC()
	if err := store.Update(ctx, old); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2 := scheduler.NewSQLiteChatStore(db2)
	got, err := store2.Get(ctx, "cr-old")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.SessionID != "s-conversation" || got.PromptTemplate != "ping, edited" {
		t.Fatalf("after reopen = %+v, want the stored conversation kept across an edit", got)
	}
	if err := store2.SetSessionID(ctx, "cr-old", ""); err != nil {
		t.Fatalf("clear SetSessionID: %v", err)
	}
	if got, _ := store2.Get(ctx, "cr-old"); got.SessionID != "" {
		t.Fatalf("SessionID after clear = %q, want empty", got.SessionID)
	}
}
