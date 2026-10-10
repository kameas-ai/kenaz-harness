package mlproducer

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer/mlstore"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// recorder_test.go drives the Recorder against REAL sqlite (storage.Open
// on a temp dir, the production migration set), never a memory fake:
// every assertion here is about what lands in, or survives in, the
// outbox and task tables (CLAUDE.md blind spot #2).

const workspace = "/Users/alice/acme-secret-repo"

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t     *testing.T
	dir   string
	db    storage.DB
	store *mlstore.Store
	rec   *Recorder
	gate  *atomic.Bool
	clock *fakeClock
}

func openDB(t *testing.T, dir string) (storage.DB, *mlstore.Store) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("storage Open: %v", err)
	}
	h, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		t.Fatal("storage.DB has no SQL()")
	}
	return db, mlstore.New(h.SQL())
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), gate: &atomic.Bool{},
		clock: &fakeClock{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}}
	h.gate.Store(true)
	h.db, h.store = openDB(t, h.dir)
	h.rec = h.newRecorder()
	t.Cleanup(func() {
		h.rec.Close()
		_ = h.db.Close(context.Background())
	})
	return h
}

func (h *harness) newRecorder() *Recorder {
	return NewRecorder(Config{
		Store:         h.store,
		Hasher:        NewHasher(h.dir),
		Gate:          GateFunc(h.gate.Load),
		Servers:       ServerClassifierFunc(func(s string) bool { return s == "my-private-server" }),
		Workspace:     func() string { return workspace },
		Now:           h.clock.Now,
		SweepInterval: -1,
	})
}

