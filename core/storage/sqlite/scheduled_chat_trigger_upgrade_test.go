package sqlite_test

// model-scheduled-jobs-01PMSJ01 WP08: one-shot schedules. Migration
// sessions/0339-scheduled-chat-runs-trigger-kind adds trigger_kind and
// run_at to scheduled_chat_runs.
//
// CLAUDE.md blind spot #3: every test starting from an EMPTY database
// cannot see migration-selection defects — the migration high-water mark
// starts at 0 and everything applies in one ascending pass, which is
// exactly the condition under which the v0.63.0 P0 was invisible. THIS
// migration additionally sits BELOW an already-applied migration
// (sessions/0340, WP09) on any install produced by a release after
// v0.71.0 — the exact shape the v0.63.0 bug had, deliberately
// reconstructed here rather than avoided: this test boots
// testdata/upgrade/v0.78.1 (whose dump.sql already carries 0340 applied,
// per its own PROVENANCE.md) through the production Open path and
// proves 0339 still applies alongside it.
//
// Mirrors TestCedarDecisionStore_AppliesOverPreviousReleaseSnapshot
// (core/storage/sqlite/cedar_decision_upgrade_test.go) and
// TestSQLiteAnchorStore_AC004_AppliesOverPreviousReleaseSnapshot
// (core/trust/anchor_sqlite_test.go): open the snapshot, prove the new
// columns are not just PRESENT but USABLE by writing through it, close,
// reopen, and read the write back — persistence across a close/reopen
// cycle is what actually proves nothing lives in Go-side memory that
// happens to look right within one process.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestMigration0339_AppliesOverPreviousReleaseSnapshotAlreadyAt0340(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("testdata", "upgrade", "v0.78.1", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.78.1 snapshot not available at %s: %v", dumpPath, err)
	}

	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.78.1 snapshot: %v", err)
	}
	// The snapshot already carries a scheduled_chat_runs row from the
	// seed corpus. Confirm trigger_kind/run_at do NOT exist yet — proves
	// this test is really exercising 0339's ADD COLUMN, not a no-op on a
	// database that already has the columns.
	if columnExists(t, raw, "scheduled_chat_runs", "trigger_kind") {
		t.Fatal("v0.78.1 snapshot already has trigger_kind — this test no longer proves what it claims")
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialize: %v", err)
	}

	// ---- Open through the production path: applies migration 0339
	// (sessions/0338 too, and every other migration newer than the
	// snapshot) against the upgraded database. ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.78.1 snapshot failed: %v", err)
	}

	store := scheduler.NewSQLiteChatStore(db)

	// Pre-existing rows backfill trigger_kind='cron' (the schema
	// default) and keep firing as recurring schedules — the mission's own
	// AC-010 "also fails if a cron row's behaviour changed" clause,
	// checked here at the column level: List() must not error, and every
	// row must read EffectiveTriggerKind()=="cron" (never "" — the SQL
	// column has NOT NULL DEFAULT, so a real backfill, not a Go-side
	// zero-value coincidence).
	existing, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List after Open: %v", err)
	}
	if len(existing) == 0 {
		t.Fatal("expected at least one pre-existing scheduled_chat_runs row from the seed corpus")
	}
	for _, r := range existing {
		if r.EffectiveTriggerKind() != scheduler.TriggerKindCron {
			t.Errorf("pre-existing row %s: EffectiveTriggerKind() = %q, want %q (the backfilled default)",
				r.ID, r.EffectiveTriggerKind(), scheduler.TriggerKindCron)
		}
		if r.RunAt != nil {
			t.Errorf("pre-existing row %s: RunAt = %v, want nil for a cron row", r.ID, r.RunAt)
		}
	}

	// ---- Write a NEW trigger_kind='once' row through the upgraded
	// schema, proving the columns are usable, not merely present. ----
	runAt := time.Now().Add(1 * time.Hour).UTC()
	now := time.Now().UTC()
	if err := store.Create(ctx, scheduler.ChatRunRecord{
		ID:             "upgrade-once-probe",
		Name:           "post-upgrade once probe",
		PromptTemplate: "hello",
		OutputSink:     "none",
		Enabled:        true,
		TriggerKind:    scheduler.TriggerKindOnce,
		RunAt:          &runAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("Create a trigger_kind='once' row on the upgraded schema: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	// ---- Reopen from the SAME upgraded file and read the once-row back
	// — proves persistence across a close/reopen, not merely an
	// in-process cache. ----
	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(ctx) })
	store2 := scheduler.NewSQLiteChatStore(db2)
	got, err := store2.Get(ctx, "upgrade-once-probe")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.EffectiveTriggerKind() != scheduler.TriggerKindOnce {
		t.Errorf("EffectiveTriggerKind() = %q, want once", got.EffectiveTriggerKind())
	}
	if got.RunAt == nil {
		t.Fatal("RunAt did not survive the close/reopen round trip")
	}
	if got.RunAt.Unix() != runAt.Unix() {
		t.Errorf("RunAt = %v, want %v", got.RunAt, runAt)
	}
}

