package sqlite_test

// sessions/0349-scheduled-chat-context-budget adds the per-schedule
// context policy (context_budget_tokens, keep_recent_runs, memory_focus)
// to scheduled_chat_runs and prompt_message_id / compaction_cost_usd to
// scheduled_chat_run_history.
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.99.0, which predates 0349) with its seeded schedule and
// history rows, plus a planted pre-0349 schedule and history row, and
// only then opens under HEAD. Asserts 0349 applies over populated
// tables, every existing schedule reads back with the owner-approved
// defaults (20000 / 2 / ""), old history rows read back with no prompt
// boundary and no compaction cost, and new values round-trip through SQL
// across a close/reopen — including keep_recent_runs = 0, which a
// zero-value default would swallow.
//
// Falsifiable: drop migration0349 from Migrations() and Get fails (no
// such column: context_budget_tokens).

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

func TestMigration0349_ScheduledChatContextBudget_UpgradesPopulatedV0990(t *testing.T) {
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
	if columnExists(t, raw, "scheduled_chat_runs", "context_budget_tokens") ||
		columnExists(t, raw, "scheduled_chat_run_history", "prompt_message_id") {
		t.Fatal("v0.99.0 snapshot already has the 0349 columns — the fixture no longer predates 0349")
	}
	var seeded, seededHist int
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_chat_runs`).Scan(&seeded); err != nil || seeded == 0 {
		t.Fatalf("fixture sanity: %d seeded scheduled_chat_runs rows (err %v), want at least one", seeded, err)
	}
	if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_chat_run_history`).Scan(&seededHist); err != nil {
		t.Fatalf("count seeded history: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO scheduled_chat_runs
		(id, name, prompt_template, cron, created_at, updated_at) VALUES ('cr-old', 'Sentinel', 'ping', '*/30 * * * *', 1, 1)`); err != nil {
		t.Fatalf("plant pre-0349 schedule: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO scheduled_chat_run_history
		(id, chat_run_id, session_id, status, started_at, output_snippet, error, model, cost_usd)
		VALUES ('h-old', 'cr-old', 's-1', 'completed', 10, 'hi', '', 'm', 0.5)`); err != nil {
		t.Fatalf("plant pre-0349 history row: %v", err)
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
		if r.ContextBudgetTokens != scheduler.DefaultContextBudgetTokens || r.KeepRecentRuns == nil ||
			*r.KeepRecentRuns != scheduler.DefaultKeepRecentRuns || r.MemoryFocus != "" {
			t.Fatalf("pre-0349 row %s policy = %d/%v/%q, want the defaults %d/%d/\"\"", r.ID,
				r.ContextBudgetTokens, r.KeepRecentRuns, r.MemoryFocus,
				scheduler.DefaultContextBudgetTokens, scheduler.DefaultKeepRecentRuns)
		}
	}
	readHist := 0
	for _, r := range all {
		rows, herr := store.History(ctx, r.ID, 1000)
		if herr != nil {
			t.Fatalf("History(%s) on the upgraded table: %v", r.ID, herr)
		}
		for _, h := range rows {
			readHist++
			if h.PromptMessageID != "" || h.CompactionCostUSD != nil {
				t.Fatalf("pre-0349 history row %s = %+v, want no prompt boundary and no compaction cost", h.ID, h)
			}
		}
	}
	if readHist != seededHist+1 {
		t.Fatalf("read %d history rows back, want %d (seeded + planted)", readHist, seededHist+1)
	}
	hist, err := store.History(ctx, "cr-old", 10)
	if err != nil || len(hist) != 1 {
		t.Fatalf("History(cr-old) = %+v (err %v), want the planted row", hist, err)
	}
	if hist[0].PromptMessageID != "" || hist[0].CompactionCostUSD != nil || hist[0].CostUSD != 0.5 {
		t.Fatalf("pre-0349 history row = %+v, want no prompt boundary, no compaction cost, cost kept", hist[0])
	}

	// New values round-trip, including keep 0.
	old, err := store.Get(ctx, "cr-old")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	zero := 0
	old.ContextBudgetTokens = 12000
	old.KeepRecentRuns = &zero
	old.MemoryFocus = "open action items"
	old.UpdatedAt = time.Unix(2, 0).UTC()
	if err := store.Update(ctx, old); err != nil {
		t.Fatalf("Update: %v", err)
	}
	cost := 0.0125
	if err := store.AppendHistory(ctx, scheduler.ChatRunHistoryRecord{
		ID: "h-new", ChatRunID: "cr-old", SessionID: "s-1", Status: "completed",
		StartedAt: time.Unix(20, 0).UTC(), CostUSD: 0.3, PromptMessageID: "msg-prompt", CompactionCostUSD: &cost,
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
	store2 := scheduler.NewSQLiteChatStore(db2)
	got, err := store2.Get(ctx, "cr-old")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.ContextBudgetTokens != 12000 || got.KeepRecentRuns == nil || *got.KeepRecentRuns != 0 || got.MemoryFocus != "open action items" {
		t.Fatalf("after reopen policy = %d/%v/%q, want 12000/0/\"open action items\"", got.ContextBudgetTokens, got.KeepRecentRuns, got.MemoryFocus)
	}
	hist, err = store2.History(ctx, "cr-old", 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("History after reopen = %+v (err %v), want 2 rows", hist, err)
	}
	if hist[0].ID != "h-new" || hist[0].PromptMessageID != "msg-prompt" || hist[0].CompactionCostUSD == nil || *hist[0].CompactionCostUSD != cost {
		t.Fatalf("new history row = %+v, want prompt boundary and compaction cost kept", hist[0])
	}
	if n, _ := store2.History(ctx, "nope", 1); len(n) != 0 {
		t.Fatalf("History(unknown) = %+v, want none", n)
	}
}
