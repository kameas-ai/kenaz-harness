package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/session"
	autotitle "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle"
)

// These integration tests load the production chat_default.yaml graph
// and exercise the runtime end-to-end so any topology drift in the
// chat surface is caught immediately. The previous hand-built test
// topologies left the door open to "the production YAML is shaped
// differently than the test fixture" regressions; mission
// tool-dispatch-node cuts that drift surface by reading the canonical
// graph from disk on every test.

// stubLLM is a programmable LLMProvider for chat-runner integration tests.
// Each Generate call pops the next pre-staged response off the queue and
// emits the supplied stream events into the kernel-bound StreamSink.
type stubLLM struct {
	mu        sync.Mutex
	responses []stubLLMResponse
	// requests records what was actually asked, so a test can assert on
	// the composed prompt and not just the call count. Mutex-guarded
	// and read only through snapshotRequests — Generate is called from
	// the kernel's dispatch goroutines (CLAUDE.md "Race-safe test
	// fakes").
	requests []coreag.LLMRequest
	calls    atomic.Int32
}

type stubLLMResponse struct {
	stream []coreag.StreamEvent
	resp   coreag.LLMResponse
	err    error
}

func (s *stubLLM) push(r stubLLMResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responses = append(s.responses, r)
}

func (s *stubLLM) snapshotRequests() []coreag.LLMRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]coreag.LLMRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *stubLLM) Generate(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
	s.calls.Add(1)
	s.mu.Lock()
	s.requests = append(s.requests, req)
	if len(s.responses) == 0 {
		s.mu.Unlock()
		return coreag.LLMResponse{}, errors.New("stubLLM: out of responses")
	}
	r := s.responses[0]
	s.responses = s.responses[1:]
	s.mu.Unlock()
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		for _, ev := range r.stream {
			sink.Emit(ev)
		}
	}
	return r.resp, r.err
}

// stubTools is a programmable ToolRegistry. Has() answers from the
// allowlist; Call() pops queued results.
type stubTools struct {
	mu       sync.Mutex
	allowed  map[string]bool
	results  []coreag.ToolResult
	calls    []coreag.ToolCall
	callsErr error
}

func newStubTools(names ...string) *stubTools {
	allowed := map[string]bool{}
	for _, n := range names {
		allowed[n] = true
	}
	return &stubTools{allowed: allowed}
}

func (s *stubTools) push(r coreag.ToolResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results = append(s.results, r)
}

// snapshotCalls is the race-safe read of what was dispatched. Call()
// runs on the kernel's tool-dispatch goroutines, so the test body must
// never touch s.calls directly (CLAUDE.md "Race-safe test fakes").
func (s *stubTools) snapshotCalls() []coreag.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]coreag.ToolCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubTools) Has(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.allowed[name]
}

func (s *stubTools) Call(_ context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
	if s.callsErr != nil {
		return coreag.ToolResult{}, s.callsErr
	}
	if len(s.results) == 0 {
		return coreag.ToolResult{Content: "ok"}, nil
	}
	r := s.results[0]
	s.results = s.results[1:]
	return r, nil
}

// loadProductionChatGraph reads core/rpc/views/agentgraph/library/chat_default.yaml
// and returns the parsed Graph AS PRODUCTION RUNS IT — i.e. with the
// agentic-turn-routing gate applied at its shipped position, which is
// off (agentgraph-total-convergence-01PMGX01 WP11b). The path is
// resolved from the test's CWD (which is the chat package directory) up
// two levels into the library directory; failure to locate the bundled
// file is a hard test failure because the whole point of these
// integration tests is to verify the production graph topology.
//
// Applying the gate here rather than reading the raw YAML is the whole
// point: after WP11b the authored file carries the routed turn and the
// GraphLoader strips it, so a test that read the file directly would be
// asserting against a graph no user runs. loadRoutedChatGraph is the
// other position.
func loadProductionChatGraph(t *testing.T) coreag.Graph {
	t.Helper()
	return coreag.GateAgenticTurnRouting(loadChatGraphYAML(t), false)
}

// loadRoutedChatGraph is loadProductionChatGraph with the flag ON: the
// routed turn, with `route` in the agent loop and the `exit_gate`
// review node between the loop and the writer.
func loadRoutedChatGraph(t *testing.T) coreag.Graph {
	t.Helper()
	return coreag.GateAgenticTurnRouting(loadChatGraphYAML(t), true)
}

func loadChatGraphYAML(t *testing.T) coreag.Graph {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "library", "chat_default.yaml"),
		filepath.Join("library", "chat_default.yaml"),
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
		t.Fatalf("loadProductionChatGraph: cannot find chat_default.yaml; tried %v: %v", candidates, err)
	}
	g, err := coreag.LoadYAML(data)
	if err != nil {
		t.Fatalf("loadProductionChatGraph: parse: %v", err)
	}
	return g
}

