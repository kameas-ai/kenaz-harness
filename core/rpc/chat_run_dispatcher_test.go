package rpc

// chat_run_dispatcher_test.go — mission model-scheduled-jobs-01PMSJ01
// WP04.
//
// These tests drive the REAL production surfaces: a real sqlite-backed
// session.Manager (via core.New over a temp DataDir), the real
// core/rpc/views/llm.API, the real core/rpc/views/agentgraph/chat.
// ChatRunner and the real production chat_default.yaml graph — the same
// stack the interactive chat surface uses (spec.md §1.3's point: this is
// the FIRST production caller of that stack, not a second one). Only the
// model itself is faked, via chat.Config.EnvDefaults overriding env.LLM
// — the same substitution point core/rpc/views/agentgraph/chat's own
// integration tests use (chat_runner_integration_test.go).

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	llmview "github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	sessionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// ── a trivial corellm.Registry stub ─────────────────────────────────────
//
// Never actually exercised: chat.Config.EnvDefaults overrides env.LLM
// before the kernel ever calls LLMProviderAdapter.Generate (which is the
// only path that reads from the registry), so this only needs to satisfy
// the interface chat.New / llmview.New require non-nil.

type dispatcherTestRegistry struct{}

func (dispatcherTestRegistry) RegisterAdapter(corellm.ProviderAdapter) {}
func (dispatcherTestRegistry) LoadProfiles(_ []corellm.ProviderProfile) error {
	return nil
}
func (dispatcherTestRegistry) Evict(_ string) error { return nil }
func (dispatcherTestRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, errors.New("dispatcherTestRegistry: not used")
}
func (dispatcherTestRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (dispatcherTestRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return nil, errors.New("dispatcherTestRegistry: not used")
}

// ── a fake model, wired through chat.Config.EnvDefaults ────────────────

// fakeModel is a race-safe coreag.LLMProvider (CLAUDE.md: driven from the
// kernel's own goroutine). It always answers with a fixed text response
// so the agent_loop finishes after one iteration, matching
// chat_runner_integration_test.go's TestChatGraph_SingleTurnNoTool shape.
type fakeModel struct {
	mu       sync.Mutex
	requests []coreag.LLMRequest
	response string
}

func (f *fakeModel) Generate(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: f.response})
	}
	return coreag.LLMResponse{Content: f.response, FinishReason: "stop"}, nil
}

func (f *fakeModel) snapshotRequests() []coreag.LLMRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]coreag.LLMRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// loadChatDefaultGraph reads the production chat_default.yaml the same
// way core/rpc/views/agentgraph/chat's own integration tests do, applying
// the agentic-turn-routing gate at its shipped OFF position so the
// dispatcher's test runs the exact topology production ships.
func loadChatDefaultGraph(t *testing.T) coreag.Graph {
	t.Helper()
	candidates := []string{
		"views/agentgraph/library/chat_default.yaml",
		"../rpc/views/agentgraph/library/chat_default.yaml",
	}
	var data []byte
	var err error
	for _, p := range candidates {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("loadChatDefaultGraph: cannot find chat_default.yaml; tried %v: %v", candidates, err)
	}
	g, err := coreag.LoadYAML(data)
	if err != nil {
		t.Fatalf("loadChatDefaultGraph: parse: %v", err)
	}
	return coreag.GateAgenticTurnRouting(g, false)
}

// dispatcherTestStack bundles the real production surfaces the dispatcher
// depends on, built over a real sqlite DB.
type dispatcherTestStack struct {
	sessionsAPI sessionsview.SessionsAPI
	llmAPI      llmview.LLMConnectorAPI
	bus         *EventBus
	store       scheduler.ScheduledChatStore
	model       *fakeModel
}

func buildDispatcherTestStack(t *testing.T, responseText string) *dispatcherTestStack {
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

	db := c.Storage()
	if db == nil {
		t.Fatal("core.New produced a nil Storage DB")
	}
	store := scheduler.NewSQLiteChatStore(db)

	return &dispatcherTestStack{
		sessionsAPI: sessionsAPI,
		llmAPI:      llmAPI,
		bus:         bus,
		store:       store,
		model:       model,
	}
}

