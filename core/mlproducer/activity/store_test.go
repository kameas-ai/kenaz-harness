package activity

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// store_test.go drives REAL sqlite files only (CLAUDE.md blind spots #2
// and #3): fresh files, reopened files, a file the engine created first,
// and two processes' worth of connections writing at once.

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// rawDB opens path the way another process (the engine) would.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func sampleWrite(ts int64, task string) Write {
	return Write{
		Source: "harness.agent",
		Events: []Event{
			{Kind: "file", TS: ts, Payload: map[string]any{"task": task, "path": "0123456789abcdef.go", "file": "0123456789abcdef.go"}},
			{Kind: "phase_change", TS: ts, Payload: map[string]any{"task": task, "phase": "coding"}},
		},
		Task: &Task{ID: task, RepoRoot: "fedcba9876543210", Branch: "xxxx", Phase: "coding",
			Files: map[string]int{"0123456789abcdef.go": 1}, StartedAt: ts, LastActive: ts},
	}
}

func TestPath(t *testing.T) {
	got := Path("/data/dir")
	if got != filepath.Join("/data/dir", "ml", "activity.db") {
		t.Fatalf("Path = %q", got)
	}
}

func TestOpen_FreshFileSchemaWALAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	s := openAt(t, path)
	ctx := context.Background()
	if v, err := s.SchemaVersion(ctx); err != nil || v != SchemaVersion {
		t.Fatalf("schema version = %d, %v; want %d", v, err, SchemaVersion)
	}
	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v; want wal", mode, err)
	}
	var busy int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil || busy != 5000 {
		t.Fatalf("busy_timeout = %d, %v; want 5000", busy, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", di.Mode().Perm())
	}
	// The harness creates events, tasks, ml_predictions, ml_events — and
	// NOT ml_cursor (the engine's).
	for _, tbl := range []string{"events", "tasks", "ml_predictions", "ml_events", "harness_activity_schema"} {
		if count(t, s.db, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl) != 1 {
			t.Errorf("table %s missing", tbl)
		}
	}
	if count(t, s.db, `SELECT COUNT(*) FROM sqlite_master WHERE name='ml_cursor'`) != 0 {
		t.Error("ml_cursor created by the harness; the engine owns it")
	}
	for _, idx := range []string{"idx_events_user_id", "idx_events_user_ts", "idx_tasks_user_completed",
		"idx_tasks_user_last_active", "idx_ml_predictions_user_model_created", "idx_ml_events_ts"} {
		if count(t, s.db, `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, idx) != 1 {
			t.Errorf("index %s missing", idx)
		}
	}
}

func TestCommit_RowsInFleetShape(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	ids, err := s.Commit(ctx, sampleWrite(1000, "agent-aaaaaaaaaaaaaaaa"))
	if err != nil || len(ids) != 2 {
		t.Fatalf("Commit = %v, %v", ids, err)
	}
	var kind, source, payload, user, node, producer string
	var ts, dev, recv int64
	if err := s.db.QueryRow(`SELECT kind, source, payload, ts, user_id, node_id, device_event_id, received_at, producer FROM events WHERE id = ?`, ids[0]).
		Scan(&kind, &source, &payload, &ts, &user, &node, &dev, &recv, &producer); err != nil {
		t.Fatal(err)
	}
	if kind != "file" || source != "harness.agent" || ts != 1000 || dev != ids[0] || producer != "harness" || user != "" || node != "" || recv <= 0 {
		t.Errorf("row = %s %s %d %q %q dev=%d recv=%d %s", kind, source, ts, user, node, dev, recv, producer)
	}
	if payload != `{"file":"0123456789abcdef.go","path":"0123456789abcdef.go","task":"agent-aaaaaaaaaaaaaaaa"}` {
		t.Errorf("payload = %s", payload)
	}
	got, ok, err := s.LoadTask(ctx, "agent-aaaaaaaaaaaaaaaa")
	if err != nil || !ok || got.Phase != "coding" || got.Files["0123456789abcdef.go"] != 1 || got.Branch != "xxxx" || got.CompletedAt != 0 {
		t.Fatalf("LoadTask = %+v %v %v", got, ok, err)
	}
	if count(t, s.db, `SELECT COUNT(*) FROM tasks WHERE completed_at IS NULL`) != 1 {
		t.Error("open task's completed_at is not NULL")
	}
	// Upsert in place.
	w := sampleWrite(2000, "agent-aaaaaaaaaaaaaaaa")
	w.Task.CompletedAt, w.Task.CommitCount = 2000, 3
	if _, err := s.Commit(ctx, w); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s.db, `SELECT COUNT(*) FROM tasks`); n != 1 {
		t.Fatalf("tasks = %d, want 1 (upsert)", n)
	}
	got, _, _ = s.LoadTask(ctx, "agent-aaaaaaaaaaaaaaaa")
	if got.CompletedAt != 2000 || got.CommitCount != 3 {
		t.Errorf("task after upsert = %+v", got)
	}
	// The producer CHECK is Fleet's.
	if _, err := s.db.Exec(`INSERT INTO events (kind, source, payload, ts, user_id, node_id, device_event_id, producer) VALUES ('x','y','{}',1,'','',99,'other')`); err == nil {
		t.Error("producer CHECK accepted 'other'")
	}
	// The dedup key is Fleet's.
	if _, err := s.db.Exec(`INSERT INTO events (kind, source, payload, ts, user_id, node_id, device_event_id, producer) VALUES ('x','y','{}',1000,'','',?, 'harness')`, ids[0]); err == nil {
		t.Error("events_dedup_key accepted a duplicate")
	}
}

// Reopening an existing file keeps every row and applies nothing twice.
func TestOpen_ReopenExistingFile(t *testing.T) {
	path := Path(t.TempDir())
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.Commit(context.Background(), sampleWrite(1000, "agent-bbbbbbbbbbbbbbbb"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2 := openAt(t, path)
	if n := count(t, s2.db, `SELECT COUNT(*) FROM events`); n != 2 {
		t.Fatalf("events after reopen = %d, want 2", n)
	}
	if n := count(t, s2.db, `SELECT COUNT(*) FROM harness_activity_schema`); n != SchemaVersion {
		t.Fatalf("ledger rows = %d, want %d (no re-apply)", n, SchemaVersion)
	}
	more, err := s2.Commit(context.Background(), sampleWrite(3000, "agent-bbbbbbbbbbbbbbbb"))
	if err != nil || more[0] <= ids[1] {
		t.Fatalf("ids after reopen = %v (before %v), %v: ids must keep rising", more, ids, err)
	}
}

// Upgrade shape: the engine may create the file first (its ml_cursor, a
// cursor row, WAL), before any harness ran. The harness must adopt it
// without touching the engine's table.
func TestOpen_AdoptsEngineCreatedFile(t *testing.T) {
	path := Path(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	eng := rawDB(t, path)
	if _, err := eng.Exec(`CREATE TABLE ml_cursor (stream TEXT PRIMARY KEY, last_event_id INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Exec(`INSERT INTO ml_cursor (stream, last_event_id, updated_at) VALUES ('harness', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	s := openAt(t, path)
	if v, _ := s.SchemaVersion(context.Background()); v != SchemaVersion {
		t.Fatalf("schema = %d", v)
	}
	if n := count(t, s.db, `SELECT COUNT(*) FROM ml_cursor WHERE stream='harness'`); n != 1 {
		t.Fatalf("engine cursor row lost: %d", n)
	}
	if _, err := s.Commit(context.Background(), sampleWrite(1, "agent-cccccccccccccccc")); err != nil {
		t.Fatal(err)
	}
	// The engine reads what the harness wrote.
	if n := count(t, eng, `SELECT COUNT(*) FROM events WHERE producer='harness'`); n != 2 {
		t.Fatalf("engine sees %d events", n)
	}
}

// A ledger NEWER than this build (a downgrade) still opens and writes.
func TestOpen_NewerLedgerStillOpens(t *testing.T) {
	path := Path(t.TempDir())
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO harness_activity_schema (version, applied_at) VALUES (?, 1)`, SchemaVersion+5); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s2 := openAt(t, path)
	if _, err := s2.Commit(context.Background(), sampleWrite(1, "agent-dddddddddddddddd")); err != nil {
		t.Fatalf("Commit on a newer-ledger file: %v", err)
	}
}

func TestPruneBefore_AndPruner(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour).UnixMilli()
	mid := now.Add(-40 * 24 * time.Hour).UnixMilli()
	fresh := now.Add(-time.Hour).UnixMilli()
	for i, ts := range []int64{old, mid, fresh} {
		if _, err := s.Commit(ctx, sampleWrite(ts, fmt.Sprintf("agent-%016d", i))); err != nil {
			t.Fatal(err)
		}
	}
	// The engine's rows are not the pruner's.
	if _, err := s.db.Exec(`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES ('m','r',0.5,1)`); err != nil {
		t.Fatal(err)
	}
	days := 0 // unset → 90
	p := NewPruner(s, func() (int, error) { return days, nil }, func() time.Time { return now }, nil)
	ev, tk, err := p.PruneOnce(ctx)
	if err != nil || ev != 2 || tk != 1 {
		t.Fatalf("90-day prune = %d events %d tasks, %v; want 2, 1", ev, tk, err)
	}
	// The dial is read every pass.
	days = 30
	ev, tk, err = p.PruneOnce(ctx)
	if err != nil || ev != 2 || tk != 1 {
		t.Fatalf("30-day prune = %d events %d tasks, %v; want 2, 1", ev, tk, err)
	}
	st, _ := s.Stats(ctx)
	if st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("left = %+v, want the fresh write only", st)
	}
	if n := count(t, s.db, `SELECT COUNT(*) FROM ml_predictions`); n != 1 {
		t.Errorf("pruner touched ml_predictions (%d left)", n)
	}
	if EffectiveRetentionDays(0) != 90 || EffectiveRetentionDays(-1) != 90 || EffectiveRetentionDays(99999) != MaxRetentionDays || EffectiveRetentionDays(7) != 7 {
		t.Error("EffectiveRetentionDays")
	}
	p.Start()
	p.Stop()
	p.Stop()
}

func TestDeleteAll_WipesHarnessAndEngineRowsKeepsCursorAndIDs(t *testing.T) {
	path := Path(t.TempDir())
	s := openAt(t, path)
	ctx := context.Background()
	ids, _ := s.Commit(ctx, sampleWrite(1, "agent-eeeeeeeeeeeeeeee"))
	eng := rawDB(t, path)
	if _, err := eng.Exec(`CREATE TABLE IF NOT EXISTS ml_cursor (stream TEXT PRIMARY KEY, last_event_id INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Exec(`INSERT INTO ml_cursor VALUES ('harness', ?, 1)`, ids[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Exec(`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES ('m','r',0.5,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Exec(`INSERT INTO ml_events (kind, endpoint, routing, latency_ms, ts) VALUES ('k','e','local',3,1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"events", "tasks", "ml_predictions", "ml_events"} {
		if n := count(t, s.db, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Errorf("%s has %d rows after DeleteAll", tbl, n)
		}
	}
	if n := count(t, s.db, `SELECT COUNT(*) FROM ml_cursor`); n != 1 {
		t.Error("DeleteAll removed the engine's cursor")
	}
	next, _ := s.Commit(ctx, sampleWrite(2, "agent-eeeeeeeeeeeeeeee"))
	if next[0] <= ids[1] {
		t.Fatalf("id reused after delete: %d <= %d (the engine cursor would skip it)", next[0], ids[1])
	}
}

// Two connections (the harness and the engine process) write the same
// file at once: busy_timeout + IMMEDIATE transactions mean no SQLITE_BUSY.
func TestConcurrentWriters_NoBusyErrors(t *testing.T) {
	path := Path(t.TempDir())
	s := openAt(t, path)
	other := openAt(t, path) // a second process's store
	eng := rawDB(t, path)
	ctx := context.Background()
	const n = 60
	var wg sync.WaitGroup
	errs := make(chan error, 4*n)
	wg.Add(4)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := s.Commit(ctx, sampleWrite(int64(i), "agent-1111111111111111")); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := other.Commit(ctx, sampleWrite(int64(10000+i), "agent-2222222222222222")); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if _, err := eng.Exec(`INSERT INTO ml_predictions (model, result, confidence, created_at) VALUES ('m','r',0.5,?)`, i); err != nil {
				errs <- err
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < n/6; i++ {
			if _, _, err := other.PruneBefore(ctx, 5); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write failed: %v", err)
	}
	if got := count(t, eng, `SELECT COUNT(*) FROM events WHERE ts >= 10000`); got != 2*n {
		t.Errorf("second writer's events = %d, want %d", got, 2*n)
	}
	if got := count(t, eng, `SELECT COUNT(*) FROM ml_predictions`); got != n {
		t.Errorf("engine predictions = %d, want %d", got, n)
	}
}

// DDL() is the kenaz-ml handoff; it must be what Open applies.
func TestDDL_MatchesWhatOpenApplies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.db")
	db := rawDB(t, path)
	for _, stmt := range DDL() {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("DDL statement fails: %v\n%s", err, stmt)
		}
	}
	s := openAt(t, Path(t.TempDir()))
	schema := func(db *sql.DB) map[string]string {
		rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var n, q string
			_ = rows.Scan(&n, &q)
			m[n] = q
		}
		return m
	}
	a, b := schema(db), schema(s.db)
	if len(a) != len(b) {
		t.Fatalf("DDL() objects %d != Open objects %d", len(a), len(b))
	}
	for k, v := range a {
		if b[k] != v {
			t.Errorf("%s differs:\nDDL():%s\nOpen: %s", k, v, b[k])
		}
	}
}

// A pass whose retention read fails deletes nothing (review finding 1).
func TestPruner_ReadErrorSkipsPass(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	if _, err := s.Commit(ctx, sampleWrite(now.Add(-200*24*time.Hour).UnixMilli(), "agent-ffffffffffffffff")); err != nil {
		t.Fatal(err)
	}
	p := NewPruner(s, func() (int, error) { return 0, fmt.Errorf("settings: parse: boom") }, func() time.Time { return now }, nil)
	if ev, tk, err := p.PruneOnce(ctx); err != ErrRetentionUnreadable || ev != 0 || tk != 0 {
		t.Fatalf("PruneOnce = %d/%d, %v; want skipped with ErrRetentionUnreadable", ev, tk, err)
	}
	p.pass() // logs the skip; must not delete either
	if st, _ := s.Stats(ctx); st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("rows after skipped passes = %+v", st)
	}
}

// Review finding 3 (#429): N processes' first opens of the same fresh
// file at once all succeed — busy_timeout is set before journal_mode, the
// migration re-checks the ledger inside its write transaction, and Open
// retries a transient failure. Run under -race -count=20.
func TestOpen_ConcurrentFirstOpensAllSucceed(t *testing.T) {
	path := Path(t.TempDir())
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, n)
	stores := make(chan *Store, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			s, err := Open(path)
			if err != nil {
				errs <- err
				return
			}
			stores <- s
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	close(stores)
	for err := range errs {
		t.Errorf("concurrent first open failed: %v", err)
	}
	for s := range stores {
		if v, err := s.SchemaVersion(context.Background()); err != nil || v != SchemaVersion {
			t.Errorf("schema = %d, %v", v, err)
		}
		_ = s.Close()
	}
	db := rawDB(t, path)
	if got := count(t, db, `SELECT COUNT(*) FROM harness_activity_schema`); got != SchemaVersion {
		t.Fatalf("ledger rows = %d, want %d (a migration applied twice)", got, SchemaVersion)
	}
}