// buildIntegrationRunner wires a ChatRunner with the supplied stubs +
// recorders. Returns the runner, broker, and writer for assertions.
// Loads the real chat_default.yaml so any topology regression bites the
// test immediately.
func buildIntegrationRunner(
	t *testing.T,
	llm *stubLLM,
	tools *stubTools,
	maxTurns int,
	graphHistory []coreag.Message,
) (*ChatRunner, *recordingBroker, *recordingHistoryWriter) {
	graph := loadProductionChatGraph(t)
	return buildIntegrationRunnerWithGraph(t, llm, tools, maxTurns, graphHistory, graph)
}

func buildIntegrationRunnerWithGraph(
	t *testing.T,
	llm *stubLLM,
	tools *stubTools,
	maxTurns int,
	graphHistory []coreag.Message,
	graph coreag.Graph,
) (*ChatRunner, *recordingBroker, *recordingHistoryWriter) {
	t.Helper()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{msgs: graphHistory},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return maxTurns },
		EnvDefaults: func(env *coreag.Env) {
			// Override the registry-backed adapters with stubs so the
			// kernel run uses pre-staged responses + tool results.
			if llm != nil {
				env.LLM = llm
			}
			if tools != nil {
				env.Tools = tools
			}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, writer
}

// waitForClosed polls the broker for an llm:stream-closed payload up to
// 2s; returns the payload (or fails the test).
func waitForClosed(t *testing.T, broker *recordingBroker) StreamClosedPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			if e.topic == "llm:stream-closed" {
				return e.payload.(StreamClosedPayload)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not see llm:stream-closed within 2s; events = %+v", broker.snapshot())
	return StreamClosedPayload{}
}

// TestChatGraph_SingleTurnNoTool: stub LLM emits text-only, no tool_use
// against the production chat_default.yaml. Asserts that the agent_loop
// terminates after one iteration (text-only finishes the loop) and that
// the assistant message lands on the broker + history writer.
func TestChatGraph_SingleTurnNoTool(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "hello "},
			{Kind: coreag.StreamEventText, Text: "world"},
		},
		resp: coreag.LLMResponse{
			Content:      "hello world",
			FinishReason: "stop",
		},
	})
	runner, broker, writer := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "say hi"},
	})

	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "say hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if !strings.HasPrefix(subID, "chat-") {
		t.Errorf("subID = %q, want chat-N prefix", subID)
	}

	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Errorf("Reason = %q, want completed-style; msg=%q", closed.Reason, closed.Message)
	}

	// Single LLM call: text-only finishes the agent_loop on iter 1.
	if got := llm.calls.Load(); got != 1 {
		t.Errorf("llm.calls = %d, want 1 (single text-only turn)", got)
	}

	// Assert text deltas streamed in order.
	events := broker.snapshot()
	var texts []string
	for _, e := range events {
		if e.topic != "llm:stream-chunk" {
			continue
		}
		chunk := e.payload.(StreamChunkPayload)
		if chunk.Chunk.Kind == corellm.StreamText {
			texts = append(texts, chunk.Chunk.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "hello " || texts[1] != "world" {
		t.Errorf("text deltas = %+v, want [hello , world]", texts)
	}

	// Assert assistant persistence (writer.calls[0] is the user-turn
	// append done by StartStream; calls[len-1] is the SessionWriteNode
	// assistant turn).
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) < 2 {
		t.Fatalf("len(writer.calls) = %d, want >=2 (user turn + assistant turn); calls=%+v", len(writer.calls), writer.calls)
	}
	assistantCall := writer.calls[len(writer.calls)-1]
	if assistantCall.role != "assistant" {
		t.Errorf("last writer call role = %q, want assistant", assistantCall.role)
	}
	if !strings.Contains(assistantCall.content, "hello world") {
		t.Errorf("assistant content = %q, want 'hello world'", assistantCall.content)
	}
}

// TestChatGraph_MultiTurnAgentLoop: stub LLM emits tool_use → stub Tool
// returns result → next iteration → text-only finish, against the real
// chat_default.yaml. Asserts:
//   - LLM called twice (tool_use turn + finishing turn)
//   - tool_dispatch fires the registered tool exactly once
//   - the final assistant message persists
func TestChatGraph_MultiTurnAgentLoop(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	// First call: tool_use response
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "search__web", ToolArgs: `{"q":"hello"}`},
		},
		resp: coreag.LLMResponse{
			Content:      "",
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-1", Name: "search__web", Arguments: `{"q":"hello"}`},
			},
		},
	})
	// Second call: text-only finish
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "found 42"},
		},
		resp: coreag.LLMResponse{
			Content:      "found 42",
			FinishReason: "stop",
		},
	})
	tools := newStubTools("search__web")
	tools.push(coreag.ToolResult{Content: `{"result":"42"}`, IsError: false})

	runner, broker, writer := buildIntegrationRunner(t, llm, tools, 25, []coreag.Message{
		{Role: "user", Content: "search hello"},
	})
	_, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "search hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Errorf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	// LLM should have been called exactly twice (tool_use then finish).
	if got := llm.calls.Load(); got != 2 {
		t.Errorf("llm.calls = %d, want 2 (tool_use turn + finish turn)", got)
	}
	// Tool should have been called exactly once.
	tools.mu.Lock()
	calls := append([]coreag.ToolCall(nil), tools.calls...)
	tools.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("len(tools.calls) = %d, want 1; calls=%+v", len(calls), calls)
	}
	if calls[0].Name != "search__web" {
		t.Errorf("tool name = %q, want search__web", calls[0].Name)
	}

	// The final assistant turn ("found 42") should be persisted.
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) < 2 {
		t.Fatalf("len(writer.calls) = %d, want >=2; calls=%+v", len(writer.calls), writer.calls)
	}
	assistantCall := writer.calls[len(writer.calls)-1]
	if assistantCall.role != "assistant" {
		t.Errorf("last writer call role = %q, want assistant", assistantCall.role)
	}
	if !strings.Contains(assistantCall.content, "found 42") {
		t.Errorf("assistant content = %q, want 'found 42'", assistantCall.content)
	}
}

