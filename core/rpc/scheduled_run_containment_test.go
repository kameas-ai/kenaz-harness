package rpc

// scheduled_run_containment_test.go — model-harness-toolset-01MHTS001
// WP02, finding H-1: a fired scheduled run is contained to its tool
// allowlist per tool call.
//
// Every test here drives the REAL fire path end to end: a real
// ChatCronEngine (one-shot trigger, so it fires immediately on Start)
// with the real Cedar engine as its execute gate, the real
// LiveChatRunDispatcher, the real chat runner over the production
// chat_default.yaml graph, the real kernel tool adapter, and the real
// merged permission resolver (toolloop.NewMergedResolver over the
// production cedarSessionKindResolver) — all over a real sqlite DB from
// core.New. Only the model and the tool pool are scripted: the model asks
// for two tools, the pool records which calls actually dispatched. Nothing
// here fakes the resolver.
//
// Mutation evidence (run, observed failing, reverted):
//   - disable the containment Check at the top of
//     cedarSessionKindResolver.Resolve -> ..._ModelRow_OffListDenied
//     (kenaz__beta dispatches) and ..._WiredInProduction fail;
//   - drop CreatedBy/ToolAllowlist from the cron engine's ChatRunSpec ->
//     ..._ModelRow_OffListDenied fails (the dispatcher refuses the run:
//     "dispatched without the allowlist its execute gate evaluated").
// The discoverer's probe mark is pinned separately by
// views/llm's TestMCPToolDiscoverer_ResolvesAsVisibilityProbe.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"

	_ "modernc.org/sqlite"
)

// ── scripted model: turn 1 asks for kenaz__alpha AND kenaz__beta, turn 2
// answers in text. Race-safe (driven from the kernel goroutine).

type containmentModel struct {
	mu    sync.Mutex
	calls int
}

func (m *containmentModel) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n == 1 {
		return coreag.LLMResponse{
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-a", Name: "kenaz__alpha", Arguments: `{}`},
				{ID: "tu-b", Name: "kenaz__beta", Arguments: `{}`},
			},
		}, nil
	}
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "done"})
	}
	return coreag.LLMResponse{Content: "done", FinishReason: "stop"}, nil
}

func (m *containmentModel) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// ── recording tool pool: which calls actually reached dispatch.

type containmentPool struct {
	mu    sync.Mutex
	calls []string
}

func (p *containmentPool) Tools(context.Context) ([]chat.ToolEntry, error) {
	return []chat.ToolEntry{{Server: "kenaz", Name: "alpha"}, {Server: "kenaz", Name: "beta"}}, nil
}

func (p *containmentPool) Call(_ context.Context, server, tool string, _ []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, server+"__"+tool)
	return []byte(`{"ok":true}`), nil
}

func (p *containmentPool) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

// ── recording audit emitter (race-safe).

type containmentAudit struct {
	mu     sync.Mutex
	events []contextaudit.Event
}

func (a *containmentAudit) Emit(_ context.Context, e contextaudit.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *containmentAudit) blocked(t *testing.T) []contextaudit.BlockedPermissionRequestPayload {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []contextaudit.BlockedPermissionRequestPayload
	for _, e := range a.events {
		if e.Kind != contextaudit.KindBlockedPermissionRequest {
			continue
		}
		var p contextaudit.BlockedPermissionRequestPayload
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("decode audit payload: %v", err)
		}
		out = append(out, p)
	}
	return out
}

type containmentFixture struct {
	runner   *chat.ChatRunner
	sessions sessionsview.SessionsAPI
	llm      llmview.LLMConnectorAPI
	store    scheduler.ScheduledChatStore
	engine   *scheduler.ChatCronEngine
	pool     *containmentPool
	model    *containmentModel
	audit    *containmentAudit
	blocked  blockedrequests.Store
	registry *ScheduledRunContainmentRegistry
	db       string // dataDir
}

// buildContainmentFixture wires the production fire path over dataDir
// (which may already hold a materialised upgrade snapshot).
func buildContainmentFixture(t *testing.T, dataDir string) *containmentFixture {
	t.Helper()
	return buildContainmentFixtureWith(t, dataDir, nil, 10*time.Second)
}

