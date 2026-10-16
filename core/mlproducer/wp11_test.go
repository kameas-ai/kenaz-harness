package mlproducer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/activity"
)

// wp11_test.go — spec §12 A-16: always record locally; ship only what the
// gate allowed AT RECORD TIME. Both stores are real sqlite files: data.db
// through storage.Open (the ship outbox) and <dir>/ml/activity.db (the
// local store), read back by a separate connection the way the engine
// reads it.

type localHarness struct {
	*harness
	local *activity.Store
	path  string
}

func newLocalHarness(t *testing.T) *localHarness {
	t.Helper()
	h := newHarness(t)
	h.rec.Close()
	lh := &localHarness{harness: h, path: activity.Path(h.dir)}
	lh.reopen(t)
	return lh
}

// reopen closes the recorder and the local store and opens both again on
// the same files (a restart).
func (lh *localHarness) reopen(t *testing.T) {
	t.Helper()
	if lh.rec != nil {
		lh.rec.Close()
	}
	if lh.local != nil {
		_ = lh.local.Close()
	}
	s, err := activity.Open(lh.path)
	if err != nil {
		t.Fatal(err)
	}
	lh.local = s
	lh.rec = NewRecorder(Config{
		Store: lh.store, Local: s, Hasher: NewHasher(lh.dir),
		Gate:      KindGateFunc{Record: lh.gate.Load, AIUsage: lh.ai.Load},
		Workspace: func() string { return workspace }, Now: lh.clock.Now, SweepInterval: -1,
	})
	rec := lh.rec
	// The recorder drains before its store closes.
	t.Cleanup(func() { rec.Close(); _ = s.Close() })
}

type localEvent struct {
	ID      int64
	Kind    string
	TS      int64
	Payload map[string]any
}

// localEvents reads the local events table through its own connection.
func (lh *localHarness) localEvents(t *testing.T) []localEvent {
	t.Helper()
	lh.flush()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(lh.path)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, kind, ts, payload FROM events WHERE producer = 'harness' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []localEvent
	for rows.Next() {
		var e localEvent
		var p string
		if err := rows.Scan(&e.ID, &e.Kind, &e.TS, &p); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(p), &e.Payload); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func kindsOf[T any](xs []T, kind func(T) string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		out = append(out, kind(x))
	}
	sort.Strings(out)
	return out
}

func localKinds(evs []localEvent) []string {
	return kindsOf(evs, func(e localEvent) string { return e.Kind })
}

func outboxEventKinds(recs []decoded) []string {
	var evs []decoded
	for _, r := range recs {
		if r.Table == "events" {
			evs = append(evs, r)
		}
	}
	return kindsOf(evs, func(d decoded) string { return d.event.Kind })
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const writeArgs = `{"path":"src/main.go","content":"x"}`

// Gate closed: the local store records, the outbox does not.
func TestWP11_GateClosed_RecordsLocallyOnly(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	lh.gate.Store(false)
	ctx := context.Background()
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashOK)
	lh.rec.TurnEnded(ctx, "s1", "completed", 2, 2, time.Second)

	if recs := lh.outbox(); len(recs) != 0 {
		t.Fatalf("outbox has %d record(s) with the gate closed", len(recs))
	}
	got := localKinds(lh.localEvents(t))
	want := []string{KindTurn, KindFile, KindPhaseChange, KindPhaseChange, KindTerminal}
	sort.Strings(want)
	if !eq(got, want) {
		t.Fatalf("local kinds = %v, want %v", got, want)
	}
	task, ok, err := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if err != nil || !ok {
		t.Fatalf("local task: %v %v", ok, err)
	}
	if task.TestRuns != 1 || len(task.Files) != 1 || task.RepoRoot != lh.hash(workspace) || task.Phase != string(PhaseTesting) {
		t.Errorf("local task = %+v", task)
	}
	// Minimised form: the raw path never reaches the local store.
	for _, e := range lh.localEvents(t) {
		b, _ := json.Marshal(e.Payload)
		if strings.Contains(string(b), "src/main.go") || strings.Contains(string(b), workspace) {
			t.Errorf("local event %s carries a raw path: %s", e.Kind, b)
		}
	}
	if _, ok, _ := lh.store.LoadTask(ctx, lh.taskID("s1")); ok {
		t.Error("ship task state advanced while the gate was closed")
	}
}