// TestChatGraph_CapHitAtMaxAgentTurns: stub LLM keeps emitting tool_use;
// with MaxAgentTurns=3, asserts the LoopNode hits the cap. The per-run
// cap is enforced by the chat_runner.applyMaxTurnsDial walk + the kernel
// LoopNode max_iterations bound. Verifies the run terminates rather
// than streaming forever, and that LLM call count stays bounded.
func TestChatGraph_CapHitAtMaxAgentTurns(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	// Stage many tool_use responses so the loop would never exit
	// without the cap.
	for i := 0; i < 50; i++ {
		llm.push(stubLLMResponse{
			stream: []coreag.StreamEvent{
				{Kind: coreag.StreamEventTool, ToolID: "tu-X", ToolName: "echo__one", ToolArgs: `{}`},
			},
			resp: coreag.LLMResponse{
				FinishReason: "tool_use",
				ToolCalls: []coreag.ToolCallRequest{
					{ID: "tu-X", Name: "echo__one", Arguments: `{}`},
				},
			},
		})
	}
	tools := newStubTools("echo__one")
	for i := 0; i < 50; i++ {
		tools.push(coreag.ToolResult{Content: "ok", IsError: false})
	}

	runner, broker, _ := buildIntegrationRunner(t, llm, tools, 3, []coreag.Message{
		{Role: "user", Content: "loop forever"},
	})
	_, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "loop forever")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "" {
		t.Errorf("expected non-empty close reason; got payload = %+v", closed)
	}
	// Critically: we must NOT have called the LLM 50 times. With a cap
	// of 3 iterations the LoopNode body fires at most 3 times, each
	// firing one LLMNode call. Allow a small overhead for any pre-loop
	// pumping the chassis might add.
	calls := llm.calls.Load()
	if calls > 6 {
		t.Errorf("llm.calls = %d, want <=6 (cap=3 with overhead); did the cap fire?", calls)
	}
	if calls < 1 {
		t.Errorf("llm.calls = %d, want >=1; loop body never ran?", calls)
	}
}

// TestChatGraph_StreamingOrder asserts the streaming deltas the
// LLMNode pumps reach the broker in arrival order. The broker bridges
// to the existing llm:stream-chunk topic; the production frontend
// useSession.ts depends on byte-equal chunk order so this is a pin
// against any reordering inside the chat runner.
func TestChatGraph_StreamingOrder(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "alpha "},
			{Kind: coreag.StreamEventText, Text: "beta "},
			{Kind: coreag.StreamEventText, Text: "gamma "},
			{Kind: coreag.StreamEventText, Text: "delta"},
			{Kind: coreag.StreamEventFinish, Finish: "stop"},
		},
		resp: coreag.LLMResponse{
			Content:      "alpha beta gamma delta",
			FinishReason: "stop",
		},
	})
	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "stream please"},
	})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "stream please"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Errorf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	want := []string{"alpha ", "beta ", "gamma ", "delta"}
	var got []string
	for _, e := range broker.snapshot() {
		if e.topic != "llm:stream-chunk" {
			continue
		}
		chunk := e.payload.(StreamChunkPayload)
		if chunk.Chunk.Kind == corellm.StreamText {
			got = append(got, chunk.Chunk.Text)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("text deltas: got=%v want=%v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("delta[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestChatRunnerIntegration_ProviderError: stub LLM returns an error
// mid-stream. Asserts llm:stream-closed surfaces with reason=backend-error.
func TestChatRunnerIntegration_ProviderError(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "partial..."},
			{Kind: coreag.StreamEventError, ErrMsg: "transient"},
		},
		err: errors.New("transient backend error"),
	})

	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "fail me"},
	})
	_, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "fail me")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Errorf("Reason = %q, want backend-error; msg=%q", closed.Reason, closed.Message)
	}
	if closed.Message == "" {
		t.Errorf("expected non-empty error message in close payload; got %+v", closed)
	}
}

