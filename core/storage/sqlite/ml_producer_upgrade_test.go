package sqlite_test

// ml-producer/1700-ml-outbox-and-tasks adds ml_outbox + ml_tasks
// (ml-producer-01MLPRD01 WP02).
//
// CLAUDE.md blind spot #3: starts from the newest committed release
// snapshot (v0.94.0, which predates 1700) with its seeded rows, opens
// under HEAD, and drives the production mlstore through a close/reopen:
//
//   - outbox seq is never reused after a purge (AUTOINCREMENT), across a
//     reopen too — fleet keys events by it;
//   - task counters written before a reopen read back after it;
//   - the snapshot's own sessions survive Open.
//
// Falsifiable: drop mlstore.RegisterMigrations from storagesqlite.Open and
// every write fails (no such table: ml_outbox); switch seq to a plain
// INTEGER PRIMARY KEY and the post-purge seq restarts at 1.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

func openMLStore(t *testing.T, dir string) (storage.DB, *mlstore.Store) {
	t.Helper()
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage.DB does not expose SQL()")
	}
	return db, mlstore.New(h.SQL())
}

func commitEvents(t *testing.T, s *mlstore.Store, n int) []int64 {
	t.Helper()
	drafts := make([]mlstore.EventDraft, n)
	for i := range drafts {
		drafts[i] = mlstore.EventDraft{CreatedAt: int64(i + 1), Body: func(seq int64) ([]byte, error) {
			return []byte(fmt.Sprintf(`{"id":%d,"kind":"agent.tool"}`, seq)), nil
		}}
	}
	seqs, err := s.Commit(context.Background(), mlstore.Write{Events: drafts})
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return seqs
}

func TestMigration1700_MLProducer_UpgradesPopulatedV0940(t *testing.T) {
	ctx := context.Background()
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.94.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.94.0 fixture: %v", err)
	}
	dir := t.TempDir()
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.94.0 snapshot: %v", err)
	}
	var tables int
	if err := raw.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('ml_outbox','ml_tasks')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("v0.94.0 snapshot already has the ml-producer tables — the fixture no longer predates 1700")
	}
	var seededSessions int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&seededSessions); err != nil {
		t.Fatal(err)
	}
	if seededSessions == 0 {
		t.Fatal("snapshot seeds no sessions; the test needs a populated install")
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, store := openMLStore(t, dir)
	if pending, perr := db.Migrations().Pending(); perr != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none", pending, perr)
	}
	var sessionsAfter int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionsAfter); err != nil {
		t.Fatal(err)
	}
	if sessionsAfter != seededSessions {
		t.Fatalf("sessions after Open = %d, want the snapshot's %d", sessionsAfter, seededSessions)
	}

	first := commitEvents(t, store, 3)
	task := mlstore.TaskRow{
		TaskID: "agent-0123456789abcdef", SessionHash: "0123456789abcdef", RepoRootHash: "fedcba9876543210",
		Phase: "testing", Files: map[string]int{"aaaaaaaaaaaaaaaa.go": 2},
		StartedAt: 100, LastActive: 200, CommitCount: 1, TestRuns: 3, TestFails: 1, LastUpsertAt: 150,
	}
	if _, err := store.Commit(ctx, mlstore.Write{Task: &task, TaskUpsert: []byte(`{"id":"agent-0123456789abcdef"}`)}); err != nil {
		t.Fatalf("commit task: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// ---- reopen: task counters survive ----
	db2, store2 := openMLStore(t, dir)
	defer db2.Close(ctx)
	got, ok, err := store2.LoadTask(ctx, task.TaskID)
	if err != nil || !ok {
		t.Fatalf("LoadTask after reopen: ok=%v err=%v", ok, err)
	}
	if got.CommitCount != 1 || got.TestRuns != 3 || got.TestFails != 1 || got.Phase != "testing" ||
		got.Files["aaaaaaaaaaaaaaaa.go"] != 2 || got.StartedAt != 100 || got.LastActive != 200 || got.LastUpsertAt != 150 {
		t.Fatalf("task after reopen = %+v, want the counters written before it", got)
	}
	recs, err := store2.ReadBatch(ctx, 0, 100)
	if err != nil || len(recs) != 4 {
		t.Fatalf("outbox after reopen = %d records (err %v), want 3 events + 1 task upsert", len(recs), err)
	}
	if recs[0].Table != mlstore.TableEvents || recs[0].RowID != fmt.Sprint(first[0]) ||
		string(recs[0].Body) != fmt.Sprintf(`{"id":%d,"kind":"agent.tool"}`, first[0]) {
		t.Errorf("first record = %+v, want event row_id == body id == seq %d", recs[0], first[0])
	}
	if recs[3].Table != mlstore.TableTasks || recs[3].Op != mlstore.OpUpsert || recs[3].RowID != task.TaskID {
		t.Errorf("last record = %+v, want the task upsert", recs[3])
	}

	// ---- purge, then seq is never reused ----
	if err := store2.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n, _ := store2.Pending(ctx); n != 0 {
		t.Fatalf("pending after purge = %d", n)
	}
	if _, ok, _ := store2.LoadTask(ctx, task.TaskID); ok {
		t.Fatal("task survived a purge")
	}
	after := commitEvents(t, store2, 1)
	maxBefore := recs[len(recs)-1].Seq
	if after[0] <= maxBefore {
		t.Fatalf("seq after purge = %d, want > %d (a reused seq is de-duplicated by fleet against an older event)", after[0], maxBefore)
	}

	// DeleteThrough is the shipper's cursor advance.
	more := commitEvents(t, store2, 2)
	if n, err := store2.DeleteThrough(ctx, more[0]); err != nil || n != 2 {
		t.Fatalf("DeleteThrough = %d (err %v), want 2", n, err)
	}
	left, _ := store2.ReadBatch(ctx, 0, 10)
	if len(left) != 1 || left[0].Seq != more[1] {
		t.Fatalf("after DeleteThrough = %+v, want only seq %d", left, more[1])
	}
}