func (h *harness) hash(x string) string {
	v, err := NewHasher(h.dir).H(x)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func (h *harness) taskID(session string) string { return TaskIDPrefix + h.hash(session) }

func (h *harness) flush() {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.rec.Flush(ctx); err != nil {
		h.t.Fatalf("Flush: %v", err)
	}
}

func (h *harness) call(ctx context.Context, session, tool, args string, outcome coreag.ToolOutcome, result string) {
	h.rec.ToolCallCompleted(coreag.ToolCallRecord{
		Ctx: ctx, SessionID: session, ToolName: tool, Outcome: outcome,
		Duration: 25 * time.Millisecond, RawArgs: args, ResultContent: result,
	})
}

type decoded struct {
	mlstore.Record
	event eventBody
	task  taskBody
}

func (h *harness) outbox() []decoded {
	h.t.Helper()
	h.flush()
	recs, err := h.store.ReadBatch(context.Background(), 0, 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	out := make([]decoded, 0, len(recs))
	for _, r := range recs {
		d := decoded{Record: r}
		var err error
		if r.Table == mlstore.TableEvents {
			err = json.Unmarshal(r.Body, &d.event)
		} else {
			err = json.Unmarshal(r.Body, &d.task)
		}
		if err != nil {
			h.t.Fatalf("decode %s body %s: %v", r.Table, r.Body, err)
		}
		out = append(out, d)
	}
	return out
}

func events(recs []decoded, kind string) []decoded {
	var out []decoded
	for _, r := range recs {
		if r.Table == mlstore.TableEvents && r.event.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

func lastTask(t *testing.T, recs []decoded) taskBody {
	t.Helper()
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i].Table == mlstore.TableTasks {
			return recs[i].task
		}
	}
	t.Fatal("no task upsert in the outbox")
	return taskBody{}
}

const bashOK = `{"stdout":"","stderr":"","exit_code":0,"truncated":false}`
const bashFail = `{"stdout":"FAIL","stderr":"","exit_code":1,"truncated":false}`

// The §8.1 shape, at the recorder: 3 writes, a failing `go test`, a
// commit, a turn end.
func TestRecorder_AttendedSession_EndToEndShape(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	for _, f := range []string{"a.go", "b.go", "/Users/alice/acme-secret-repo/c_test.go"} {
		h.call(ctx, "s1", "kenaz__write_file", `{"path":"`+f+`","content":"package x"}`, coreag.ToolOutcomeOK, `{"bytes_written":9}`)
	}
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashFail)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m 'acme secret plan'"}`, coreag.ToolOutcomeOK, bashOK)
	h.rec.TurnEnded(ctx, "s1", "completed", 4, 5, 3*time.Second)
	recs := h.outbox()

	id := h.taskID("s1")
	if n := len(events(recs, KindFile)); n != 3 {
		t.Errorf("file = %d, want 3", n)
	}
	if n := len(events(recs, KindTerminal)); n != 2 {
		t.Errorf("terminal = %d, want 2 (go test + git commit)", n)
	}
	if n := len(events(recs, KindTool)); n != 0 {
		t.Errorf("agent.tool = %d, want 0 (every call has an engine kind)", n)
	}
	if n := len(events(recs, KindCommit)); n != 1 {
		t.Errorf("agent.commit = %d, want 1", n)
	}
	if n := len(events(recs, KindTurn)); n != 1 {
		t.Errorf("agent.turn = %d, want 1", n)
	}
	phases := events(recs, KindPhaseChange)
	if len(phases) != 2 || phases[0].event.Payload["phase"] != "coding" || phases[1].event.Payload["phase"] != "testing" {
		t.Errorf("phases = %+v, want coding then testing", phases)
	}
	for _, r := range recs {
		if r.Table != mlstore.TableEvents {
			continue
		}
		if r.event.Payload["task"] != id {
			t.Errorf("event %s task = %v, want %s", r.event.Kind, r.event.Payload["task"], id)
		}
		if r.RowID == "" || r.RowID != jsonNum(r.event.ID) || r.event.Source != EventSource || PayloadKeys(r.event.Kind) == nil {
			t.Errorf("event envelope wrong: row_id %q body %+v", r.RowID, r.event)
		}
		allowed := map[string]bool{}
		for _, k := range PayloadKeys(r.event.Kind) {
			allowed[k] = true
		}
		for k := range r.event.Payload {
			if !allowed[k] {
				t.Errorf("%s payload has key %q outside §3.2", r.event.Kind, k)
			}
		}
	}
	terms := events(recs, KindTerminal)
	if terms[0].event.Payload["cmd"] != "go test" || terms[0].event.Payload["exit_code"] != float64(1) {
		t.Errorf("go test event payload = %v", terms[0].event.Payload)
	}
	if terms[1].event.Payload["cmd"] != "git commit" || terms[1].event.Payload["exit_code"] != float64(0) {
		t.Errorf("git commit terminal payload = %v (the terminal event ships alongside commit)", terms[1].event.Payload)
	}
	files := events(recs, KindFile)
	if files[0].event.Payload["path"] != h.hash(workspace+"/a.go")+".go" || files[0].event.Payload["file"] != files[0].event.Payload["path"] {
		t.Errorf("file payload = %v, want path == file == h(abs)+.go", files[0].event.Payload)
	}
	turn := events(recs, KindTurn)[0].event.Payload
	if turn["outcome"] != "completed" || turn["model_calls"] != float64(4) || turn["tool_calls"] != float64(5) || turn["dur_ms"] != float64(3000) {
		t.Errorf("turn payload = %v", turn)
	}

	task := lastTask(t, recs)
	if task.ID != id || task.RepoRoot != h.hash(workspace) || task.Branch != "" {
		t.Errorf("task identity = %+v", task)
	}
	// The (throttled) upsert carries the first call's state; the live
	// counters are in ml_tasks until the next upsert / completion.
	row, _, _ := h.store.LoadTask(ctx, id)
	if row.CommitCount != 1 || row.TestRuns != 1 || row.TestFails != 1 || len(row.Files) != 3 || row.Phase != "testing" {
		t.Errorf("task counters = %+v, want 1 commit, 1 run, 1 fail, 3 files, phase testing", row)
	}
	// Exactly one upsert: the first; the rest sit inside the 60 s throttle.
	var upserts int
	for _, r := range recs {
		if r.Table == mlstore.TableTasks {
			upserts++
		}
	}
	if upserts != 1 {
		t.Errorf("task upserts = %d, want 1 (throttled)", upserts)
	}

	// Nothing raw leaves: no path, no full command, no session id.
	for _, r := range recs {
		body := string(r.Body)
		for _, leak := range []string{"alice", "acme", "secret", "./...", "-m", "s1\"", "package x"} {
			if strings.Contains(body, leak) {
				t.Errorf("record body leaks %q: %s", leak, body)
			}
		}
	}
}

func jsonNum(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestRecorder_ScheduledChatRecordsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := runposture.Unattended(context.Background())
	h.call(ctx, "sched-1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	h.rec.TurnEnded(ctx, "sched-1", "completed", 1, 1, time.Second)
	if recs := h.outbox(); len(recs) != 0 {
		t.Fatalf("a scheduled chat recorded %d records", len(recs))
	}
	if _, ok, _ := h.store.LoadTask(context.Background(), h.taskID("sched-1")); ok {
		t.Fatal("a scheduled chat created a task")
	}
}

func TestRecorder_SubagentOfAttended_LandsOnRootTask(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.call(context.Background(), "root", "kenaz__read_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	h.rec.LinkChild("child", "root")
	child := runposture.Unattended(context.Background())
	h.call(child, "child", "kenaz__edit_file", `{"path":"b.go"}`, coreag.ToolOutcomeOK, "{}")
	h.rec.TurnEnded(child, "child", "completed", 1, 0, time.Second)
	recs := h.outbox()
	rootID := h.taskID("root")
	for _, r := range recs {
		if r.Table == mlstore.TableEvents && r.event.Payload["task"] != rootID {
			t.Errorf("child event on task %v, want root %s", r.event.Payload["task"], rootID)
		}
	}
	if n := len(events(recs, KindTool)) + len(events(recs, KindFile)); n != 2 {
		t.Fatalf("tool events = %d, want root's + child's", n)
	}
	if n := len(events(recs, KindTurn)); n != 1 {
		t.Errorf("child turn not emitted on the root task")
	}
	// A child's tool-less turn does not idle the root.
	for _, p := range events(recs, KindPhaseChange) {
		if p.event.Payload["phase"] == "idle" {
			t.Error("a child turn idled the root task")
		}
	}
	if _, ok, _ := h.store.LoadTask(context.Background(), h.taskID("child")); ok {
		t.Error("the child got a task of its own")
	}
	// The child's processes carry the ROOT's session marker.
	env := h.rec.ProcessEnv("child")
	if len(env) != 2 || env[1] != "KENAZ_SESSION="+h.hash("root") {
		t.Errorf("child ProcessEnv = %v, want KENAZ_SESSION=h(root)", env)
	}
}

func TestRecorder_SubagentOfScheduledChat_RecordsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// Not linked: the spawner only links from an attended spawn ctx.
	child := runposture.Unattended(context.Background())
	h.call(child, "child-of-sched", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	h.rec.TurnEnded(child, "child-of-sched", "completed", 1, 1, time.Second)
	if recs := h.outbox(); len(recs) != 0 {
		t.Fatalf("a subagent of a scheduled chat recorded %d records", len(recs))
	}
}

func TestRecorder_NestedChildChainsToRoot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.rec.LinkChild("child", "root")
	h.rec.LinkChild("grandchild", "child")
	h.call(runposture.Unattended(context.Background()), "grandchild", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	recs := h.outbox()
	tools := events(recs, KindTool)
	if len(tools) != 1 || tools[0].event.Payload["task"] != h.taskID("root") {
		t.Fatalf("grandchild call = %+v, want it on the root task", tools)
	}
	if h.rec.ProcessEnv("grandchild")[1] != "KENAZ_SESSION="+h.hash("root") {
		t.Error("grandchild KENAZ_SESSION is not the root's")
	}
}

func TestRecorder_ClosedGateRecordsNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.gate.Store(false)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	h.rec.TurnEnded(ctx, "s1", "completed", 1, 1, time.Second)
	_ = h.rec.SessionDeleted(ctx, "s1")
	if recs := h.outbox(); len(recs) != 0 {
		t.Fatalf("closed gate recorded %d records", len(recs))
	}
	if _, ok, _ := h.store.LoadTask(ctx, h.taskID("s1")); ok {
		t.Fatal("closed gate wrote a task")
	}
	// A nil gate is closed too.
	rec := NewRecorder(Config{Store: h.store, Hasher: NewHasher(h.dir), SweepInterval: -1})
	rec.ToolCallCompleted(coreag.ToolCallRecord{Ctx: ctx, SessionID: "s2", ToolName: "kenaz__glob", Outcome: coreag.ToolOutcomeOK})
	_ = rec.Flush(ctx)
	rec.Close()
	if n, _ := h.store.Pending(ctx); n != 0 {
		t.Fatal("nil gate recorded")
	}
}

func TestRecorder_PurgeThenSeqNeverReused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	before := h.outbox()
	var maxSeq int64
	for _, r := range before {
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
	}
	if err := h.rec.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if n, _ := h.store.Pending(ctx); n != 0 {
		t.Fatalf("outbox after purge = %d", n)
	}
	if _, ok, _ := h.store.LoadTask(ctx, h.taskID("s1")); ok {
		t.Fatal("task survived the purge")
	}
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	after := h.outbox()
	if len(after) == 0 {
		t.Fatal("nothing recorded after purge")
	}
	for _, r := range after {
		if r.Seq <= maxSeq {
			t.Errorf("seq %d reused after purge (max before %d)", r.Seq, maxSeq)
		}
	}
	// The purge forgot the cached task: the new task starts fresh.
	if task := lastTask(t, after); len(task.Files) != 0 || task.StartedAt != h.clock.Now().UnixMilli() {
		t.Errorf("post-purge task carried pre-purge state: %+v", task)
	}
}

func TestRecorder_TaskCountersSurviveReopen(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, "{}")
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashFail)
	h.flush()
	h.rec.Close()
	if err := h.db.Close(ctx); err != nil {
		t.Fatal(err)
	}

	h.db, h.store = openDB(t, h.dir)
	h.rec = h.newRecorder()
	h.clock.advance(2 * time.Minute)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m x"}`, coreag.ToolOutcomeOK, bashOK)
	h.flush()
	got, ok, err := h.store.LoadTask(ctx, h.taskID("s1"))
	if err != nil || !ok {
		t.Fatalf("task after reopen: %v %v", ok, err)
	}
	if got.TestRuns != 1 || got.TestFails != 1 || got.CommitCount != 1 || got.Files[h.hash(workspace+"/a.go")+".go"] != 1 {
		t.Fatalf("counters after reopen = %+v, want the pre-reopen counts plus the commit", got)
	}
}