// _ ensures the integration tests compile when chat package is imported
// elsewhere (smoke check).
var _ = json.RawMessage(nil)

// -- WP06 (session-auto-titling-01KQ8TDS) --
// The four scenarios below pin the chat-runner auto-title trigger
// against the real chat_default.yaml so any drift in the post-run
// firing rules, the predicate-recheck race guard, or the audit-emit
// surface fails loudly.
//
// All four share three fakes:
//   - fakeAutoTitleManager: in-memory session.Record + message log;
//     enforces the same auto_titled / placeholder-name / supersede
//     semantics as the production sqlStore.
//   - fakeAutoTitleGenerator: pre-staged response queue (string or
//     err); records every Call.
//   - fakeAutoTitleAudit: captures every Emit so the failure-path
//     test can assert the audit row landed with error_kind set.

// fakeAutoTitleManager is a hand-built session.Manager surface that
// satisfies the chat package's AutoTitleManager interface. It mirrors
// just the slice of session.Manager the trigger touches: Get,
// ListMessagesActive, AutoTitle, MarkAutoTitleAttempted.
type fakeAutoTitleManager struct {
	mu        sync.Mutex
	rec       session.Record
	msgs      []session.Message
	autoCalls int
	markCalls int
	autoErr   error
}

func (f *fakeAutoTitleManager) Get(_ context.Context, id string) (session.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rec.ID != id {
		return session.Record{}, session.ErrSessionNotFound
	}
	// Return a copy so callers can't mutate internal state.
	r := f.rec
	return r, nil
}

func (f *fakeAutoTitleManager) ListMessagesActive(_ context.Context, _ string) ([]session.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]session.Message, len(f.msgs))
	copy(out, f.msgs)
	return out, nil
}

func (f *fakeAutoTitleManager) AutoTitle(_ context.Context, id, title string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.autoCalls++
	if f.autoErr != nil {
		return f.autoErr
	}
	if f.rec.ID != id {
		return session.ErrSessionNotFound
	}
	if f.rec.AutoTitled {
		return session.ErrAutoTitleSuperseded
	}
	f.rec.Name = title
	f.rec.AutoTitled = true
	return nil
}

func (f *fakeAutoTitleManager) MarkAutoTitleAttempted(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls++
	if f.rec.ID != id {
		return session.ErrSessionNotFound
	}
	f.rec.AutoTitled = true
	return nil
}

func (f *fakeAutoTitleManager) snapshot() session.Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rec
}

func (f *fakeAutoTitleManager) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec.AutoTitled = false
	f.rec.Name = ""
	f.autoCalls = 0
	f.markCalls = 0
}

// fakeAutoTitleGenerator returns a queued title or error per Call.
type fakeAutoTitleGenerator struct {
	mu    sync.Mutex
	queue []autoTitleResp
	calls int
}

type autoTitleResp struct {
	title string
	err   error
}

func (g *fakeAutoTitleGenerator) push(r autoTitleResp) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.queue = append(g.queue, r)
}

func (g *fakeAutoTitleGenerator) GenerateTitle(_ context.Context, _ autotitle.Transcript) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls++
	if len(g.queue) == 0 {
		return "", errors.New("fakeAutoTitleGenerator: queue exhausted")
	}
	r := g.queue[0]
	g.queue = g.queue[1:]
	return r.title, r.err
}

func (g *fakeAutoTitleGenerator) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// fakeAutoTitleAudit records every Emit.
type fakeAutoTitleAudit struct {
	mu      sync.Mutex
	emitted []auditEmission
}

type auditEmission struct {
	kind    string
	payload map[string]any
}

func (a *fakeAutoTitleAudit) Emit(_ context.Context, kind string, payload map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make(map[string]any, len(payload))
	for k, v := range payload {
		cp[k] = v
	}
	a.emitted = append(a.emitted, auditEmission{kind: kind, payload: cp})
}

func (a *fakeAutoTitleAudit) snapshot() []auditEmission {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]auditEmission, len(a.emitted))
	copy(out, a.emitted)
	return out
}

// buildAutoTitleRunner extends buildIntegrationRunner with a wired
// AutoTitleDeps. The session id used by all four WP06 scenarios is
// "session-1"; the manager fake is seeded with a single user-assistant
// pair so the predicate has the assistant message it needs.
func buildAutoTitleRunner(
	t *testing.T,
	llm *stubLLM,
	mgr *fakeAutoTitleManager,
	gen *fakeAutoTitleGenerator,
	audit *fakeAutoTitleAudit,
	enabled func() bool,
) (*ChatRunner, *recordingBroker, *recordingHistoryWriter) {
	t.Helper()
	graph := loadProductionChatGraph(t)
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "what's a good way to learn rust?"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			if llm != nil {
				env.LLM = llm
			}
			env.Tools = newStubTools()
		},
		AutoTitle: &AutoTitleDeps{
			Manager:          mgr,
			Generator:        gen,
			Audit:            audit,
			EffectiveEnabled: enabled,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, writer
}