// Opting in later never back-fills: the outbox holds only what was
// recorded after the gate opened, and the shipped task counters start
// from that moment.
func TestWP11_OptInLater_NoBackfill(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.gate.Store(false)
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, `{"stdout":"","stderr":"","exit_code":1,"truncated":false}`)
	lh.flush()
	lh.clock.advance(time.Minute)
	lh.gate.Store(true)
	lh.call(ctx, "s1", "kenaz__write_file", `{"path":"src/other.go","content":"y"}`, coreag.ToolOutcomeOK, `{"ok":true}`)

	recs := lh.outbox()
	if got := outboxEventKinds(recs); !eq(got, []string{KindFile, KindPhaseChange}) {
		t.Fatalf("outbox kinds = %v, want only the post-opt-in write's file + phase_change", got)
	}
	task := lastTask(t, recs)
	if len(task.Files) != 1 || task.TestRuns != 0 || task.TestFails != 0 {
		t.Errorf("shipped task = %+v: counters from the closed period leaked into the upsert", task)
	}
	if task.StartedAt != lh.clock.Now().UnixMilli() {
		t.Errorf("shipped task started_at = %d, want the opt-in moment %d", task.StartedAt, lh.clock.Now().UnixMilli())
	}
	// Locally the task carries everything.
	lt, _, _ := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if len(lt.Files) != 2 || lt.TestRuns != 1 || lt.TestFails != 1 {
		t.Errorf("local task = %+v, want both files and the failed test run", lt)
	}
	if n := len(lh.localEvents(t)); n != 6 {
		t.Errorf("local events = %d, want 6 (file, phase, terminal, phase, file, phase)", n)
	}
}

// A call observed while the gate was open but written after it closed
// does not ship (both ends of "record time" must hold).
func TestWP11_GateClosesBeforeWrite_NotShipped(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.rec.mu.Lock() // hold the worker
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.gate.Store(false)
	lh.rec.mu.Unlock()
	if recs := lh.outbox(); len(recs) != 0 {
		t.Fatalf("outbox = %d record(s); the gate was closed when the call was written", len(recs))
	}
	if n := len(lh.localEvents(t)); n != 2 {
		t.Fatalf("local events = %d, want 2", n)
	}
}

// Withdrawal / sign-out purges the outbox and the ship task state only.
func TestWP11_Purge_KeepsLocal(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	if len(lh.outbox()) == 0 {
		t.Fatal("nothing shipped with the gate open")
	}
	if err := lh.rec.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(lh.outbox()); n != 0 {
		t.Fatalf("outbox after purge = %d", n)
	}
	if n := len(lh.localEvents(t)); n != 2 {
		t.Fatalf("local events after purge = %d, want 2 (untouched)", n)
	}
	if _, ok, _ := lh.local.LoadTask(ctx, lh.taskID("s1")); !ok {
		t.Fatal("local task lost on purge")
	}
}