func TestRecorder_OutcomesAndBackgroundBash(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__read_file", `{"path":"a.go"}`, coreag.ToolOutcomeOK, `{"is_error":true,"error":"nope"}`)
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go"}`, coreag.ToolOutcomeDenied, "")
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./...","run_in_background":true}`, coreag.ToolOutcomeOK, `{"task_id":"t","status":"running"}`)
	h.call(ctx, "s1", "my-private-server__lookup", `{}`, coreag.ToolOutcomeCancelled, "")
	h.call(ctx, "s1", "kenaz__bash", `{"command":"ls"}`, coreag.ToolOutcomeCancelled, "")
	recs := h.outbox()
	tools := events(recs, KindTool)
	if len(tools) != 4 {
		t.Fatalf("agent.tool events = %d, want 4 (read error, denied write, custom MCP, cancelled bash)", len(tools))
	}
	if tools[0].event.Payload["outcome"] != "error" || tools[0].event.Payload["tool"] != "kenaz__read_file" {
		t.Errorf("fsbuiltins is_error result = %v, want agent.tool outcome error", tools[0].event.Payload)
	}
	if tools[1].event.Payload["outcome"] != "denied" || tools[1].event.Payload["tool"] != "kenaz__write_file" {
		t.Errorf("denied write = %v, want agent.tool outcome denied", tools[1].event.Payload)
	}
	if _, has := tools[1].event.Payload["path"]; has {
		t.Error("agent.tool carried a path (not in its A-10 key set)")
	}
	if tools[2].event.Payload["tool"] != "custom__"+h.hash("my-private-server")+"__lookup" || tools[2].event.Payload["outcome"] != "cancelled" {
		t.Errorf("custom MCP call = %v", tools[2].event.Payload)
	}
	if tools[3].event.Payload["tool"] != "kenaz__bash" || tools[3].event.Payload["outcome"] != "cancelled" {
		t.Errorf("cancelled bash = %v, want agent.tool outcome cancelled", tools[3].event.Payload)
	}
	terms := events(recs, KindTerminal)
	if len(terms) != 1 {
		t.Fatalf("terminal events = %d, want 1 (the background spawn)", len(terms))
	}
	if _, has := terms[0].event.Payload["exit_code"]; has {
		t.Error("background bash carried an exit_code")
	}
	if n := len(events(recs, KindFile)); n != 0 {
		t.Errorf("file events = %d, want 0 (the only write was denied)", n)
	}
	task, _, _ := h.store.LoadTask(ctx, h.taskID("s1"))
	if task.TestRuns != 1 || task.TestFails != 0 {
		t.Errorf("background test run counters = %d/%d, want 1/0", task.TestRuns, task.TestFails)
	}
	if len(task.Files) != 0 {
		t.Errorf("a denied write counted as a file edit: %v", task.Files)
	}
	// The denied write did not move the phase to coding.
	for _, p := range events(recs, KindPhaseChange) {
		if p.event.Payload["phase"] == "coding" {
			t.Error("a denied write changed the phase")
		}
	}
}