// seedSession primes the fake manager with a placeholder-named session
// and a one-turn user/assistant transcript. Returns the same manager
// for chaining.
func seedSession(mgr *fakeAutoTitleManager, sessionID string) *fakeAutoTitleManager {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.rec = session.Record{
		ID:         sessionID,
		Name:       "",
		AutoTitled: false,
	}
	mgr.msgs = []session.Message{
		{ID: "m-1", SessionID: sessionID, Sequence: 1, Role: session.RoleUser, Content: "what's a good way to learn rust?"},
		{ID: "m-2", SessionID: sessionID, Sequence: 2, Role: session.RoleAssistant, Content: "start with the official rust book and work through the examples."},
	}
	return mgr
}

// waitForAutoTitleAttempt polls until the auto-title trigger has both
// (a) touched the manager — AutoTitle or MarkAutoTitleAttempted — and
// (b) recorded wantEmissions audit rows. Returns true when both hold;
// false on timeout. Pass wantEmissions = 0 to wait on the manager only.
//
// THE AUDIT LEG IS WHY THIS EXISTS (autonomy-knobs review; fixed by
// 01PMGX01 WP17). The trigger runs on its own goroutine and, on every
// path, calls the manager BEFORE emitting the audit row:
//
//	deps.Manager.MarkAutoTitleAttempted(ctx, sessionID)   // autotitle.go
//	deps.Audit.Emit(ctx, session.EventKindSessionAutoTitled, ...)
//
// Waiting on the manager alone therefore unblocks the test body in the
// window BETWEEN those two statements. Every caller that then reads
// audit.snapshot() and asserts a count was racing the emit — a real
// flake, not a theoretical one, and one that fails more often under
// -race (which is what CI runs) because the scheduler interleaves more.
//
// Callers must pass the number of emissions the scenario expects, so
// the wait covers the assertion that follows it rather than an
// unrelated earlier one.
func waitForAutoTitleAttempt(mgr *fakeAutoTitleManager, audit *fakeAutoTitleAudit, wantEmissions int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		fired := mgr.autoCalls > 0 || mgr.markCalls > 0
		mgr.mu.Unlock()
		if fired && (audit == nil || len(audit.snapshot()) >= wantEmissions) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// TestChatRunnerIntegration_AutoTitle_HappyPath: stub LLM emits a
// text-only assistant turn; the auto-title generator returns
// "Learning Rust"; the manager rec ends up with the new name and
// auto_titled=1, exactly one generator Call, exactly one AutoTitle
// write, and one audit emission.
func TestChatRunnerIntegration_AutoTitle_HappyPath(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "start with the rust book."},
		},
		resp: coreag.LLMResponse{Content: "start with the rust book.", FinishReason: "stop"},
	})

	mgr := seedSession(&fakeAutoTitleManager{}, "session-1")
	gen := &fakeAutoTitleGenerator{}
	gen.push(autoTitleResp{title: "Learning Rust"})
	audit := &fakeAutoTitleAudit{}

	runner, broker, _ := buildAutoTitleRunner(t, llm, mgr, gen, audit, nil)
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "what's a good way to learn rust?"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	if !waitForAutoTitleAttempt(mgr, audit, 1) {
		t.Fatalf("auto-title trigger did not fire within 2s")
	}

	rec := mgr.snapshot()
	if rec.Name != "Learning Rust" {
		t.Errorf("rec.Name = %q, want Learning Rust", rec.Name)
	}
	if !rec.AutoTitled {
		t.Errorf("rec.AutoTitled = false, want true")
	}
	if got := gen.callCount(); got != 1 {
		t.Errorf("generator calls = %d, want 1", got)
	}

	emitted := audit.snapshot()
	if len(emitted) != 1 {
		t.Fatalf("audit emissions = %d, want 1; got %+v", len(emitted), emitted)
	}
	if emitted[0].kind != session.EventKindSessionAutoTitled {
		t.Errorf("audit kind = %q, want %q", emitted[0].kind, session.EventKindSessionAutoTitled)
	}
	if got, _ := emitted[0].payload["title"].(string); got != "Learning Rust" {
		t.Errorf("audit payload title = %q, want Learning Rust", got)
	}
	if _, hasErr := emitted[0].payload["error_kind"]; hasErr {
		t.Errorf("happy-path emission should not carry error_kind; got %+v", emitted[0].payload)
	}
}