// buildContainmentFixtureWith is buildContainmentFixture with the model
// (nil = the default containmentModel) and dispatcher timeout exposed.
func buildContainmentFixtureWith(t *testing.T, dataDir string, llm coreag.LLMProvider, timeout time.Duration) *containmentFixture {
	t.Helper()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	sessMgr := c.SessionManager()
	sessionsAPI := sessionsview.NewManagerAPI(sessMgr)
	historyAdapter := newSessionHistoryReader(c)
	historyWriter := &llmHistoryWriter{inner: historyAdapter}
	bus := NewEventBus()
	broker := chatBrokerAdapter{broker: NewStreamBroker(NewMultiEmitter(&busEmitter{bus: bus}))}

	engine := buildCedarEngineOrNil(dataDir, nil)
	if engine == nil {
		t.Fatal("buildCedarEngineOrNil returned nil over a real DataDir")
	}

	audit := &containmentAudit{}
	blocked := blockedrequests.NewSQLiteStore(c.Storage())
	registry := NewScheduledRunContainmentRegistry(newBlockedRequestSink(blocked, audit, nil))

	// The production resolver chain (api.go newLLMStack + New()): the
	// session arm, with the containment registry bound in, merged over a
	// static arm.
	sessionArm := newCedarSessionKindResolver(sessMgr, engine)
	sessionArm.SetScheduledRunContainment(registry)
	perms := toolloop.NewMergedResolver(nil, sessionArm)

	pool := &containmentPool{}
	model := &containmentModel{}
	if llm == nil {
		llm = model
	}
	// A fresh graph per run: the runner's dials mutate the loaded graph,
	// and two runs sharing one value race (production's loader parses per
	// call too).
	_ = loadChatDefaultGraph(t) // fail fast if the production graph does not parse
	graphYAML, err := os.ReadFile("views/agentgraph/library/chat_default.yaml")
	if err != nil {
		t.Fatalf("read chat_default.yaml: %v", err)
	}
	graphLoader := func() (coreag.Graph, error) {
		g, err := coreag.LoadYAML(graphYAML)
		if err != nil {
			return coreag.Graph{}, err
		}
		return coreag.GateAgenticTurnRouting(g, false), nil
	}
	runner, err := chat.New(chat.Config{
		Kernel:        coreag.NewKernel(),
		Registry:      dispatcherTestRegistry{},
		Broker:        broker,
		History:       chatSessionMessageReader{inner: historyAdapter},
		HistoryWriter: historyWriter,
		GraphLoader:   graphLoader,
		MaxTurns:      func() int { return 25 },
		Pool:          pool,
		Perms:         chatPermsAdapter{inner: perms},
		EnvDefaults:   func(env *coreag.Env) { env.LLM = llm },
	})
	if err != nil {
		t.Fatalf("chat.New: %v", err)
	}
	llmAPI := llmview.New(llmview.Config{Registry: dispatcherTestRegistry{}, History: historyWriter, ChatRunner: runner})

	store := scheduler.NewSQLiteChatStore(c.Storage())
	dispatcher := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          store,
		Sessions:       sessionsAPI,
		LLM:            llmAPI,
		Bus:            bus,
		DefaultProfile: func() string { return "test-profile" },
		Containment:    registry,
		Timeout:        timeout,
	})
	cron, err := scheduler.NewChatCronEngine(context.Background(), scheduler.ChatCronEngineConfig{Store: store, Cedar: engine})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	cron.SetDispatcher(dispatcher)
	cron.Start()
	t.Cleanup(cron.Stop)

	return &containmentFixture{runner: runner, sessions: sessionsAPI, llm: llmAPI, store: store, engine: cron, pool: pool, model: model, audit: audit, blocked: blocked, registry: registry, db: dataDir}
}

// fireOnce creates a one-shot row due now, arms it through the engine's
// production Sync, and waits for its history row — the cron fire path
// (fireOnce -> fireSync -> Cedar gate -> dispatcher), not RunNow.
func (f *containmentFixture) fireOnce(t *testing.T, rec scheduler.ChatRunRecord) scheduler.ChatRunHistoryRecord {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	runAt := now.Add(-time.Second)
	rec.Name = "containment " + rec.ID
	rec.PromptTemplate = "use your tools"
	rec.OutputSink = "none"
	rec.Enabled = true
	rec.TriggerKind = scheduler.TriggerKindOnce
	rec.RunAt = &runAt
	rec.CreatedAt, rec.UpdatedAt = now, now
	if err := f.store.Create(ctx, rec); err != nil {
		t.Fatalf("Create %s: %v", rec.ID, err)
	}
	return f.syncAndAwait(t, rec.ID)
}

// syncAndAwait arms id through the engine's production Sync, then awaits.
func (f *containmentFixture) syncAndAwait(t *testing.T, id string) scheduler.ChatRunHistoryRecord {
	t.Helper()
	if err := f.engine.Sync(context.Background(), id); err != nil {
		t.Fatalf("Sync %s: %v", id, err)
	}
	return f.await(t, id)
}

