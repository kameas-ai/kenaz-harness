package rpc

// AC-013 (model-scheduled-jobs-01PMSJ01 WP12, FR-008): "Nothing regresses
// for a user with no schedules. A build with zero scheduled_chat_runs
// rows behaves byte-identically at boot; the cron engine registers
// nothing and starts no goroutines beyond the engine itself."
//
// The task brief for this WP is explicit: "AC-013 was NOT independently
// verified; treat it as unverified, not passing." This file is that
// independent verification.
//
// Boots through a REAL upgrade snapshot (CLAUDE.md blind spot #3) with
// its scheduled_chat_runs rows deleted post-materialize — the schema
// still goes through the full migration path (sessions/0338, 0339, 0340
// all apply against an install that predates them), only the row COUNT
// is forced to zero, which is the scenario AC-013 actually describes
// ("a user with no schedules"), not "a database that never had the
// feature."
//
// What this test can and cannot assert about goroutines: "no new
// goroutines beyond the engine itself" reduces, by construction, to "zero
// scheduled_chat_runs rows means NewChatCronEngine's boot loop calls
// registerRecord zero times" — core/scheduler/chat_cron_engine_test.go's
// own suite already proves an engine with zero registered entries starts
// exactly one robfig/cron dispatcher goroutine via Start() and nothing
// per-entry. This test proves the PRECONDITION (zero rows survive the
// real migration path and produce zero registrations, zero history, zero
// blocked-request rows) rather than re-deriving robfig/cron's own
// internals.

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	blockedrequestsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/blockedrequests"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func TestAC013_ZeroScheduleBuildBehavesByteIdenticallyAtBoot(t *testing.T) {
	sandboxUserConfigDir(t)
	ctx := context.Background()

	dumpPath := filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", "v0.78.1", "dump.sql")
	dumpText, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Skipf("v0.78.1 snapshot not available at %s: %v", dumpPath, err)
	}

	dataDir := t.TempDir()
	rawPath := filepath.Join(dataDir, "data.db")
	raw, err := sql.Open("sqlite", "file:"+rawPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialize v0.78.1 snapshot: %v", err)
	}
	// Force the "zero schedules" scenario AC-013 describes: the snapshot's
	// seed corpus carries at least one scheduled_chat_runs row (see
	// TestUpgradePath's expectedChangedTables note on this exact table).
	if _, err := raw.ExecContext(ctx, "DELETE FROM scheduled_chat_run_history"); err != nil {
		t.Fatalf("delete scheduled_chat_run_history: %v", err)
	}
	if _, err := raw.ExecContext(ctx, "DELETE FROM scheduled_chat_runs"); err != nil {
		t.Fatalf("delete scheduled_chat_runs: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	// Confirm the precondition through the PRODUCTION migration path
	// (core.New does not itself open storage — force it, as
	// api := New(c) below will anyway) before asserting anything about
	// boot behaviour: zero rows, on an install that just walked every
	// migration from v0.78.1 through HEAD.
	db := c.Storage()
	if db == nil {
		t.Fatal("c.Storage() returned nil — migration path failed")
	}
	chatStore := scheduler.NewSQLiteChatStore(db)
	preRows, err := chatStore.List(ctx)
	if err != nil {
		t.Fatalf("List before boot: %v", err)
	}
	if len(preRows) != 0 {
		t.Fatalf("setup failed: %d scheduled_chat_runs rows survived the DELETE, want 0", len(preRows))
	}

	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)

	// ---- Precondition for "no goroutines beyond the engine": the
	// engine was constructed but not yet armed. ----
	if api.chatCronEngine == nil {
		t.Fatal("New(c) with a real DB did not construct chatCronEngine even with zero rows — FR-008 does not mean 'skip construction', it means 'construct but register nothing'")
	}
	if api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine reports Started() before SetContext")
	}

	api.SetContext(ctx)

	if !api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine.Started() == false after SetContext — the engine itself must still start (FR-008 is about registrations, not about the engine)")
	}

	// ---- No history rows. A zero-schedule build that somehow produced a
	// history row would mean something fired that was never registered. ----
	postRows, err := chatStore.List(ctx)
	if err != nil {
		t.Fatalf("List after boot: %v", err)
	}
	if len(postRows) != 0 {
		t.Fatalf("scheduled_chat_runs rows after boot = %d, want 0 (boot must not fabricate schedule rows)", len(postRows))
	}
	var historyCount int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM scheduled_chat_run_history").Scan(&historyCount); err != nil {
		t.Fatalf("count scheduled_chat_run_history: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("scheduled_chat_run_history rows after boot = %d, want 0", historyCount)
	}

	// ---- No blocked_permission_requests rows (model-scheduled-jobs-
	// 01PMSJ01 WP06/WP07's own contribution to FR-008: this table's only
	// current producer is a scheduled-run's denied fs write, so zero
	// schedules also means zero blocked-request rows). ----
	pending, err := api.BlockedRequests().ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending after boot: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending blocked_permission_requests after boot = %d, want 0", len(pending))
	}
	if _, ok := api.BlockedRequests().(*blockedrequestsview.API); !ok {
		t.Fatal("api.BlockedRequests() did not return the wired *blockedrequestsview.API — a graceful-empty stub would trivially pass every assertion above without proving anything")
	}

	// Shutdown must stop the engine cleanly (mirrors the existing
	// TestChatCronEngine_ConstructedButNotStarted_UntilSetContext).
	api.Shutdown()
	if api.chatCronEngine.Started() {
		t.Fatal("chatCronEngine still reports Started() after Shutdown")
	}
}
