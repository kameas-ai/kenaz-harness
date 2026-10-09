package sqlite_test

// tool-context-budget-01TCBUD01 WP01: migration
// sessions/0346-session-usage-cache-tokens adds cached_tokens and
// cache_write_tokens to session_messages, and the last-usage snapshot
// gains the request composition.
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.93.2, which predates 0346), records usage on a seeded
// assistant row exactly as v0.93.2 wrote it, and only then opens under
// HEAD. Asserts 0346 applies over populated rows, the old row's usage
// survives with no cache split, and a new row's cache split plus the
// last call's composition round-trip through real SQL across a
// close/reopen and come back out of Sessions.GetUsage.
//
// Falsifiable: drop migration0346 from Migrations() and usage.Add fails
// (no such column: cached_tokens).

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/session"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/usage"
)

// usageCacheSnapshotTag is the newest committed snapshot when 0346 was
// written; it must predate 0346 for this test to exercise the upgrade.
const usageCacheSnapshotTag = "v0.93.2"

func TestMigration0346_SessionUsageCacheTokens_UpgradesPopulatedSnapshot(t *testing.T) {
	ctx := context.Background()
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", usageCacheSnapshotTag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s fixture: %v", usageCacheSnapshotTag, err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s snapshot: %v", usageCacheSnapshotTag, err)
	}
	if columnExists(t, raw, "session_messages", "cached_tokens") {
		t.Fatalf("%s snapshot already has session_messages.cached_tokens — the fixture no longer predates 0346", usageCacheSnapshotTag)
	}
	// Usage on the seeded assistant row, as the pre-0346 release wrote it.
	if _, err := raw.ExecContext(ctx, `UPDATE session_messages
		SET prompt_tokens = 1200, completion_tokens = 80, cost_usd = 0.01, cost_source = 'provider'
		WHERE id = 'seed-msg-2'`); err != nil {
		t.Fatalf("plant pre-0346 usage: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO session_messages
		(id, session_id, sequence, role, content, created_at)
		VALUES ('seed-msg-4', 'seed-session-1', 3, 'assistant', 'post-upgrade reply', 1700000000400)`); err != nil {
		t.Fatalf("plant the post-upgrade assistant row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the %s snapshot: %v", usageCacheSnapshotTag, err)
	}
	if pending, perr := db.Migrations().Pending(); perr != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, perr)
	}
	um := usage.New(db)
	agg, err := um.GetSession(ctx, "seed-session-1")
	if err != nil {
		t.Fatalf("GetSession on the upgraded table: %v", err)
	}
	if agg.PromptTokens != 1200 || agg.CompletionTokens != 80 || agg.CachedTokens != 0 || agg.CacheWriteTokens != 0 {
		t.Fatalf("pre-0346 aggregate = %+v, want the old row intact with no cache split", agg)
	}

	if err := um.Add(ctx, usage.UsageTurn{
		SessionID: "seed-session-1", MessageID: "seed-msg-4",
		PromptTokens: 1500, CompletionTokens: 20, CachedTokens: 900, CacheWriteTokens: 300,
		CostSource: "provider",
	}); err != nil {
		t.Fatalf("usage.Add with cache split: %v", err)
	}
	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	if err := mgr.SetLastUsage(ctx, "seed-session-1", session.LastUsage{
		PromptTokens: 1500, CompletionTokens: 20, TotalTokens: 1520, CostSource: "provider",
		Composition: &session.UsageComposition{System: 600, Tools: 520, History: 30, Attachments: 12, Cached: 900, ToolsFull: 4},
	}); err != nil {
		t.Fatalf("SetLastUsage with composition: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })

	var oldCached, oldWrite *int
	if err := db2.Reader().QueryRow(ctx,
		"SELECT cached_tokens, cache_write_tokens FROM session_messages WHERE id = 'seed-msg-2'").Scan(&oldCached, &oldWrite); err != nil {
		t.Fatalf("read the pre-0346 row: %v", err)
	}
	if oldCached != nil || oldWrite != nil {
		t.Fatalf("pre-0346 row cache columns = %v/%v, want NULL (never reported)", oldCached, oldWrite)
	}

	mgr2 := session.NewManager(session.NewSQLStore(session.NewStorageDB(db2)))
	api := sessions.WithUsageManager(sessions.NewManagerAPI(mgr2), usage.New(db2))
	got, err := api.GetUsage(ctx, "seed-session-1")
	if err != nil {
		t.Fatalf("GetUsage after reopen: %v", err)
	}
	if got.PromptTokens != 2700 || got.CachedTokens != 900 {
		t.Fatalf("GetUsage = %+v, want prompt 2700 (both rows) and cached 900 (the new row)", got)
	}
	want := sessions.UsageComposition{System: 600, Tools: 520, History: 30, Attachments: 12, Cached: 900, ToolsFull: 4}
	if got.Composition == nil || *got.Composition != want {
		t.Fatalf("GetUsage composition = %+v, want %+v", got.Composition, want)
	}
	agg2, err := usage.New(db2).GetSession(ctx, "seed-session-1")
	if err != nil || agg2.CacheWriteTokens != 300 {
		t.Fatalf("aggregate after reopen = %+v (err %v), want cache_write 300", agg2, err)
	}
}