// "Delete local activity data" wipes the local store, the outbox and the
// ship task state; later activity starts afresh in both.
func TestWP11_DeleteLocal_WipesEverything(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashOK)
	if err := lh.rec.DeleteLocal(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(lh.outbox()); n != 0 {
		t.Fatalf("outbox after delete = %d", n)
	}
	if n := len(lh.localEvents(t)); n != 0 {
		t.Fatalf("local events after delete = %d", n)
	}
	st, _ := lh.local.Stats(ctx)
	if st.Tasks != 0 {
		t.Fatalf("local tasks after delete = %d", st.Tasks)
	}
	if _, ok, _ := lh.store.LoadTask(ctx, lh.taskID("s1")); ok {
		t.Fatal("ml_tasks row survived the delete")
	}
	lh.call(ctx, "s1", "kenaz__write_file", `{"path":"b.go","content":"x"}`, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.flush()
	lt, ok, _ := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if !ok || len(lt.Files) != 1 || lt.TestRuns != 0 {
		t.Fatalf("task after delete = %+v %v; want a fresh task (cache cleared too)", lt, ok)
	}
}

// Org exclusions govern only what leaves the device (A-16 item 4).
func TestWP11_Exclusions_ApplyToShipOnly(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.rec.SetExclusions(mustCompile(t, Exclusions{Commands: []string{"ssh"}}))
	lh.call(ctx, "s1", "kenaz__bash", `{"command":"ssh prod uptime"}`, coreag.ToolOutcomeOK, bashOK)
	if got := outboxEventKinds(lh.outbox()); !eq(got, []string{KindTool}) {
		t.Fatalf("outbox kinds = %v, want agent.tool only", got)
	}
	evs := lh.localEvents(t)
	if got := localKinds(evs); !eq(got, []string{KindTerminal}) {
		t.Fatalf("local kinds = %v, want the terminal the outbox did not get", got)
	}
	for _, e := range evs {
		if e.Kind == KindTerminal && e.Payload["cmd"] != "ssh prod" {
			t.Errorf("local terminal cmd = %v, want the two-token form", e.Payload["cmd"])
		}
	}
}

// Without ai_usage_allowed the Rev2Kinds stay off the outbox but are kept
// locally.
func TestWP11_Rev2Kinds_LocalWithoutAIUsage(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	lh.ai.Store(false)
	ctx := context.Background()
	lh.call(ctx, "s1", "kenaz__web_search", `{"query":"q"}`, coreag.ToolOutcomeOK, `{}`)
	lh.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	if got := outboxEventKinds(lh.outbox()); !eq(got, []string{KindPhaseChange}) {
		t.Fatalf("outbox kinds = %v, want phase_change only", got)
	}
	if got := localKinds(lh.localEvents(t)); !eq(got, []string{KindTool, KindTurn, KindPhaseChange}) {
		t.Fatalf("local kinds = %v", got)
	}
}

// A background job spawned with the gate closed and ending after it opened
// ships no follow-up (its spawn was never recorded for Fleet), but the
// local store gets the exit and the counters.
func TestWP11_BackgroundExit_SpawnedWhileClosed(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.gate.Store(false)
	lh.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./...","run_in_background":true}`, coreag.ToolOutcomeOK, spawnResult("bg-1"))
	lh.flush()
	lh.gate.Store(true)
	lh.rec.BackgroundEnded(ctx, "bg-1", 1)
	if got := outboxEventKinds(lh.outbox()); len(got) != 0 {
		t.Fatalf("outbox kinds = %v, want none", got)
	}
	var terms []localEvent
	for _, e := range lh.localEvents(t) {
		if e.Kind == KindTerminal {
			terms = append(terms, e)
		}
	}
	if len(terms) != 2 || terms[1].Payload["exit_code"] != float64(1) {
		t.Fatalf("local terminals = %+v, want spawn + exit 1", terms)
	}
	lt, _, _ := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if lt.TestRuns != 1 || lt.TestFails != 1 {
		t.Errorf("local counters = %d/%d, want 1/1", lt.TestRuns, lt.TestFails)
	}
}

// A restart reopens the same activity file and continues the task.
func TestWP11_RestartContinuesLocalTask(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.gate.Store(false)
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.flush()
	lh.reopen(t)
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.flush()
	lt, _, _ := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if n := lt.Files[lh.hash(workspace+"/src/main.go")+".go"]; n != 2 {
		t.Fatalf("files after restart = %v, want the same file counted twice", lt.Files)
	}
	evs := lh.localEvents(t)
	if len(evs) != 3 || evs[2].ID <= evs[1].ID {
		t.Fatalf("events after restart = %+v", evs)
	}
	// Session delete completes it locally.
	_ = lh.rec.SessionDeleted(ctx, "s1")
	lh.flush()
	lt, _, _ = lh.local.LoadTask(ctx, lh.taskID("s1"))
	if lt.CompletedAt == 0 {
		t.Error("local task not completed by the session delete")
	}
}

// The idle sweep completes local tasks too.
func TestWP11_IdleSweepCompletesLocalTask(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.gate.Store(false)
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	lh.flush()
	lh.clock.advance(8 * 24 * time.Hour)
	if err := lh.rec.SweepIdle(ctx); err != nil {
		t.Fatal(err)
	}
	lt, _, _ := lh.local.LoadTask(ctx, lh.taskID("s1"))
	if lt.CompletedAt == 0 || lt.CompletedAt != lt.LastActive {
		t.Fatalf("local task after sweep = %+v", lt)
	}
}

// Review finding 3 (#429): a local store that failed to open at boot is
// reopened lazily on a later record (throttled by ReopenEvery), instead of
// the process recording nothing locally until restart. The reopen runs in
// the background (nit 3), so the record that starts it is not written
// locally; the next one after it succeeds is.
func TestWP11_LocalStoreReopensLazily(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.Close()
	path := activity.Path(h.dir)
	var attempts atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	var opened atomic.Pointer[activity.Store]
	h.rec = NewRecorder(Config{
		Store: h.store, Hasher: NewHasher(h.dir),
		Gate: KindGateFunc{Record: func() bool { return false }},
		OpenLocal: func() (*activity.Store, error) {
			attempts.Add(1)
			if fail.Load() {
				return nil, fmt.Errorf("activity: open: database is locked")
			}
			s, err := activity.Open(path)
			opened.Store(s)
			return s, err
		},
		ReopenEvery: time.Minute,
		Workspace:   func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1,
	})
	rec := h.rec
	t.Cleanup(func() {
		rec.Close()
		if s := opened.Load(); s != nil {
			_ = s.Close()
		}
	})
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.flush()
	waitFor(t, func() bool { return attempts.Load() >= 1 && !rec.reopening.Load() }, "reopen")
	if n := attempts.Load(); n != 1 || h.rec.LocalStore() != nil {
		t.Fatalf("attempts = %d (want 1: throttled), store = %v", n, h.rec.LocalStore())
	}
	fail.Store(false)
	h.clock.advance(2 * time.Minute)
	h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.flush()
	waitFor(t, func() bool { return h.rec.LocalStore() != nil }, "reopen")
	if n := attempts.Load(); n != 2 {
		t.Fatalf("attempts = %d, want 2", n)
	}
	h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.flush()
	st, err := h.rec.LocalStore().Stats(ctx)
	if err != nil || st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("local after reopen = %+v, %v; want the post-reopen call's file + phase_change", st, err)
	}
}