// await waits for id's one-shot fire to settle and returns its history row.
func (f *containmentFixture) await(t *testing.T, id string) scheduler.ChatRunHistoryRecord {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for {
		hist, err := f.store.History(ctx, id, 5)
		if err != nil {
			t.Fatalf("History: %v", err)
		}
		if len(hist) > 0 && !f.engine.Registered(id) {
			if r, gerr := f.store.Get(ctx, id); gerr == nil && !r.Enabled {
				return hist[0]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s to fire; history=%+v", id, hist)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// writeRawAllowlist overwrites a row's tool_allowlist column with raw text
// (a corrupted/hand-edited value no production writer produces).
func writeRawAllowlist(t *testing.T, dataDir, id, raw string) {
	t.Helper()
	dsn := "file:" + url.PathEscape(filepath.Join(dataDir, "data.db")) + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE scheduled_chat_runs SET tool_allowlist = ? WHERE id = ?`, raw, id); err != nil {
		t.Fatalf("corrupt allowlist: %v", err)
	}
}

func toolBlockedRows(t *testing.T, s blockedrequests.Store) []blockedrequests.Record {
	t.Helper()
	all, err := s.ListByStatus(context.Background(), "")
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	var out []blockedrequests.Record
	for _, r := range all {
		if r.Family == toolFamilyBlocked {
			out = append(out, r)
		}
	}
	return out
}

// TestScheduledRunContainment_ModelRow_OffListDenied is the H-1 proof: a
// model-created schedule with allowlist [kenaz__alpha] fires; the run's
// call to kenaz__alpha dispatches and its call to kenaz__beta is denied
// at call time, with a durable blocked row and an audit record.
func TestScheduledRunContainment_ModelRow_OffListDenied(t *testing.T) {
	sandboxUserConfigDir(t)
	f := buildContainmentFixture(t, t.TempDir())

	hist := f.fireOnce(t, scheduler.ChatRunRecord{
		ID:            "cr-model",
		CreatedBy:     scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist: []string{"kenaz__alpha"},
	})
	if hist.Status != "completed" {
		t.Fatalf("run status = %q (error %q), want completed — the run must execute, contained", hist.Status, hist.Error)
	}
	if got := f.pool.snapshot(); len(got) != 1 || got[0] != "kenaz__alpha" {
		t.Fatalf("dispatched tools = %v, want exactly [kenaz__alpha] (kenaz__beta is off the allowlist)", got)
	}

	rows := toolBlockedRows(t, f.blocked)
	if len(rows) != 1 {
		t.Fatalf("tool-family blocked rows = %+v, want exactly one (for kenaz__beta)", rows)
	}
	r := rows[0]
	if r.Resource != "kenaz__beta" || r.Action != "use_tool" || r.Origin != "scheduled_chat_run" || r.OriginID != "cr-model" || r.SessionID != hist.SessionID {
		t.Fatalf("blocked row = %+v, want resource kenaz__beta / use_tool / scheduled_chat_run cr-model / session %s", r, hist.SessionID)
	}
	audits := f.audit.blocked(t)
	if len(audits) != 1 || audits[0].Resource != "kenaz__beta" || audits[0].Family != "tool" || audits[0].OriginID != "cr-model" {
		t.Fatalf("blocked audit records = %+v, want one for kenaz__beta", audits)
	}
	// The terminal event released the session; a finished run's session
	// is no longer contained.
	if f.registry.Contained(hist.SessionID) {
		t.Error("session still contained after its run's terminal event")
	}
}

// TestScheduledRunContainment_ModelRow_CorruptAllowlistDoesNotRun: a
// model-created row whose allowlist cannot be decoded never runs (B-3 F2)
// — no tool dispatches and the model is never called.
func TestScheduledRunContainment_ModelRow_CorruptAllowlistDoesNotRun(t *testing.T) {
	sandboxUserConfigDir(t)
	dir := t.TempDir()
	f := buildContainmentFixture(t, dir)

	ctx := context.Background()
	now := time.Now().UTC()
	runAt := now.Add(-time.Second)
	if err := f.store.Create(ctx, scheduler.ChatRunRecord{
		ID: "cr-corrupt", Name: "corrupt", PromptTemplate: "use your tools", OutputSink: "none",
		Enabled: true, TriggerKind: scheduler.TriggerKindOnce, RunAt: &runAt,
		CreatedBy: scheduler.ScheduledRunCreatedByModel, ToolAllowlist: []string{"kenaz__alpha"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeRawAllowlist(t, dir, "cr-corrupt", `{not json`)

	hist := f.syncAndAwait(t, "cr-corrupt")
	if hist.Status != "failed" {
		t.Fatalf("status = %q, want failed (a model row with an unreadable allowlist does not run)", hist.Status)
	}
	if got := f.pool.snapshot(); len(got) != 0 {
		t.Fatalf("dispatched tools = %v, want none", got)
	}
	if n := f.model.callCount(); n != 0 {
		t.Fatalf("model called %d times, want 0 — the run must not start", n)
	}
}

// TestScheduledRunContainment_ModelRow_NoAllowlistDoesNotRun: the empty
// case (B-3 F1) — refused before any session or model call.
func TestScheduledRunContainment_ModelRow_NoAllowlistDoesNotRun(t *testing.T) {
	sandboxUserConfigDir(t)
	f := buildContainmentFixture(t, t.TempDir())
	hist := f.fireOnce(t, scheduler.ChatRunRecord{ID: "cr-empty", CreatedBy: scheduler.ScheduledRunCreatedByModel})
	if hist.Status != "failed" || hist.SessionID != "" {
		t.Fatalf("history = %+v, want failed with no session", hist)
	}
	if got := f.pool.snapshot(); len(got) != 0 {
		t.Fatalf("dispatched tools = %v, want none", got)
	}
	if n := f.model.callCount(); n != 0 {
		t.Fatalf("model called %d times, want 0", n)
	}
}

// TestScheduledRunContainment_UserRow_CorruptAllowlistDeniesEveryTool: a
// USER row whose declared allowlist no longer decodes runs, but every tool
// is denied — an unreadable allowlist never reads as unrestricted.
func TestScheduledRunContainment_UserRow_CorruptAllowlistDeniesEveryTool(t *testing.T) {
	sandboxUserConfigDir(t)
	dir := t.TempDir()
	f := buildContainmentFixture(t, dir)

	ctx := context.Background()
	now := time.Now().UTC()
	runAt := now.Add(-time.Second)
	if err := f.store.Create(ctx, scheduler.ChatRunRecord{
		ID: "cr-user-corrupt", Name: "user corrupt", PromptTemplate: "use your tools", OutputSink: "none",
		Enabled: true, TriggerKind: scheduler.TriggerKindOnce, RunAt: &runAt,
		CreatedBy: scheduler.ScheduledRunCreatedByUser, ToolAllowlist: []string{"kenaz__alpha"},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	writeRawAllowlist(t, dir, "cr-user-corrupt", `["kenaz__alpha"`)

	hist := f.syncAndAwait(t, "cr-user-corrupt")
	if hist.Status != "completed" {
		t.Fatalf("status = %q (%q), want completed", hist.Status, hist.Error)
	}
	if got := f.pool.snapshot(); len(got) != 0 {
		t.Fatalf("dispatched tools = %v, want none (corrupt allowlist denies everything)", got)
	}
	if rows := toolBlockedRows(t, f.blocked); len(rows) != 2 {
		t.Fatalf("blocked rows = %+v, want two (alpha and beta)", rows)
	}
}

// TestScheduledRunContainment_UserRow_NoAllowlistUnchanged pins that a
// HUMAN-created schedule with no allowlist runs exactly as before WP02:
// every tool dispatches, nothing is recorded.
func TestScheduledRunContainment_UserRow_NoAllowlistUnchanged(t *testing.T) {
	sandboxUserConfigDir(t)
	f := buildContainmentFixture(t, t.TempDir())
	hist := f.fireOnce(t, scheduler.ChatRunRecord{ID: "cr-user", CreatedBy: scheduler.ScheduledRunCreatedByUser})
	if hist.Status != "completed" {
		t.Fatalf("status = %q (%q), want completed", hist.Status, hist.Error)
	}
	if got := sortedCalls(f.pool); got != "kenaz__alpha,kenaz__beta" {
		t.Fatalf("dispatched tools = %q, want both (a user row without an allowlist is unrestricted)", got)
	}
	if rows := toolBlockedRows(t, f.blocked); len(rows) != 0 {
		t.Fatalf("blocked rows = %+v, want none", rows)
	}
	if f.registry.Contained(hist.SessionID) {
		t.Error("an uncontained run's session was registered as contained")
	}
}

// TestScheduledRunContainment_UpgradedUserRowStillRuns boots the v0.89.2
// upgrade snapshot (CLAUDE.md blind spot #3) and fires the
// seed-schedrun-1 row a previous release wrote — created_by='user',
// tool_allowlist=” — through the same production fire path. It must run
// unrestricted, exactly as it did before WP02.
func TestScheduledRunContainment_UpgradedUserRowStillRuns(t *testing.T) {
	sandboxUserConfigDir(t)
	ctx := context.Background()
	dir := t.TempDir()
	dumpPath := filepath.Join("..", "storage", "sqlite", "testdata", "upgrade", "v0.89.2", "dump.sql")
	dump, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read %s: %v", dumpPath, err)
	}
	dsn := "file:" + url.PathEscape(filepath.Join(dir, "data.db")) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	raw.SetMaxOpenConns(1)
	if err := upgradesnap.Materialize(ctx, raw, string(dump)); err != nil {
		t.Fatalf("materialise v0.89.2: %v", err)
	}
	// Make the seeded row a due one-shot so the engine fires it now; its
	// provenance columns (created_by, tool_allowlist) stay exactly as the
	// previous release wrote them.
	due := time.Now().Add(-time.Second).Unix()
	if _, err := raw.Exec(`UPDATE scheduled_chat_runs SET trigger_kind='once', run_at=?, output_sink='none' WHERE id='seed-schedrun-1'`, due); err != nil {
		t.Fatalf("arm seed row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	f := buildContainmentFixture(t, dir)
	rec, err := f.store.Get(ctx, "seed-schedrun-1")
	if err != nil {
		t.Fatalf("seed row missing after upgrade boot: %v", err)
	}
	if rec.CreatedBy != scheduler.ScheduledRunCreatedByUser || len(rec.ToolAllowlist) != 0 || rec.ToolAllowlistUnresolvable {
		t.Fatalf("seed row provenance = %q / %v / unresolvable=%v, want user / none / false", rec.CreatedBy, rec.ToolAllowlist, rec.ToolAllowlistUnresolvable)
	}
	// The engine armed the due seed row itself at construction (it loads
	// every enabled row) and Start fired it — no Sync, or it fires twice.
	hist := f.await(t, "seed-schedrun-1")
	if hist.Status != "completed" {
		t.Fatalf("status = %q (%q), want completed", hist.Status, hist.Error)
	}
	if got := sortedCalls(f.pool); got != "kenaz__alpha,kenaz__beta" {
		t.Fatalf("dispatched tools = %q, want both — a pre-WP02 user row is unrestricted", got)
	}
}

// TestScheduledRunContainment_VisibilityProbeRecordsNothing: listing the
// catalog for a contained session hides off-list pool tools (same verdict)
// but records no blocked row — the model never asked for them.
func TestScheduledRunContainment_VisibilityProbeRecordsNothing(t *testing.T) {
	sink := &recordingContainmentSink{}
	reg := NewScheduledRunContainmentRegistry(sink)
	reg.Contain("sess-1", "cr-1", []string{"kenaz__alpha"})

	if _, denied := reg.Check(toolloop.WithVisibilityProbe(context.Background()), "sess-1", "kenaz", "beta"); !denied {
		t.Fatal("probe: off-list tool not denied")
	}
	if n := sink.count(); n != 0 {
		t.Fatalf("probe recorded %d rows, want 0", n)
	}
	if _, denied := reg.Check(context.Background(), "sess-1", "kenaz", "beta"); !denied {
		t.Fatal("dispatch: off-list tool not denied")
	}
	if n := sink.count(); n != 1 {
		t.Fatalf("dispatch recorded %d rows, want 1", n)
	}
	if _, denied := reg.Check(context.Background(), "sess-1", "kenaz", "alpha"); denied {
		t.Fatal("on-list tool denied")
	}
	if _, denied := reg.Check(context.Background(), "sess-2", "kenaz", "beta"); denied {
		t.Fatal("uncontained session denied")
	}
	// A child inherits; a parent with no containment gives nothing.
	if !reg.Inherit("child-1", "sess-1") {
		t.Fatal("Inherit from a contained parent reported false")
	}
	if _, denied := reg.Check(context.Background(), "child-1", "kenaz", "beta"); !denied {
		t.Fatal("child of a contained run can call an off-list tool")
	}
	if reg.Inherit("child-2", "sess-2") {
		t.Fatal("Inherit from an uncontained parent reported true")
	}
}

// TestScheduledRunContainment_WiredInProduction pins the New() wiring:
// the registry exists, and the PRODUCTION merged resolver
// (a.toolPermsResolver) denies an off-list tool for a contained session.
func TestScheduledRunContainment_WiredInProduction(t *testing.T) {
	sandboxUserConfigDir(t)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	if api.scheduledRunContainment == nil {
		t.Fatal("scheduledRunContainment not constructed")
	}
	if api.toolPermsResolver == nil {
		t.Fatal("toolPermsResolver nil")
	}
	api.scheduledRunContainment.Contain("sess-wired", "cr-wired", []string{"kenaz__alpha"})
	res, err := api.toolPermsResolver.Resolve(toolloop.WithVisibilityProbe(context.Background()), "sess-wired", "kenaz", "beta")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy != toolloop.PolicyDeny {
		t.Fatalf("production resolver verdict for an off-list tool = %+v, want deny", res)
	}
	res, err = api.toolPermsResolver.Resolve(context.Background(), "sess-other", "kenaz", "beta")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Policy == toolloop.PolicyDeny {
		t.Fatalf("uncontained session denied: %+v", res)
	}
}

type recordingContainmentSink struct {
	mu sync.Mutex
	n  int
}

func (s *recordingContainmentSink) RecordBlocked(context.Context, corefs.BlockedRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return nil
}

func (s *recordingContainmentSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

func sortedCalls(p *containmentPool) string {
	c := p.snapshot()
	sort.Strings(c)
	return strings.Join(c, ",")
}

// TestScheduledRunContainment_ListingShowsOnlyAllowlistedBuiltins (security
// review M2): a contained session's catalog lists only the builtins on its
// allowlist — visibility matches reachability — and listing records no
// blocked rows. An uncontained session still sees the full set. Production
// resolver and builtin registry from rpc.New.
func TestScheduledRunContainment_ListingShowsOnlyAllowlistedBuiltins(t *testing.T) {
	sandboxUserConfigDir(t)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	ctx := context.Background()
	contained, err := api.Sessions().Create(ctx, "Scheduled: list")
	if err != nil {
		t.Fatal(err)
	}
	other, err := api.Sessions().Create(ctx, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	api.scheduledRunContainment.Contain(contained.ID, "cr-list", []string{"kenaz__sleep"})

	disc := llmview.NewMCPToolDiscovererWithBuiltins(nil, api.toolPermsResolver, api.Builtins())
	names := func(sid string) []string {
		specs, err := disc.Tools(ctx, sid)
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		var out []string
		for _, s := range specs {
			out = append(out, s.Name)
		}
		sort.Strings(out)
		return out
	}
	if got := names(contained.ID); len(got) != 1 || got[0] != "kenaz__sleep" {
		t.Fatalf("contained listing = %v, want exactly [kenaz__sleep]", got)
	}
	var full []string
	for _, b := range api.Builtins().List() {
		full = append(full, b.Name())
	}
	sort.Strings(full)
	if got := names(other.ID); strings.Join(got, ",") != strings.Join(full, ",") {
		t.Fatalf("uncontained listing = %v, want exactly the full unfiltered builtin set %v", got, full)
	}
	if rows := toolBlockedRows(t, blockedrequests.NewSQLiteStore(c.Storage())); len(rows) != 0 {
		t.Fatalf("listing recorded blocked rows: %+v", rows)
	}
}

// authThenToolsModel: turn 1 fails provider auth (the run pauses for key
// rotation and never closes); after RedriveLastTurn it asks for
// kenaz__alpha + kenaz__beta, then answers. Race-safe.
type authThenToolsModel struct {
	mu    sync.Mutex
	calls int
}

func (m *authThenToolsModel) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	switch n {
	case 1:
		return coreag.LLMResponse{}, &corellm.ErrProviderAuthFailed{Provider: "anthropic", ProfileID: "test-profile", ModelID: "m", Reason: "bad key"}
	case 2:
		return coreag.LLMResponse{
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-a", Name: "kenaz__alpha", Arguments: `{}`},
				{ID: "tu-b", Name: "kenaz__beta", Arguments: `{}`},
			},
		}, nil
	}
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "done"})
	}
	return coreag.LLMResponse{Content: "done", FinishReason: "stop"}, nil
}

// TestScheduledRunContainment_RedriveAfterKeyRotationStaysContained (review
// L4, pinned BEFORE the release fix): a contained model-created run pauses
// on a provider auth failure (no terminal event), the dispatcher times out,
// the user rotates the key and RedriveLastTurn re-runs the turn in the SAME
// session — the redrive is still contained: alpha dispatches, beta does not.
func TestScheduledRunContainment_RedriveAfterKeyRotationStaysContained(t *testing.T) {
	sandboxUserConfigDir(t)
	f := buildContainmentFixtureWith(t, t.TempDir(), &authThenToolsModel{}, 500*time.Millisecond)

	hist := f.fireOnce(t, scheduler.ChatRunRecord{
		ID:            "cr-redrive",
		CreatedBy:     scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist: []string{"kenaz__alpha"},
	})
	if hist.Status != "failed" || hist.SessionID == "" {
		t.Fatalf("history = %+v, want a timed-out failed run with a session", hist)
	}
	if !f.registry.Contained(hist.SessionID) {
		t.Fatal("timed-out (auth-paused) run's session was released while its turn can still be redriven")
	}

	if _, err := f.runner.RedriveLastTurn(context.Background(), "test-profile"); err != nil {
		t.Fatalf("RedriveLastTurn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(f.pool.snapshot()) == 0 && len(toolBlockedRows(t, f.blocked)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("redrive never reached tool dispatch")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Wait for both calls to resolve one way or the other.
	for len(f.pool.snapshot())+len(toolBlockedRows(t, f.blocked)) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("calls=%v blocked=%d", f.pool.snapshot(), len(toolBlockedRows(t, f.blocked)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.pool.snapshot(); len(got) != 1 || got[0] != "kenaz__alpha" {
		t.Fatalf("redriven turn dispatched %v, want only [kenaz__alpha] — the redrive escaped containment", got)
	}
	// Other side (L4 fix): once the redriven stream terminates, the run is
	// over and the session is released.
	waitReleased(t, f.registry, hist.SessionID)
}

func waitReleased(t *testing.T, reg *ScheduledRunContainmentRegistry, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for reg.Contained(sessionID) {
		if time.Now().After(deadline) {
			t.Fatalf("session %s still contained after its run's streams terminated", sessionID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// slowModel answers after a delay longer than the dispatcher's timeout.
type slowModel struct{ delay time.Duration }

func (m slowModel) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
		return coreag.LLMResponse{}, ctx.Err()
	}
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "late"})
	}
	return coreag.LLMResponse{Content: "late", FinishReason: "stop"}, nil
}

// TestScheduledRunContainment_TimedOutRunReleasedWhenItsStreamEnds (review
// L4): a run whose dispatcher times out stays contained while its stream
// may still be executing, and is released once that stream terminates — a
// user who later opens the "Scheduled:" session is not left contained.
// Mutation: drop releaseOnSessionTerminal from the timeout branch -> the
// session stays contained forever and this test fails.
func TestScheduledRunContainment_TimedOutRunReleasedWhenItsStreamEnds(t *testing.T) {
	sandboxUserConfigDir(t)
	f := buildContainmentFixtureWith(t, t.TempDir(), slowModel{delay: 1500 * time.Millisecond}, 300*time.Millisecond)
	hist := f.fireOnce(t, scheduler.ChatRunRecord{
		ID:            "cr-slow",
		CreatedBy:     scheduler.ScheduledRunCreatedByModel,
		ToolAllowlist: []string{"kenaz__alpha"},
	})
	if hist.Status != "failed" || !strings.Contains(hist.Error, "timed out") {
		t.Fatalf("history = %+v, want a timeout", hist)
	}
	if !f.registry.Contained(hist.SessionID) {
		t.Fatal("released at timeout, while the stream was still running")
	}
	waitReleased(t, f.registry, hist.SessionID)
}

// armOnce creates a due one-shot row and arms it WITHOUT waiting for it to
// finish (for tests that act while the run is in flight).
func (f *containmentFixture) armOnce(t *testing.T, rec scheduler.ChatRunRecord) {
	t.Helper()
	now := time.Now().UTC()
	runAt := now.Add(-time.Second)
	rec.Name, rec.PromptTemplate, rec.OutputSink = "containment "+rec.ID, "use your tools", "none"
	rec.Enabled, rec.TriggerKind, rec.RunAt = true, scheduler.TriggerKindOnce, &runAt
	rec.CreatedAt, rec.UpdatedAt = now, now
	if err := f.store.Create(context.Background(), rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := f.engine.Sync(context.Background(), rec.ID); err != nil {
		t.Fatalf("Sync: %v", err)
	}
}

// scheduledSessionID waits for the fired run's "Scheduled:" session.
func (f *containmentFixture) scheduledSessionID(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err := f.sessions.List(context.Background())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, s := range list {
			if strings.HasPrefix(s.Name, "Scheduled: ") && f.registry.Contained(s.ID) {
				return s.ID
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduled run's session never appeared contained")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// gatedFirstCallModel blocks its FIRST call (the scheduled stream) until
// release is closed; every later call (an interactive turn) answers at once.
type gatedFirstCallModel struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (m *gatedFirstCallModel) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()
	if n == 1 {
		select {
		case <-m.release:
		case <-ctx.Done():
			return coreag.LLMResponse{}, ctx.Err()
		}
	}
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "ok"})
	}
	return coreag.LLMResponse{Content: "ok", FinishReason: "stop"}, nil
}

// TestScheduledRunContainment_UnrelatedStreamInSessionDoesNotRelease is the
// re-review's probe, kept as a pin: while a contained scheduled stream is
// still running, a user opens the "Scheduled:" session and completes an
// interactive turn in it. That stream's end must NOT release containment
// (the scheduled stream would otherwise run on with the full catalogue);
// the scheduled stream's OWN end does.
func TestScheduledRunContainment_UnrelatedStreamInSessionDoesNotRelease(t *testing.T) {
	sandboxUserConfigDir(t)
	model := &gatedFirstCallModel{release: make(chan struct{})}
	f := buildContainmentFixtureWith(t, t.TempDir(), model, 10*time.Second)
	f.armOnce(t, scheduler.ChatRunRecord{ID: "cr-mid", CreatedBy: scheduler.ScheduledRunCreatedByModel, ToolAllowlist: []string{"kenaz__alpha"}})
	sid := f.scheduledSessionID(t)

	// The interactive turn, through the same production surfaces the chat
	// UI uses, in the SAME session, while the scheduled stream is blocked.
	ctx := context.Background()
	if _, err := f.sessions.AppendMessage(ctx, sid, "user", "hi, what are you doing?"); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if _, err := f.llm.StartStream(ctx, "test-profile", sid, ""); err != nil {
		t.Fatalf("interactive StartStream: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		model.mu.Lock()
		n := model.calls
		model.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("interactive turn never reached the model")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond) // let the interactive stream's terminal event land
	if !f.registry.Contained(sid) {
		t.Fatal("an unrelated interactive stream ending released the scheduled run's containment while the scheduled stream still runs")
	}

	close(model.release)
	hist := f.await(t, "cr-mid")
	if hist.Status != "completed" {
		t.Fatalf("scheduled run = %+v, want completed", hist)
	}
	waitReleased(t, f.registry, sid)
}

// TestScheduledRunContainment_RedriveBeforeTimeoutStaysContainedThenReleases
// (re-review (b), follow-up 4): the paused turn is redriven while the
// dispatcher is still waiting. The redrive is contained (alpha only) and
// its end releases containment from the dispatcher's own wait loop — well
// before the dispatcher's timeout. Mutation: make the wait loop release
// only on the dispatched sub id (ignore redrive links) -> no release before
// the timeout and this test fails.
func TestScheduledRunContainment_RedriveBeforeTimeoutStaysContainedThenReleases(t *testing.T) {
	sandboxUserConfigDir(t)
	const dispatchTimeout = 8 * time.Second
	f := buildContainmentFixtureWith(t, t.TempDir(), &authThenToolsModel{}, dispatchTimeout)
	start := time.Now()
	f.armOnce(t, scheduler.ChatRunRecord{ID: "cr-early-redrive", CreatedBy: scheduler.ScheduledRunCreatedByModel, ToolAllowlist: []string{"kenaz__alpha"}})
	sid := f.scheduledSessionID(t)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := f.runner.HasPausedSubFor("test-profile"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduled turn never paused for key rotation")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := f.runner.RedriveLastTurn(context.Background(), "test-profile"); err != nil {
		t.Fatalf("RedriveLastTurn: %v", err)
	}
	for len(f.pool.snapshot())+len(toolBlockedRows(t, f.blocked)) < 2 {
		if time.Now().After(deadline.Add(5 * time.Second)) {
			t.Fatalf("redrive calls=%v blocked=%d", f.pool.snapshot(), len(toolBlockedRows(t, f.blocked)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := f.pool.snapshot(); len(got) != 1 || got[0] != "kenaz__alpha" {
		t.Fatalf("redrive dispatched %v, want only [kenaz__alpha]", got)
	}
	for f.registry.Contained(sid) {
		if time.Since(start) > dispatchTimeout-time.Second {
			t.Fatal("redrive ended but containment was not released before the dispatcher timeout — the wait loop does not follow the run's own redrive")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.await(t, "cr-early-redrive") // the original sub never closes; the dispatcher times out
}
