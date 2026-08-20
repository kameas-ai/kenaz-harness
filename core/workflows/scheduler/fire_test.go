package scheduler

// White-box tests for fireSync's nil-dispatcher honesty fix (UNIT-1,
// FR-001, AC-001). Package scheduler (not scheduler_test) because fireSync
// is unexported — RunNow's exported surface only drives the
// scheduled=false path, and AC-001 is specifically about the
// scheduled=true cron-tick path.
//
// CLAUDE.md blind spot #2: persistence assertions must drive real sqlite,
// never a fake Storage. AC-001's subject is the workflow_schedules table
// itself, so these tests read it back with a raw SELECT rather than
// through the Storage interface or the returned RunSummary (spec.md §10
// rule 1 / tasks.md "the test must assert on the table, not the returned
// RunSummary").

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/workflows"

	_ "modernc.org/sqlite"
)

// openRealDB opens a real sqlite-backed storage.DB in a temp dir, matching
// the canonical pattern in core/workflows/storage_test.go.
func openRealDB(t *testing.T) storage.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

const fireTestYAML = `
id: fire-test-wf
name: "Fire test workflow"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["hello"]
`

// seedWorkflow inserts a real workflows row and returns its ID.
// workflow_schedules.workflow_id carries an FK (ON DELETE CASCADE) to
// workflows.id (sessions/0321-workflow-schedules), so a bare Upsert
// against an empty workflows table would fail its foreign-key check.
func seedWorkflow(t *testing.T, db storage.DB) string {
	t.Helper()
	w, err := workflows.LoadYAML([]byte(fireTestYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	store := workflows.NewSQLiteStore(db)
	saved, err := store.Save(context.Background(), w)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	return saved.ID
}

// scheduleRow is the raw workflow_schedules projection this test cares
// about — deliberately NOT scheduler.StoredSchedule, so a bug in
// SQLiteStorage's own Scan logic can't hide a bug in fireSync (or vice
// versa) behind a shared struct.
type scheduleRow struct {
	lastFiredAt *int64
	lastRunID   *string
}

func selectScheduleRow(t *testing.T, db storage.DB, workflowID string) scheduleRow {
	t.Helper()
	rows, err := db.Reader().Query(context.Background(),
		`SELECT last_fired_at, last_run_id FROM workflow_schedules WHERE workflow_id = ?`,
		workflowID,
	)
	if err != nil {
		t.Fatalf("SELECT workflow_schedules: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no workflow_schedules row for %q", workflowID)
	}
	var row scheduleRow
	if err := rows.Scan(&row.lastFiredAt, &row.lastRunID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	return row
}

// stubDispatcher is a race-safe fake Dispatcher (mutex + snapshot per
// CLAUDE.md's race-safe-fakes rule).
type stubDispatcher struct {
	mu    sync.Mutex
	calls int
	runID string
}

func (d *stubDispatcher) Dispatch(_ context.Context, _ string, _ bool) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	return d.runID, nil
}

func (d *stubDispatcher) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

// TestFireSync_ScheduledTick_NilDispatcher_RecordsFailureNotCompletion is
// AC-001. Before the UNIT-1 fix, s.dispatcher == nil took the `else`
// branch, left err nil, set summary.Status = "completed", and called
// SetLastFired — this test's SELECT read a non-nil last_fired_at /
// last_run_id and its error assertions failed. That before-state was
// captured for the commit body (see UNIT-1's mutation re-run below).
func TestFireSync_ScheduledTick_NilDispatcher_RecordsFailureNotCompletion(t *testing.T) {
	t.Parallel()
	db := openRealDB(t)
	workflowID := seedWorkflow(t, db)

	store := NewSQLiteStorage(db)
	ctx := context.Background()
	if err := store.Upsert(ctx, StoredSchedule{
		WorkflowID: workflowID,
		Cron:       "0 7 * * *",
		Timezone:   "UTC",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	s, err := New(ctx, Config{Store: store, Dispatcher: nil})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	before := selectScheduleRow(t, db, workflowID)
	if before.lastFiredAt != nil || before.lastRunID != nil {
		t.Fatalf("precondition: row already has last_fired_at/last_run_id set: %+v", before)
	}

	summary, fireErr := s.fireSync(ctx, workflowID, true /* scheduled */)

	if fireErr == nil {
		t.Fatal("fireSync returned nil error with no dispatcher wired")
	}
	if !errors.Is(fireErr, ErrNoDispatcherWired) {
		t.Errorf("fireSync error = %v, want ErrNoDispatcherWired", fireErr)
	}
	if summary.Status == "completed" {
		t.Errorf("summary.Status = %q, must not be completed", summary.Status)
	}
	if summary.Err == "" {
		t.Error("summary.Err is empty; must name the missing dispatcher")
	}

	// The durable claim: by SELECT against the table, not by re-reading
	// the in-memory RunSummary.
	after := selectScheduleRow(t, db, workflowID)
	if after.lastFiredAt != nil {
		t.Errorf("last_fired_at advanced to %v with no dispatcher wired", *after.lastFiredAt)
	}
	if after.lastRunID != nil {
		t.Errorf("last_run_id set to %v with no dispatcher wired", *after.lastRunID)
	}

	// A config error is not an attempted run — see AC-002 for the
	// symmetric RunNow assertion in scheduler_test.go.
	hist, err := s.History(ctx, workflowID, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("History = %d entries, want 0 (config error, not an attempted run)", len(hist))
	}
}

// TestFireSync_ScheduledTick_WithDispatcher_StillCompletesAndAdvances is
// the required mutation-guard pairing (tasks.md UNIT-1): "A run with a
// stub dispatcher still records completed and still advances
// SetLastFired. Without this the unit is satisfiable by failing
// everything."
func TestFireSync_ScheduledTick_WithDispatcher_StillCompletesAndAdvances(t *testing.T) {
	t.Parallel()
	db := openRealDB(t)
	workflowID := seedWorkflow(t, db)

	store := NewSQLiteStorage(db)
	ctx := context.Background()
	if err := store.Upsert(ctx, StoredSchedule{
		WorkflowID: workflowID,
		Cron:       "0 7 * * *",
		Timezone:   "UTC",
		Enabled:    true,
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	disp := &stubDispatcher{runID: "run-real-123"}
	s, err := New(ctx, Config{Store: store, Dispatcher: disp})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	summary, fireErr := s.fireSync(ctx, workflowID, true /* scheduled */)
	if fireErr != nil {
		t.Fatalf("fireSync: %v", fireErr)
	}
	if summary.Status != "completed" {
		t.Errorf("summary.Status = %q, want completed", summary.Status)
	}
	if disp.callCount() != 1 {
		t.Errorf("dispatcher.calls = %d, want 1", disp.callCount())
	}

	after := selectScheduleRow(t, db, workflowID)
	if after.lastFiredAt == nil {
		t.Fatal("last_fired_at did not advance with a real dispatcher wired")
	}
	if after.lastRunID == nil || *after.lastRunID != "run-real-123" {
		t.Errorf("last_run_id = %v, want run-real-123", after.lastRunID)
	}

	hist, err := s.History(ctx, workflowID, 10)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 1 {
		t.Errorf("History = %d entries, want 1", len(hist))
	}
}

// TestNoDispatcherWiredCommentRemoved is the tasks.md UNIT-1 regression
// guard: grep core/ for the retired mischaracterization of the
// nil-dispatcher branch as a test path (it was the only production state —
// a docstring lie on top of the manufactured-success bug) and assert zero
// hits. The banned phrase is assembled at runtime rather than written as a
// literal in this file, so this test's own source can't trip its own
// check.
func TestNoDispatcherWiredCommentRemoved(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	coreDir := filepath.Join(root, "core")
	if info, statErr := os.Stat(coreDir); statErr != nil || !info.IsDir() {
		t.Skipf("cannot locate core/ from test working directory (root=%s): %v", root, statErr)
	}

	banned := strings.Join([]string{"No dispatcher wired ", "(test path)"}, "")
	var hits []string
	walkErr := filepath.WalkDir(coreDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		if filepath.Base(path) == "fire_test.go" {
			return nil // this file's own comments reference the banned phrase
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), banned) {
			hits = append(hits, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk core/: %v", walkErr)
	}
	if len(hits) != 0 {
		t.Errorf("found retired comment %q in: %v", banned, hits)
	}
}