// TestScheduledChatOneShot_FiresOnceFromUpgradeSnapshot is AC-010 itself:
// "copy testdata/upgrade/v0.63.2 [here: the newest committed snapshot],
// open it, run migrations, create a trigger_kind='once' row with run_at
// in the near future, assert it fires exactly once and then reports
// enabled = 0." Uses v0.78.1 (the newest committed snapshot at the time
// this test was written — see check-upgrade-snapshot-present.sh) rather
// than the spec's literal v0.63.2, per the same reasoning
// cedar_decision_upgrade_test.go and this file's sibling test above
// already apply: the newest snapshot is the one that proves the
// below-high-water-mark registration is safe on a REAL current install,
// not merely on a stale historical one.
func TestScheduledChatOneShot_FiresOnceFromUpgradeSnapshot(t *testing.T) {
	ctx := context.Background()
	dumpPath := filepath.Join("testdata", "upgrade", "v0.78.1", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.78.1 snapshot not available at %s: %v", dumpPath, err)
	}

	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.78.1 snapshot: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after materialize: %v", err)
	}

	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the v0.78.1 snapshot failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(ctx) })
	store := scheduler.NewSQLiteChatStore(db)

	runAt := time.Now().Add(150 * time.Millisecond)
	now := time.Now().UTC()
	if err := store.Create(ctx, scheduler.ChatRunRecord{
		ID:             "ac010-once",
		Name:           "AC-010 probe",
		PromptTemplate: "hello",
		OutputSink:     "none",
		Enabled:        true,
		TriggerKind:    scheduler.TriggerKindOnce,
		RunAt:          &runAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("Create trigger_kind='once' row: %v", err)
	}

	fired := make(chan string, 4)
	disp := chatRunDispatcherFunc(func(_ context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
		id := ""
		if job.ChatRun != nil {
			id = job.ChatRun.ID
		}
		ended := now
		select {
		case fired <- id:
		default:
		}
		return scheduler.ChatRunHistoryRecord{
			ChatRunID: id,
			SessionID: "sess-" + id,
			Status:    "completed",
			StartedAt: now,
			EndedAt:   &ended,
		}, nil
	})

	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{
		Store:      store,
		Dispatcher: disp,
	})
	if err != nil {
		t.Fatalf("NewChatCronEngine over upgraded snapshot: %v", err)
	}
	engine.Start()
	t.Cleanup(engine.Stop)

	select {
	case id := <-fired:
		if id != "ac010-once" {
			t.Errorf("fired id=%q, want ac010-once", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the one-shot row to fire against the upgraded schema")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		rec, gerr := store.Get(ctx, "ac010-once")
		if gerr != nil {
			t.Fatalf("Get: %v", gerr)
		}
		if !rec.Enabled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for enabled=0 after the one-shot fire")
		}
		time.Sleep(20 * time.Millisecond)
	}

	hist, herr := store.History(ctx, "ac010-once", 10)
	if herr != nil {
		t.Fatalf("History: %v", herr)
	}
	if len(hist) != 1 {
		t.Fatalf("history rows = %d, want exactly 1 (fires exactly once)", len(hist))
	}
}

// chatRunDispatcherFunc adapts a plain func to scheduler.ChatRunDispatcher.
type chatRunDispatcherFunc func(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error)

func (f chatRunDispatcherFunc) DispatchChatRun(ctx context.Context, job scheduler.Job, now time.Time) (scheduler.ChatRunHistoryRecord, error) {
	return f(ctx, job, now)
}