// Review nit 3 (#429): a slow, failing reopen (activity.Open retries on a
// locked file) runs off the worker, one attempt at a time: a burst of
// records is never held up by it, and the ship view still records.
func TestWP11_SlowReopenNeverBlocksWorker(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.Close()
	var attempts, inFlight, maxInFlight atomic.Int32
	h.rec = NewRecorder(Config{
		Store: h.store, Hasher: NewHasher(h.dir),
		Gate: KindGateFunc{Record: h.gate.Load, AIUsage: h.ai.Load},
		OpenLocal: func() (*activity.Store, error) {
			attempts.Add(1)
			n := inFlight.Add(1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(time.Second)
			inFlight.Add(-1)
			return nil, fmt.Errorf("activity: open: database is locked")
		},
		ReopenEvery: time.Millisecond,
		Workspace:   func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1,
	})
	rec := h.rec
	t.Cleanup(rec.Close)
	h.gate.Store(true)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		h.clock.advance(time.Second) // past ReopenEvery: every record may retry
		start := time.Now()
		h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
		h.flush()
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("record %d took %v with a 1 s reopen in flight; the worker waited on OpenLocal", i, d)
		}
	}
	waitFor(t, func() bool { return attempts.Load() >= 1 }, "reopen")
	if m := maxInFlight.Load(); m != 1 {
		t.Fatalf("max concurrent OpenLocal = %d, want 1 (single in-flight guard)", m)
	}
	if rec.LocalStore() != nil {
		t.Fatal("a failing open installed a store")
	}
	if got := outboxEventKinds(h.outbox()); len(got) == 0 {
		t.Fatal("ship view recorded nothing while the local store was unavailable")
	}
}