// TestChatRunnerIntegration_AutoTitle_DialOff: EffectiveEnabled
// returns false → trigger short-circuits → no generator call, no
// session rename, no audit emission.
func TestChatRunnerIntegration_AutoTitle_DialOff(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "ok."},
		},
		resp: coreag.LLMResponse{Content: "ok.", FinishReason: "stop"},
	})

	mgr := seedSession(&fakeAutoTitleManager{}, "session-1")
	gen := &fakeAutoTitleGenerator{}
	gen.push(autoTitleResp{title: "Should Not Be Called"})
	audit := &fakeAutoTitleAudit{}

	runner, broker, _ := buildAutoTitleRunner(t, llm, mgr, gen, audit, func() bool { return false })
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "hi"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	_ = waitForClosed(t, broker)

	// Give the goroutine a moment to short-circuit before asserting.
	time.Sleep(100 * time.Millisecond)

	rec := mgr.snapshot()
	if rec.Name != "" {
		t.Errorf("rec.Name = %q, want empty (dial off should not rename)", rec.Name)
	}
	if rec.AutoTitled {
		t.Errorf("rec.AutoTitled = true, want false (dial off should not flip flag)")
	}
	if got := gen.callCount(); got != 0 {
		t.Errorf("generator calls = %d, want 0 when dial off", got)
	}
	if got := audit.snapshot(); len(got) != 0 {
		t.Errorf("audit emissions = %d, want 0 when dial off; got %+v", len(got), got)
	}
}

// TestChatRunnerIntegration_AutoTitle_GeneratorFailure: generator
// returns an error → MarkAutoTitleAttempted runs → rec name stays
// empty but AutoTitled flips to true → audit emission carries
// error_kind so dashboards see the attempt.
func TestChatRunnerIntegration_AutoTitle_GeneratorFailure(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "ok."},
		},
		resp: coreag.LLMResponse{Content: "ok.", FinishReason: "stop"},
	})

	mgr := seedSession(&fakeAutoTitleManager{}, "session-1")
	gen := &fakeAutoTitleGenerator{}
	gen.push(autoTitleResp{err: errors.New("provider unavailable")})
	audit := &fakeAutoTitleAudit{}

	runner, broker, _ := buildAutoTitleRunner(t, llm, mgr, gen, audit, nil)
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "hi"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	_ = waitForClosed(t, broker)

	if !waitForAutoTitleAttempt(mgr, audit, 1) {
		t.Fatalf("auto-title trigger did not fire within 2s")
	}

	rec := mgr.snapshot()
	if rec.Name != "" {
		t.Errorf("rec.Name = %q, want empty on failure path", rec.Name)
	}
	if !rec.AutoTitled {
		t.Errorf("rec.AutoTitled = false, want true (MarkAutoTitleAttempted should flip the flag on failure)")
	}
	if got := gen.callCount(); got != 1 {
		t.Errorf("generator calls = %d, want 1", got)
	}

	emitted := audit.snapshot()
	if len(emitted) != 1 {
		t.Fatalf("audit emissions = %d, want 1; got %+v", len(emitted), emitted)
	}
	if emitted[0].kind != session.EventKindSessionAutoTitled {
		t.Errorf("audit kind = %q, want %q", emitted[0].kind, session.EventKindSessionAutoTitled)
	}
	errKind, _ := emitted[0].payload["error_kind"].(string)
	if errKind == "" {
		t.Errorf("audit payload error_kind missing on failure path; got %+v", emitted[0].payload)
	}
	if title, ok := emitted[0].payload["title"]; ok && title != "" {
		t.Errorf("audit payload title set on failure path: %v; want absent or empty", title)
	}
}

// TestChatRunnerIntegration_AutoTitle_ManualReTrigger: simulate the
// "Suggest new title" header affordance — first run auto-titles to
// "Initial Title", user clears via ClearTitle (mgr.reset), then a
// second chat run fires a fresh trigger with a new generator response.
// Asserts the second run lands the second title and emits a second
// audit row.
func TestChatRunnerIntegration_AutoTitle_ManualReTrigger(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "first reply."},
		},
		resp: coreag.LLMResponse{Content: "first reply.", FinishReason: "stop"},
	})
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "second reply."},
		},
		resp: coreag.LLMResponse{Content: "second reply.", FinishReason: "stop"},
	})

	mgr := seedSession(&fakeAutoTitleManager{}, "session-1")
	gen := &fakeAutoTitleGenerator{}
	gen.push(autoTitleResp{title: "Initial Title"})
	gen.push(autoTitleResp{title: "Refreshed Title"})
	audit := &fakeAutoTitleAudit{}

	runner, broker, _ := buildAutoTitleRunner(t, llm, mgr, gen, audit, nil)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "first turn"); err != nil {
		t.Fatalf("StartStream #1: %v", err)
	}
	_ = waitForClosed(t, broker)
	if !waitForAutoTitleAttempt(mgr, audit, 1) {
		t.Fatalf("first auto-title did not fire")
	}
	if rec := mgr.snapshot(); rec.Name != "Initial Title" || !rec.AutoTitled {
		t.Fatalf("first run rec = %+v, want name=Initial Title autoTitled=true", rec)
	}

	// Simulate Sessions_ClearTitle: name back to "", auto_titled back
	// to false. (mgr.reset zeros the counters too so the second run's
	// assertions are clean.)
	mgr.reset()

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "second turn"); err != nil {
		t.Fatalf("StartStream #2: %v", err)
	}
	_ = waitForClosed(t, broker)
	// mgr.reset() zeroes the manager counters but NOT the audit
	// recorder, so the second run's target is the cumulative 2.
	if !waitForAutoTitleAttempt(mgr, audit, 2) {
		t.Fatalf("second auto-title did not fire after clear")
	}
	rec := mgr.snapshot()
	if rec.Name != "Refreshed Title" {
		t.Errorf("post-clear rec.Name = %q, want Refreshed Title", rec.Name)
	}
	if !rec.AutoTitled {
		t.Errorf("post-clear rec.AutoTitled = false, want true")
	}
	if got := gen.callCount(); got != 2 {
		t.Errorf("generator calls = %d, want 2 across both runs", got)
	}
	if got := audit.snapshot(); len(got) != 2 {
		t.Errorf("audit emissions = %d, want 2 (one per run)", len(got))
	}
}

