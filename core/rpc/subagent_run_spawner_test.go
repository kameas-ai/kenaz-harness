package rpc

// subagent_run_spawner_test.go — mission
// subagent-control-and-background-tasks-01PMZB11 UNIT-6.
//
// These tests drive the REAL production surfaces the same way
// chat_run_dispatcher_test.go does (model-scheduled-jobs-01PMSJ01 WP04):
// a real sqlite-backed core.New(), the real core/rpc/views/llm.API, the
// real ChatRunner + chat_default.yaml graph, a real
// conversation.Manager + session.Manager (so BranchSeamAdapter.Fork does
// real Fork work — a real branch row, a real child session), and a real
// EventBus. Only the model itself is faked (chat.Config.EnvDefaults),
// same substitution point the scheduled-jobs tests use.
//
// AC-07's falsification result lives here: TestSubagentSpawnRoundTrip
// with SetRunSpawner never called is exactly the pre-UNIT-6 shape (Fork
// creates the branch, WaitForChildRun returns nil immediately without
// ever having run anything) — TestBranchSeamWaitForChildRunNoOpWithoutSpawner
// pins that as the explicit, still-correct v1 fallback rather than an
// oversight.

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
)

// subagentSpawnerTestStack bundles the real production surfaces a
// child-run spawner depends on, built over a real sqlite DB — the same
// shape as dispatcherTestStack (chat_run_dispatcher_test.go) plus a real
// BranchSeamAdapter and a real *coretasks.Registry.
type subagentSpawnerTestStack struct {
	sessionsAPI sessionsview.SessionsAPI
	llmAPI      llmview.LLMConnectorAPI
	bus         *EventBus
	seam        *graphview.BranchSeamAdapter
	tasks       *coretasks.Registry
	model       *fakeModel
}

func buildSubagentSpawnerTestStack(t *testing.T, responseText string) *subagentSpawnerTestStack {
	t.Helper()
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	sessMgr := c.SessionManager()
	if sessMgr == nil {
		t.Fatal("core.New produced a nil SessionManager")
	}
	sessionsAPI := sessionsview.NewManagerAPI(sessMgr)

	convMgr := newConversationManager(c)
	if convMgr == nil {
		t.Fatal("newConversationManager returned nil over a real Core")
	}
	seam := graphview.NewBranchSeamAdapter(convMgr, sessMgr)

	historyAdapter := newSessionHistoryReader(c)
	if historyAdapter == nil {
		t.Fatal("newSessionHistoryReader returned nil over a real Core")
	}
	historyWriter := &llmHistoryWriter{inner: historyAdapter}

	bus := NewEventBus()
	broker := chatBrokerAdapter{broker: NewStreamBroker(NewMultiEmitter(&busEmitter{bus: bus}))}

	model := &fakeModel{response: responseText}
	graph := loadChatDefaultGraph(t)

	runner, err := chat.New(chat.Config{
		Kernel:        coreag.NewKernel(),
		Registry:      dispatcherTestRegistry{},
		Broker:        broker,
		History:       chatSessionMessageReader{inner: historyAdapter},
		HistoryWriter: historyWriter,
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = model
		},
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}

	llmAPI := llmview.New(llmview.Config{
		Registry:   dispatcherTestRegistry{},
		History:    historyWriter,
		ChatRunner: runner,
	})

	// core.Storage() returns the storage.DB interface; coretasks.NewSQLiteStore
	// needs the raw *sql.DB, same extraction api.go's own task-registry
	// construction uses.
	var taskStore coretasks.SQLStore
	if db := c.Storage(); db != nil {
		type sqlHandle interface{ SQL() *sql.DB }
		if h, ok := db.(sqlHandle); ok {
			if rawDB := h.SQL(); rawDB != nil {
				taskStore = coretasks.NewSQLiteStore(rawDB)
			}
		}
	}
	if taskStore == nil {
		t.Fatal("could not extract a raw *sql.DB from core.New's Storage")
	}
	taskReg := coretasks.NewRegistry(coretasks.Options{
		Store: taskStore,
	})

	return &subagentSpawnerTestStack{
		sessionsAPI: sessionsAPI,
		llmAPI:      llmAPI,
		bus:         bus,
		seam:        seam,
		tasks:       taskReg,
		model:       model,
	}
}

// armSpawner wires NewSubagentRunSpawner onto the stack's seam, mirroring
// the SetRunSpawner call site in core/rpc/api.go's New().
func (s *subagentSpawnerTestStack) armSpawner(t *testing.T, timeout time.Duration) {
	t.Helper()
	s.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM:            s.llmAPI,
		Bus:            s.bus,
		Tasks:          s.tasks,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        timeout,
	}))
}

// TestSubagentSpawnRoundTrip is AC-07's positive arm: in a
// production-shaped wiring (real seam, real spawner, real ChatRunner —
// only the model is faked), dispatching kenaz__subagent_dispatch
// produces a child session whose run reaches a terminal state observable
// through WaitForChildRun, and the tool's sync path returns the worker's
// reply as merge_summary.
//
// Mutation: nil the spawner (don't call armSpawner). Must revert to the
// pre-UNIT-6 no-op — see TestBranchSeamWaitForChildRunNoOpWithoutSpawner
// for that half pinned separately.
func TestSubagentSpawnRoundTrip(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "sub-agent worker done")
	stack.armSpawner(t, 5*time.Second)

	parent, err := stack.sessionsAPI.Create(context.Background(), "parent session")
	if err != nil {
		t.Fatalf("create parent session: %v", err)
	}

	tool := coresubagent.New(coresubagent.Options{
		DataDir: t.TempDir(), // empty — bundled profiles only, "explore" ships bundled
		Seam:    stack.seam,
	})
	ctx := toolloop.WithSessionID(context.Background(), parent.ID)
	args := json.RawMessage(`{"profile":"explore","prompt":"find all usages","run_in_background":false}`)

	raw, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: unexpected Go error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := result["status"].(string); got != "complete" {
		t.Fatalf("status=%q, want complete; full result=%+v", got, result)
	}
	branchID, _ := result["branch_id"].(string)
	if branchID == "" {
		t.Fatal("branch_id is empty")
	}
	summary, _ := result["merge_summary"].(string)
	if !strings.Contains(summary, "sub-agent worker done") {
		t.Errorf("merge_summary=%q, want it to contain the worker's reply", summary)
	}

	// Ground truth: ask the fake model was actually invoked (proves
	// StartStream really dispatched through the real ChatRunner, not a
	// stub that fabricated the reply).
	if len(stack.model.snapshotRequests()) == 0 {
		t.Fatal("fake model was never called — the spawner didn't actually start a run")
	}
}