// A background reopen that completes after Close closes its handle
// instead of installing it (the owner closed the store it held already).
func TestWP11_ReopenAfterClose_ClosesHandle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.Close()
	path := activity.Path(h.dir)
	release := make(chan struct{})
	var opened atomic.Pointer[activity.Store]
	rec := NewRecorder(Config{
		Store: h.store, Hasher: NewHasher(h.dir),
		Gate: KindGateFunc{Record: func() bool { return false }},
		OpenLocal: func() (*activity.Store, error) {
			<-release
			s, err := activity.Open(path)
			opened.Store(s)
			return s, err
		},
		Workspace: func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1,
	})
	h.rec = rec
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	h.flush()
	waitFor(t, rec.reopening.Load, "reopen")
	rec.Close()
	close(release)
	waitFor(t, func() bool { return !rec.reopening.Load() }, "reopen")
	s := opened.Load()
	if s == nil {
		t.Fatal("reopen never ran")
	}
	if rec.LocalStore() != nil {
		t.Fatal("a reopen finishing after Close installed its store")
	}
	if _, err := s.Stats(ctx); err == nil {
		_ = s.Close()
		t.Fatal("the late-opened handle was left open")
	}
}

// Review nit 1 (#429): after shutdown, DeleteLocal must not open a store
// handle (the owner closed the local store right after Close; a new one
// would leak). Boot open failed, so the recorder has no store and only
// OpenLocal.
func TestWP11_DeleteLocalAfterClose_OpensNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.Close()
	path := activity.Path(h.dir)
	var mu sync.Mutex
	var opened []*activity.Store
	rec := NewRecorder(Config{
		Store: h.store, Hasher: NewHasher(h.dir),
		Gate: KindGateFunc{Record: func() bool { return false }},
		OpenLocal: func() (*activity.Store, error) {
			s, err := activity.Open(path)
			if err == nil {
				mu.Lock()
				opened = append(opened, s)
				mu.Unlock()
			}
			return s, err
		},
		Workspace: func() string { return workspace }, Now: h.clock.Now, SweepInterval: -1,
	})
	rec.Close()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, s := range opened {
			_ = s.Close()
		}
	})
	err := rec.DeleteLocal(context.Background())
	if !errors.Is(err, ErrRecorderClosed) {
		t.Fatalf("DeleteLocal after Close = %v, want ErrRecorderClosed", err)
	}
	mu.Lock()
	n := len(opened)
	mu.Unlock()
	if n != 0 || rec.LocalStore() != nil {
		t.Fatalf("DeleteLocal after Close opened %d store handle(s) (LocalStore %v); want none", n, rec.LocalStore())
	}
}

// With the reopen in the background, DeleteLocal on a recorder whose boot
// open failed opens the store itself (not the throttled retry), so the
// file's earlier rows are really deleted, and keeps the handle for the
// owner to close.
func TestWP11_DeleteLocal_OpensStoreWhenNoneOpen(t *testing.T) {
	t.Parallel()
	lh := newLocalHarness(t)
	ctx := context.Background()
	lh.call(ctx, "s1", "kenaz__write_file", writeArgs, coreag.ToolOutcomeOK, `{"ok":true}`)
	if len(lh.localEvents(t)) == 0 {
		t.Fatal("setup: nothing recorded locally")
	}
	lh.rec.Close()
	_ = lh.local.Close()
	rec := NewRecorder(Config{
		Store: lh.store, Hasher: NewHasher(lh.dir),
		Gate:      KindGateFunc{Record: func() bool { return false }},
		OpenLocal: func() (*activity.Store, error) { return activity.Open(lh.path) },
		Workspace: func() string { return workspace }, Now: lh.clock.Now, SweepInterval: -1,
	})
	t.Cleanup(func() {
		rec.Close()
		if s := rec.LocalStore(); s != nil {
			_ = s.Close()
		}
	})
	if err := rec.DeleteLocal(ctx); err != nil {
		t.Fatal(err)
	}
	s := rec.LocalStore()
	if s == nil {
		t.Fatal("DeleteLocal did not install the store it opened")
	}
	if st, err := s.Stats(ctx); err != nil || st.Events != 0 || st.Tasks != 0 {
		t.Fatalf("after DeleteLocal: %+v, %v; want empty", st, err)
	}
}