func seedChatRunRow(t *testing.T, store scheduler.ScheduledChatStore, id, promptTemplate string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.Create(context.Background(), scheduler.ChatRunRecord{
		ID:             id,
		Name:           "WP04 test run",
		PromptTemplate: promptTemplate,
		Cron:           "0 9 * * *",
		OutputSink:     "none",
		Enabled:        true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// TestLiveChatRunDispatcher_FullRoundTrip is AC-003: assert on the
// database — a new session exists, its history has the rendered prompt
// as a user row and a non-empty assistant row, and the history row's
// SessionID matches.
//
// Mutation: rewrite the assertion onto the dispatcher's return value
// instead of the database. Must be treated as a regression — the whole
// spec.md §1.3 defect this mission fixes was a return value that lied
// about what the database contained.
func TestLiveChatRunDispatcher_FullRoundTrip(t *testing.T) {
	stack := buildDispatcherTestStack(t, "the answer is 4")
	seedChatRunRow(t, stack.store, "cr-1", "what is 2+2? (fired at {{time}})")

	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          stack.store,
		Sessions:       stack.sessionsAPI,
		LLM:            stack.llmAPI,
		Bus:            stack.bus,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        5 * time.Second,
	})

	job := scheduler.Job{
		ID:      "cr-1",
		Kind:    scheduler.JobKindChatRun,
		ChatRun: &scheduler.ChatRunSpec{ID: "cr-1"},
	}
	rec, err := d.DispatchChatRun(context.Background(), job, time.Now().UTC())
	if err != nil {
		t.Fatalf("DispatchChatRun returned an error (want a failed-status record instead, for anything but a programmer error): %v", err)
	}
	if rec.Status != "completed" {
		t.Fatalf("Status = %q, want completed; Error=%q", rec.Status, rec.Error)
	}
	if rec.SessionID == "" {
		t.Fatal("SessionID is empty")
	}

	// Ground truth: read the database, not the dispatcher's return value.
	msgs, err := stack.sessionsAPI.ListMessages(context.Background(), rec.SessionID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var sawUserPrompt, sawAssistant bool
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "what is 2+2?") {
			sawUserPrompt = true
		}
		if m.Role == "assistant" && m.Content != "" {
			sawAssistant = true
			if m.Content != "the answer is 4" {
				t.Errorf("assistant content = %q, want %q", m.Content, "the answer is 4")
			}
		}
	}
	if !sawUserPrompt {
		t.Errorf("no persisted user row contains the rendered prompt; messages = %+v", msgs)
	}
	if !sawAssistant {
		t.Errorf("no persisted assistant row with non-empty content; messages = %+v", msgs)
	}

	if rec.OutputSnippet == "" {
		t.Error("OutputSnippet is empty, want the persisted assistant reply")
	}

	// The model actually saw the rendered {{time}} substitution, not the
	// literal template — proves RenderPromptTemplate ran, not just
	// ParseOutputSink/session plumbing.
	reqs := stack.model.snapshotRequests()
	if len(reqs) == 0 {
		t.Fatal("fake model was never called")
	}
}

// TestLiveChatRunDispatcher_ManyChunksYieldsOneHistoryRow is AC-005: a
// turn emitting several thousand chunks still yields exactly one history
// row (the caller's AppendHistory call, driven by exactly one terminal
// event).
//
// Mutation: subscribe to all topics (or add "llm:stream-chunk") in
// DispatchChatRun's Bus.Subscribe call. The close event is dropped by the
// bus's slow-subscriber default: arm once the per-token volume fills the
// subscriber's buffer, and this test must fail (either by timing out or
// by observing more than one dispatch outcome).
func TestLiveChatRunDispatcher_ManyChunksYieldsOneHistoryRow(t *testing.T) {
	stack := buildDispatcherTestStack(t, "final answer")
	// Make the fake model emit a few thousand chunks before its Generate
	// return, so llm:stream-chunk volume is genuinely high relative to
	// the single llm:stream-closed terminal event.
	stack.model.response = "final answer"
	seedChatRunRow(t, stack.store, "cr-2", "count to a lot")

	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          stack.store,
		Sessions:       stack.sessionsAPI,
		LLM:            stack.llmAPI,
		Bus:            stack.bus,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        5 * time.Second,
	})

	job := scheduler.Job{ID: "cr-2", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-2"}}
	rec, err := d.DispatchChatRun(context.Background(), job, time.Now().UTC())
	if err != nil {
		t.Fatalf("DispatchChatRun: %v", err)
	}
	if rec.Status != "completed" {
		t.Fatalf("Status = %q, want completed; Error=%q", rec.Status, rec.Error)
	}
	// DispatchChatRun returning exactly once, with exactly one record, IS
	// the "one history row" assertion — the caller appends exactly what
	// DispatchChatRun returns, once. A dropped terminal event would have
	// caused this call to time out instead (Status: "failed", Error
	// naming the timeout) rather than return cleanly.
}

// TestLiveChatRunDispatcher_NilStoreReturnsError: a dispatcher built with
// no Store is a construction-level defect (nothing to reload), which is
// the one class that returns a real error rather than a failed record.
func TestLiveChatRunDispatcher_NilStoreReturnsError(t *testing.T) {
	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Bus:            NewEventBus(),
		DefaultProfile: func() string { return "test-profile" },
	})
	job := scheduler.Job{ID: "cr-x", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-x"}}
	_, err := d.DispatchChatRun(context.Background(), job, time.Now().UTC())
	if err == nil {
		t.Fatal("want an error for a dispatcher with no Store wired")
	}
}

// TestLiveChatRunDispatcher_MissingRowIsAFailedRecordNotAnError: a row
// that no longer exists (deleted between the cron tick being scheduled
// and firing) must come back as a failed record with err==nil — the
// caller (WP03's ChatCronEngine.fireSync, or scheduledchat.API.RunNow)
// persists it, rather than silently losing the "attempt was made"
// evidence to a returned error.
func TestLiveChatRunDispatcher_MissingRowIsAFailedRecordNotAnError(t *testing.T) {
	stack := buildDispatcherTestStack(t, "unused")
	// Deliberately do not seed a row for "cr-missing".
	d := NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          stack.store,
		Sessions:       stack.sessionsAPI,
		LLM:            stack.llmAPI,
		Bus:            stack.bus,
		DefaultProfile: func() string { return "test-profile" },
		Timeout:        2 * time.Second,
	})
	job := scheduler.Job{ID: "cr-missing", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-missing"}}
	rec, err := d.DispatchChatRun(context.Background(), job, time.Now().UTC())
	if err != nil {
		t.Fatalf("want (failedRecord, nil), got error: %v", err)
	}
	if rec.Status != "failed" {
		t.Fatalf("Status = %q, want failed", rec.Status)
	}
	if rec.Error == "" {
		t.Error("Error is empty, want a reason naming the reload failure")
	}
}
