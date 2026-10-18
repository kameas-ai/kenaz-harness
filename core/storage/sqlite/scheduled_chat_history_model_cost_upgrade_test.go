package sqlite_test

// dogfood 2026-10-08 round 2: migration
// sessions/0345-scheduled-chat-history-model-cost adds model and cost_usd
// to scheduled_chat_run_history.
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.93.1, which predates 0345), plants a schedule and a
// history row exactly as v0.93.1 wrote them, and only then opens under
// HEAD. Asserts 0345 applies over populated tables, the old row survives
// reading as model "" / cost 0, and a new row's model + cost round-trip
// through SQL across a close/reopen.
//
// Falsifiable: drop migration0345 from Migrations() and History fails
// (no such column: model).

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

func TestMigration0345_ScheduledChatHistoryModelCost_UpgradesPopulatedV0931(t *testing.T) {
	ctx := context.Background()
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.93.1", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.93.1 fixture: %v", err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.93.1 snapshot: %v", err)
	}
	if columnExists(t, raw, "scheduled_chat_run_history", "model") {
		t.Fatal("v0.93.1 snapshot already has scheduled_chat_run_history.model — the fixture no longer predates 0345")
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO scheduled_chat_runs
		(id, name, prompt_template, cron, created_at, updated_at) VALUES ('cr-old', 'Sentinel', 'ping', '*/3 * * * *', 1, 1)`); err != nil {
		t.Fatalf("plant schedule: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO scheduled_chat_run_history
		(id, chat_run_id, session_id, status, started_at, ended_at, output_snippet, error)
		VALUES ('h-old', 'cr-old', 's-old', 'failed', 100, 101, '', 'boom')`); err != nil {
		t.Fatalf("plant pre-0345 history row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.93.1 snapshot: %v", err)
	}
	if pending, perr := db.Migrations().Pending(); perr != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, perr)
	}
	store := scheduler.NewSQLiteChatStore(db)
	hist, err := store.History(ctx, "cr-old", 10)
	if err != nil {
		t.Fatalf("History on the upgraded table: %v", err)
	}
	if len(hist) != 1 || hist[0].ID != "h-old" || hist[0].Model != "" || hist[0].CostUSD != 0 || hist[0].Error != "boom" {
		t.Fatalf("pre-0345 row = %+v, want it intact with model \"\" / cost 0", hist)
	}

	ended := time.Unix(300, 0).UTC()
	if err := store.AppendHistory(ctx, scheduler.ChatRunHistoryRecord{
		ID: "h-new", ChatRunID: "cr-old", SessionID: "s-new", Status: "completed",
		StartedAt: time.Unix(200, 0).UTC(), EndedAt: &ended,
		Model: "~anthropic/claude-haiku-latest", CostUSD: 0.1124,
	}); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	hist, err = scheduler.NewSQLiteChatStore(db2).History(ctx, "cr-old", 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("History after reopen = %+v (err %v), want 2 rows", hist, err)
	}
	if hist[0].ID != "h-new" || hist[0].Model != "~anthropic/claude-haiku-latest" || hist[0].CostUSD != 0.1124 {
		t.Fatalf("newest row = %+v, want model/cost round-tripped", hist[0])
	}
}
