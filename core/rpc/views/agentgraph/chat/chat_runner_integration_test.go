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

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
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
	calls     atomic.Int32
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

func (s *stubLLM) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	s.calls.Add(1)
	s.mu.Lock()
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
// and returns the parsed Graph. The path is resolved from the test's CWD
// (which is the chat package directory) up two levels into the library
// directory; failure to locate the bundled file is a hard test failure
// because the whole point of these integration tests is to verify the
// production graph topology.
func loadProductionChatGraph(t *testing.T) coreag.Graph {
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
