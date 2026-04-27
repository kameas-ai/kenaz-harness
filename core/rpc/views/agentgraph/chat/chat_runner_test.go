package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// recordingBroker is a fake Broker that captures every emitted topic
// and payload so tests can assert wire-shape parity with the legacy
// llm:stream-chunk / llm:stream-closed surface.
type recordingBroker struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	topic   string
	payload any
}

func (b *recordingBroker) Emit(topic string, payload any) {
	b.mu.Lock()
	b.events = append(b.events, recordedEvent{topic: topic, payload: payload})
	b.mu.Unlock()
}

func (b *recordingBroker) snapshot() []recordedEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]recordedEvent, len(b.events))
	copy(out, b.events)
	return out
}

// recordingHistoryWriter captures append-message calls.
type recordingHistoryWriter struct {
	mu    sync.Mutex
	calls []writerCall
}

type writerCall struct {
	sessionID, role, content string
}

func (w *recordingHistoryWriter) AppendMessage(_ context.Context, sid, role, content string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, writerCall{sessionID: sid, role: role, content: content})
	return "msg-1", nil
}

// staticHistoryReader returns a fixed history.
type staticHistoryReader struct {
	msgs []coreag.Message
}

func (s staticHistoryReader) History(_ context.Context, _ string, _ int) ([]coreag.Message, error) {
	return s.msgs, nil
}

func TestChatRunner_New_RequiresKernel(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatalf("expected error on empty config")
	}
}

// TestChatRunner_StartStreamPersistsUserTurn asserts the user message
// is appended to the session via the HistoryWriter before the kernel
// run starts, so HistoryReadNode picks it up at run start.
func TestChatRunner_StartStreamPersistsUserTurn(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := minimalChatGraph()
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "what is 2+2?")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatal("expected non-empty sub id")
	}
	// Wait for the run goroutine to drain.
	time.Sleep(50 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.calls) == 0 {
		t.Fatalf("expected user-turn append, got 0 calls")
	}
	c := writer.calls[0]
	if c.sessionID != "session-1" || c.role != "user" || c.content != "what is 2+2?" {
		t.Errorf("first append: %+v", c)
	}
}

// TestChatRunner_TerminalEmitsClosedPayload asserts that the runner
// always fans an llm:stream-closed payload onto the broker once the
// kernel run exits, so the chat surface stops the typing indicator.
func TestChatRunner_TerminalEmitsClosedPayload(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := minimalChatGraph()
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	// Drain the run goroutine.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		events := broker.snapshot()
		for _, e := range events {
			if e.topic == "llm:stream-closed" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not see llm:stream-closed payload; events = %+v", broker.snapshot())
}

// TestApplyMaxTurnsDial verifies the LoopNode max_iterations override.
func TestApplyMaxTurnsDial(t *testing.T) {
	t.Parallel()
	g := coreag.Graph{
		Nodes: []coreag.Node{
			{ID: "loop", Kind: coreag.NodeKindLoop, Attrs: coreag.LoopAttrs{MaxIterations: 8}},
			{ID: "ask", Kind: coreag.NodeKindAsk, Attrs: coreag.AskAttrs{Question: "?"}},
		},
	}
	applyMaxTurnsDial(&g, 42)
	got := g.Nodes[0].Attrs.(coreag.LoopAttrs).MaxIterations
	if got != 42 {
		t.Errorf("MaxIterations = %d, want 42", got)
	}
}

// TestStreamBridge_TranslatesEvents asserts translateAGStreamEvent
// preserves the kernel-side fields (text, tool, finish, error) onto
// the corellm wire shape.
func TestStreamBridge_TranslatesEvents(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	bridge := NewStreamBridge(broker, "sub-1", "sess-1")
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: "hello"})
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "shell", ToolArgs: `{"cmd":"ls"}`})
	bridge.Emit(coreag.StreamEvent{Kind: coreag.StreamEventFinish, Finish: "stop"})
	bridge.Close()

	evs := broker.snapshot()
	if len(evs) != 4 {
		t.Fatalf("len(events) = %d, want 4", len(evs))
	}
	if evs[0].topic != "llm:stream-chunk" {
		t.Errorf("evs[0].topic = %q", evs[0].topic)
	}
	if evs[3].topic != "llm:stream-closed" {
		t.Errorf("evs[3].topic = %q", evs[3].topic)
	}
	chunk := evs[0].payload.(StreamChunkPayload)
	if chunk.Chunk.Text != "hello" {
		t.Errorf("chunk.Text = %q", chunk.Chunk.Text)
	}
	tool := evs[1].payload.(StreamChunkPayload)
	if tool.Chunk.Tool == nil || tool.Chunk.Tool.Name != "shell" {
		t.Errorf("tool chunk = %+v", tool.Chunk.Tool)
	}
	finish := evs[2].payload.(StreamChunkPayload)
	if finish.Chunk.Finish != "stop" {
		t.Errorf("finish = %q", finish.Chunk.Finish)
	}
	closed := evs[3].payload.(StreamClosedPayload)
	if closed.SubID != "sub-1" || closed.Reason != "completed" {
		t.Errorf("closed = %+v", closed)
	}
}

// minimalChatGraph builds a tiny graph that fires a single AskNode and
// pauses. Used as the test loader output so the kernel run completes
// quickly without needing a real LLM.
func minimalChatGraph() coreag.Graph {
	return coreag.Graph{
		ID:          "test_chat",
		Name:        "test chat graph",
		Entrypoints: []string{"ask_user"},
		Nodes: []coreag.Node{
			{
				ID:    "ask_user",
				Kind:  coreag.NodeKindAsk,
				Attrs: coreag.AskAttrs{Question: "what?"},
			},
		},
	}
}

// stubRegistry is a no-op corellm.Registry stub for tests that don't
// fire the model executor (the minimal graph stops at the AskNode).
type stubRegistry struct{}

func (stubRegistry) RegisterAdapter(_ corellm.ProviderAdapter)    {}
func (stubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (stubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, errors.New("stub")
}
func (stubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (stubRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return nil, errors.New("stub: no stream")
}