// Spec §12 A-10 amendment: bash's own gate refusals arrive as outcome ok
// with exit_code -1 and the explicit `refused` marker. They are agent.tool
// / denied, never terminal, and count toward nothing. A pre-run failure
// (`not_run`) is agent.tool / error. A real process that exits -1 is still
// a terminal event.
func TestRecorder_BashGateRefusalIsDeniedNotTerminal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	refusal := `{"stdout":"","stderr":"cedar policy denied: no","exit_code":-1,"truncated":false,"refused":true}`
	notRun := `{"stdout":"","stderr":"secret resolution failed: x","exit_code":-1,"truncated":false,"not_run":true}`
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, refusal)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m x"}`, coreag.ToolOutcomeOK, refusal)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git diff"}`, coreag.ToolOutcomeOK, notRun)
	recs := h.outbox()
	if n := len(events(recs, KindTerminal)); n != 0 {
		t.Fatalf("terminal events = %d, want 0 for refusals", n)
	}
	if n := len(events(recs, KindCommit)) + len(events(recs, KindPhaseChange)); n != 0 {
		t.Fatalf("a refusal produced %d commit/phase events", n)
	}
	tools := events(recs, KindTool)
	if len(tools) != 3 {
		t.Fatalf("agent.tool = %d, want 3", len(tools))
	}
	for i, want := range []string{"denied", "denied", "error"} {
		if tools[i].event.Payload["outcome"] != want || tools[i].event.Payload["tool"] != "kenaz__bash" {
			t.Errorf("refusal %d = %v, want agent.tool outcome %s", i, tools[i].event.Payload, want)
		}
	}
	task, _, _ := h.store.LoadTask(ctx, h.taskID("s1"))
	if task.TestRuns != 0 || task.TestFails != 0 || task.CommitCount != 0 || task.Phase != string(PhaseIdle) {
		t.Errorf("refusals moved counters/phase: %+v", task)
	}

	// A real process exiting -1 (no marker) is still a terminal event.
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK,
		`{"stdout":"","stderr":"signal: killed","exit_code":-1,"truncated":false}`)
	recs = h.outbox()
	terms := events(recs, KindTerminal)
	if len(terms) != 1 || terms[0].event.Payload["exit_code"] != float64(-1) {
		t.Fatalf("real -1 exit = %+v, want one terminal with exit_code -1", terms)
	}
	task, _, _ = h.store.LoadTask(ctx, h.taskID("s1"))
	if task.TestRuns != 1 || task.TestFails != 1 {
		t.Errorf("real failing test run counters = %d/%d, want 1/1", task.TestRuns, task.TestFails)
	}
}

func TestRecorder_TurnRules(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	// No tool call yet: no task, so a turn records nothing (§12 A-1).
	h.rec.TurnEnded(ctx, "s1", "completed", 1, 0, time.Second)
	if recs := h.outbox(); len(recs) != 0 {
		t.Fatalf("a tool-less session recorded %d records", len(recs))
	}
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	h.rec.TurnEnded(ctx, "s1", "weird-outcome", 1, 0, time.Second)
	recs := h.outbox()
	phases := events(recs, KindPhaseChange)
	if len(phases) != 2 || phases[1].event.Payload["phase"] != "idle" {
		t.Errorf("phases = %+v, want exploring then idle", phases)
	}
	if out := events(recs, KindTurn)[0].event.Payload["outcome"]; out != "failed" {
		t.Errorf("unknown outcome mapped to %v, want failed", out)
	}
}

func TestRecorder_UpsertThrottle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	count := func() int {
		n := 0
		for _, r := range h.outbox() {
			if r.Table == mlstore.TableTasks {
				n++
			}
		}
		return n
	}
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	h.clock.advance(30 * time.Second)
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	if n := count(); n != 1 {
		t.Fatalf("upserts within 60 s = %d, want 1", n)
	}
	h.clock.advance(31 * time.Second)
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	if n := count(); n != 2 {
		t.Fatalf("upserts after 61 s = %d, want 2", n)
	}
}

func TestRecorder_SessionDeleteCompletesTask(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	h.clock.advance(10 * time.Second)
	if err := h.rec.SessionDeleted(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	task := lastTask(t, h.outbox())
	if task.CompletedAt == nil || *task.CompletedAt != h.clock.Now().UnixMilli() {
		t.Fatalf("completed_at = %v, want the delete time", task.CompletedAt)
	}
	row, _, _ := h.store.LoadTask(ctx, h.taskID("s1"))
	if row.CompletedAt == 0 {
		t.Fatal("completed_at not persisted")
	}
}

func TestRecorder_IdleSweepCompletesAtLastActive(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	h.flush()
	lastActive := h.clock.Now().UnixMilli()
	h.clock.advance(6 * 24 * time.Hour)
	if err := h.rec.SweepIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if row, _, _ := h.store.LoadTask(ctx, h.taskID("s1")); row.CompletedAt != 0 {
		t.Fatal("a 6-day-idle task was completed")
	}
	h.clock.advance(2 * 24 * time.Hour)
	if err := h.rec.SweepIdle(ctx); err != nil {
		t.Fatal(err)
	}
	task := lastTask(t, h.outbox())
	if task.CompletedAt == nil || *task.CompletedAt != lastActive {
		t.Fatalf("idle-swept completed_at = %v, want last_active %d", task.CompletedAt, lastActive)
	}
}

// The observer never blocks: a full queue drops instead.
func TestRecorder_FullQueueDropsNeverBlocks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	block := make(chan struct{})
	rec := NewRecorder(Config{Store: h.store, Hasher: NewHasher(h.dir), Gate: GateFunc(func() bool { return true }),
		QueueSize: 1, SweepInterval: -1})
	defer rec.Close()
	rec.enqueue(func() { <-block }) // wedge the worker
	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			rec.ToolCallCompleted(coreag.ToolCallRecord{Ctx: context.Background(), SessionID: "s", ToolName: "kenaz__glob", Outcome: coreag.ToolOutcomeOK})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ToolCallCompleted blocked on a full queue")
	}
	close(block)
	if rec.Dropped() == 0 {
		t.Error("a full queue dropped nothing")
	}
}

// WP04 defect fix: the per-call throttle alone left a session that went
// quiet inside the 60 s window with its final counters unshipped until
// completion. FlushTasks (the shutdown drain) and the UpsertEvery ticker
// (flushStale) write a trailing upsert — and only for tasks that moved.
func TestRecorder_TrailingUpsertCarriesFinalCounters(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"a.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	// Same millisecond as the first upsert, then a little later: neither
	// call is ≥ 60 s after it, so neither upserts on its own.
	h.call(ctx, "s1", "kenaz__write_file", `{"path":"b.go","content":"x"}`, coreag.ToolOutcomeOK, `{"bytes_written":1}`)
	h.clock.advance(5 * time.Second)
	h.call(ctx, "s1", "kenaz__bash", `{"command":"go test ./..."}`, coreag.ToolOutcomeOK, bashFail)
	countTasks := func(recs []decoded) int {
		n := 0
		for _, r := range recs {
			if r.Table == mlstore.TableTasks {
				n++
			}
		}
		return n
	}
	recs := h.outbox()
	if n := countTasks(recs); n != 1 {
		t.Fatalf("task upserts before the flush = %d, want 1 (throttled)", n)
	}
	if got := lastTask(t, recs); len(got.Files) != 1 || got.TestRuns != 0 {
		t.Fatalf("first upsert = %+v, want the first-activity state", got)
	}

	// The ticker half: not yet due (last upsert 5 s old), then due.
	h.rec.flushStaleForTest(t, false)
	if n := countTasks(h.outbox()); n != 1 {
		t.Fatalf("flushStale upserted a task whose last upsert is 5 s old")
	}
	h.clock.advance(60 * time.Second)
	h.rec.flushStaleForTest(t, false)
	recs = h.outbox()
	if n := countTasks(recs); n != 2 {
		t.Fatalf("task upserts after the 60 s trailing flush = %d, want 2", n)
	}
	if got := lastTask(t, recs); len(got.Files) != 2 || got.TestRuns != 1 || got.TestFails != 1 || got.Phase != "testing" {
		t.Fatalf("trailing upsert = %+v, want the final counters", got)
	}

	// Nothing moved since: no further upsert.
	if err := h.rec.FlushTasks(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countTasks(h.outbox()); n != 2 {
		t.Fatalf("FlushTasks re-upserted an unchanged task (%d upserts)", n)
	}

	// The drain half ignores the age.
	h.call(ctx, "s1", "kenaz__bash", `{"command":"git commit -m x"}`, coreag.ToolOutcomeOK, bashOK)
	if err := h.rec.FlushTasks(ctx); err != nil {
		t.Fatal(err)
	}
	recs = h.outbox()
	if n := countTasks(recs); n != 3 || lastTask(t, recs).CommitCount != 1 {
		t.Fatalf("FlushTasks after a commit: upserts=%d last=%+v", n, lastTask(t, recs))
	}

	// Gate closed: nothing.
	h.call(ctx, "s1", "kenaz__glob", `{}`, coreag.ToolOutcomeOK, "{}")
	h.flush()
	h.gate.Store(false)
	if err := h.rec.FlushTasks(ctx); err != nil {
		t.Fatal(err)
	}
	h.gate.Store(true)
	if n := countTasks(h.outbox()); n != 3 {
		t.Fatalf("FlushTasks wrote with the gate closed (%d upserts)", n)
	}
}

func (r *Recorder) flushStaleForTest(t *testing.T, force bool) {
	t.Helper()
	ack := make(chan struct{})
	if !r.enqueue(func() { r.flushStale(context.Background(), force); close(ack) }) {
		t.Fatal("enqueue failed")
	}
	<-ack
}