// ── WP06 (provider-keychain-rotation-01KQ8TD9) ──────────────────────────────
//
// The three scenarios below pin the auth-failure interception path against
// the real chat_default.yaml:
//
//  1. authFailureThenRedrive — first Generate returns *ErrProviderAuthFailed;
//     broker emits "provider:auth-failed" (NOT "llm:stream-closed");
//     HasPausedSubFor returns true; RedriveLastTurn completes the second run.
//
//  2. featureFlagOff — same error but HARNESS_KEYCHAIN_ROTATION=off;
//     asserts the stream closes with reason=backend-error (old behaviour).
//
//  3. redriveDedupe — RedriveLastTurn called twice; second call returns an
//     error (no paused turn) but does NOT panic or corrupt runner state.

// waitForAuthFailed polls the broker for a "provider:auth-failed" payload up
// to 2s; returns the payload (or fails the test).
func waitForAuthFailed(t *testing.T, broker *recordingBroker) AuthFailedPayload {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			if e.topic == "provider:auth-failed" {
				return e.payload.(AuthFailedPayload)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not see provider:auth-failed within 2s; events = %+v", broker.snapshot())
	return AuthFailedPayload{}
}

// hasStreamClosed returns true iff the broker saw an llm:stream-closed event.
func hasStreamClosed(broker *recordingBroker) bool {
	for _, e := range broker.snapshot() {
		if e.topic == "llm:stream-closed" {
			return true
		}
	}
	return false
}

// TestChatRunnerIntegration_KeyRotation_AuthFailureThenRedrive:
//  1. LLM returns *ErrProviderAuthFailed on the first Generate call.
//  2. ChatRunner pauses: broker emits "provider:auth-failed"; no
//     "llm:stream-closed" yet; HasPausedSubFor returns true.
//  3. RedriveLastTurn is called (simulating the rotation flow); the second
//     LLM response completes successfully.
//  4. "llm:stream-closed" is finally emitted with a non-error reason.
func TestChatRunnerIntegration_KeyRotation_AuthFailureThenRedrive(t *testing.T) {
	// t.Setenv must be called before t.Parallel per Go testing rules.
	t.Setenv(envKeychainRotationVar, "on")

	llm := &stubLLM{}
	// First Generate: return *ErrProviderAuthFailed.
	authErr := &corellm.ErrProviderAuthFailed{
		Provider:  "anthropic",
		ProfileID: "prof-1",
		ModelID:   "claude-3-5-haiku",
		Reason:    "invalid api key",
		Cause:     &corellm.ErrAuth{Status: 401, Message: "invalid api key"},
	}
	llm.push(stubLLMResponse{err: authErr})
	// Second Generate: successful text response.
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "hello after rotation"},
		},
		resp: coreag.LLMResponse{
			Content:      "hello after rotation",
			FinishReason: "stop",
		},
	})

	runner, broker, writer := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "hello"},
	})

	_, err := runner.StartStream(context.Background(), "prof-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	// Step 2: verify auth-failure broker event.
	payload := waitForAuthFailed(t, broker)
	if payload.ProfileID != "prof-1" {
		t.Errorf("AuthFailedPayload.ProfileID = %q, want prof-1", payload.ProfileID)
	}
	if payload.Provider != "anthropic" {
		t.Errorf("AuthFailedPayload.Provider = %q, want anthropic", payload.Provider)
	}
	// llm:stream-closed must NOT have been emitted at this point.
	if hasStreamClosed(broker) {
		t.Errorf("llm:stream-closed was emitted before redrive; broker = %+v", broker.snapshot())
	}

	// HasPausedSubFor returns the sub_id (chat-N) assigned to the original run.
	token, ok := runner.HasPausedSubFor("prof-1")
	if !ok {
		t.Fatalf("HasPausedSubFor(prof-1) = false, want true after auth failure")
	}
	if token == "" {
		t.Errorf("HasPausedSubFor token is empty, want a non-empty sub_id")
	}

	// Step 3: redrive (simulates caller invoking ResumeAfterKeyRotation
	// after a successful TestAndRotateKey rotation).
	newSubID, err := runner.RedriveLastTurn(context.Background(), "prof-1")
	if err != nil {
		t.Fatalf("RedriveLastTurn: %v", err)
	}
	if newSubID == "" {
		t.Errorf("RedriveLastTurn returned empty subID")
	}

	// Step 4: the redrive should complete and emit llm:stream-closed.
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Errorf("post-redrive Reason = backend-error, want non-error; msg=%q", closed.Message)
	}

	// Writer should have the assistant message from the second run.
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) < 1 {
		t.Fatalf("writer has no calls; expected at least assistant message from redrive")
	}
	assistantCall := writer.calls[len(writer.calls)-1]
	if assistantCall.role != "assistant" {
		t.Errorf("last writer call role = %q, want assistant", assistantCall.role)
	}
	if !strings.Contains(assistantCall.content, "hello after rotation") {
		t.Errorf("assistant content = %q, want to contain 'hello after rotation'", assistantCall.content)
	}
}