// TestSubagentSpawnRoundTrip_AsyncCompletesTask verifies the async
// (run_in_background=true, the tool's default) path: the tool returns
// immediately, and the task registry's row for it reaches a terminal
// state once the background run finishes — ground truth read from
// tasks.Registry, not from the tool's return value.
func TestSubagentSpawnRoundTrip_AsyncCompletesTask(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "async worker done")
	stack.armSpawner(t, 5*time.Second)

	parent, err := stack.sessionsAPI.Create(context.Background(), "parent session")
	if err != nil {
		t.Fatalf("create parent session: %v", err)
	}

	tool := coresubagent.New(coresubagent.Options{
		DataDir: t.TempDir(),
		Seam:    stack.seam,
	})
	ctx := toolloop.WithSessionID(context.Background(), parent.ID)
	args := json.RawMessage(`{"profile":"explore","prompt":"find usages"}`) // run_in_background defaults true

	start := time.Now()
	raw, err := tool.Call(ctx, args)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Call: unexpected Go error: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("async dispatch took %v, expected near-instant return", elapsed)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, _ := result["status"].(string); got != "running" {
		t.Fatalf("status=%q, want running", got)
	}

	// Poll the task registry for a terminal state — ground truth for
	// "the background run actually completed", not the tool's own
	// return value (which only ever says "running").
	deadline := time.Now().Add(5 * time.Second)
	var lastTasks []coretasks.Task
	for time.Now().Before(deadline) {
		lastTasks = stack.tasks.List()
		for _, tk := range lastTasks {
			if tk.Kind == coretasks.KindSubagent && tk.IsTerminal() {
				if tk.Status != coretasks.StatusCompleted {
					t.Fatalf("task ended with status %q, want completed", tk.Status)
				}
				return // success
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no terminal subagent task within timeout; tasks=%+v", lastTasks)
}

// TestBranchSeamWaitForChildRunNoOpWithoutSpawner pins the pre-UNIT-6
// fallback explicitly: with no RunSpawner wired, Fork creates the branch
// and child session as always, and WaitForChildRun returns nil
// immediately — the seam is otherwise unaffected by UNIT-6 when nothing
// arms it. This is the "AC-07's negative half" for the SEAM itself
// (registerSubagentDispatchTool's own negative arm is pinned in
// builtins_wiring_test.go).
func TestBranchSeamWaitForChildRunNoOpWithoutSpawner(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "unused") // armSpawner deliberately NOT called

	parent, err := stack.sessionsAPI.Create(context.Background(), "parent session")
	if err != nil {
		t.Fatalf("create parent session: %v", err)
	}
	handle, err := stack.seam.Fork(context.Background(), coreag.ForkRequest{
		ParentSessionID: parent.ID,
		Title:           "no-spawner branch",
		HandoffPrompt:   "hello",
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if handle.ChildSessionID == "" {
		t.Fatal("Fork produced no child session even without a spawner — that's a Fork regression, not a UNIT-6 one")
	}
	waitErr := stack.seam.WaitForChildRun(context.Background(), handle.BranchID)
	if waitErr != nil {
		t.Fatalf("WaitForChildRun with no spawner wired = %v, want nil (pre-UNIT-6 no-op)", waitErr)
	}
	if len(stack.model.snapshotRequests()) != 0 {
		t.Fatal("the fake model was called even though no spawner was armed — a run was spawned that shouldn't have been")
	}
}

// TestSubagentDispatchRegisteredInProductionWiring is AC-07's
// "production-shaped wiring" requirement, at the full New() level: boots
// a REAL HarnessAPI the exact way production does (the same call
// TestBuiltinEnabledPredicate_AllRegisteredToolsHaveExplicitCase uses),
// and asserts kenaz__subagent_dispatch appears in the builtin catalog —
// proving the whole chain (a.branchSeam construction, SetRunSpawner,
// registerSubagentDispatchTool) actually connects in New(), not just in
// the unit-level tests above that construct each piece by hand.
//
// Mutation: comment out the `registerSubagentDispatchTool(c,
// stack.builtins, a.branchSeam)` call in core/rpc/api.go's New(). Must
// fail (the tool disappears from the catalog even though a.branchSeam,
// a.llmAPI and a.eventBus are all real).
func TestSubagentDispatchRegisteredInProductionWiring(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))

	registry := api.Builtins()
	if registry == nil {
		t.Fatal("builtins registry is nil — construction broke")
	}
	var found bool
	for _, name := range registry.Names() {
		if name == coresubagent.ToolName {
			found = true
		}
	}
	if !found {
		t.Fatal("kenaz__subagent_dispatch is not registered by production wiring (New()) — " +
			"expected non-nil a.branchSeam + a.llmAPI + a.eventBus to arm the spawner and register the tool")
	}
}