// TestChatRunnerIntegration_KeyRotation_FeatureFlagOff: when
// HARNESS_KEYCHAIN_ROTATION=off, *ErrProviderAuthFailed is treated as a
// regular backend error — "llm:stream-closed" with reason=backend-error is
// emitted immediately and no paused turn is stored.
func TestChatRunnerIntegration_KeyRotation_FeatureFlagOff(t *testing.T) {
	t.Setenv(envKeychainRotationVar, "off")

	llm := &stubLLM{}
	authErr := &corellm.ErrProviderAuthFailed{
		Provider:  "openai",
		ProfileID: "prof-2",
		ModelID:   "gpt-4o",
		Reason:    "invalid api key",
		Cause:     &corellm.ErrAuth{Status: 401, Message: "invalid api key"},
	}
	llm.push(stubLLMResponse{err: authErr})

	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "hello flag-off"},
	})

	_, err := runner.StartStream(context.Background(), "prof-2", "session-2", "", "hello flag-off")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	// With flag off: auth failure surfaces as regular backend-error close.
	closed := waitForClosed(t, broker)
	if closed.Reason != "backend-error" {
		t.Errorf("Reason = %q, want backend-error when flag is off; msg=%q", closed.Reason, closed.Message)
	}

	// No paused turn stored.
	if _, ok := runner.HasPausedSubFor("prof-2"); ok {
		t.Errorf("HasPausedSubFor(prof-2) = true with flag off, want false")
	}

	// No "provider:auth-failed" broker event.
	for _, e := range broker.snapshot() {
		if e.topic == "provider:auth-failed" {
			t.Errorf("unexpected provider:auth-failed event with flag off: %+v", e)
		}
	}
}

// TestChatRunnerIntegration_KeyRotation_RedriveDedupe: once a paused turn is
// consumed by RedriveLastTurn, a second call returns an error and leaves the
// runner in a consistent state.
func TestChatRunnerIntegration_KeyRotation_RedriveDedupe(t *testing.T) {
	t.Setenv(envKeychainRotationVar, "on")

	llm := &stubLLM{}
	authErr := &corellm.ErrProviderAuthFailed{
		Provider:  "openrouter",
		ProfileID: "prof-3",
		ModelID:   "mistral",
		Reason:    "invalid api key",
		Cause:     &corellm.ErrAuth{Status: 401, Message: "invalid api key"},
	}
	llm.push(stubLLMResponse{err: authErr})
	// Second run: successful (consumed by first redrive).
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventText, Text: "done"},
		},
		resp: coreag.LLMResponse{Content: "done", FinishReason: "stop"},
	})

	runner, broker, _ := buildIntegrationRunner(t, llm, newStubTools(), 25, []coreag.Message{
		{Role: "user", Content: "dedupe test"},
	})

	_, err := runner.StartStream(context.Background(), "prof-3", "session-3", "", "dedupe test")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForAuthFailed(t, broker)

	// First redrive: should succeed.
	_, err = runner.RedriveLastTurn(context.Background(), "prof-3")
	if err != nil {
		t.Fatalf("first RedriveLastTurn: %v", err)
	}
	waitForClosed(t, broker)

	// Second redrive: paused turn was consumed; should return error but not panic.
	_, err = runner.RedriveLastTurn(context.Background(), "prof-3")
	if err == nil {
		t.Errorf("second RedriveLastTurn: expected error (no paused turn), got nil")
	}
}
