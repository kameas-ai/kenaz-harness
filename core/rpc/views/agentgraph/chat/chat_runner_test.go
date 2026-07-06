package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/compaction"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeCompactionEngine is a recording fake for compaction.Engine. It
// counts Compact / RollingSummarize calls and replays a scripted
// reply, so the pre-send hook tests can pin "compact ran exactly K
// times under tier X" without standing up the real engine.
type fakeCompactionEngine struct {
	mu sync.Mutex

	compactCalls         int
	rollingSummarizeCalls int

	compactErr  error
	rollingErr  error
	compactID   string
	rollingID   string
}

func (f *fakeCompactionEngine) Compact(_ context.Context, _ string, _ compaction.ProviderProfileRef, _ float64) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compactCalls++
	if f.compactErr != nil {
		return "", f.compactErr
	}
	if f.compactID == "" {
		return "summary-1", nil
	}
	return f.compactID, nil
}

func (f *fakeCompactionEngine) RollingSummarize(_ context.Context, _ string, _ compaction.ProviderProfileRef, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rollingSummarizeCalls++
	if f.rollingErr != nil {
		return "", f.rollingErr
	}
	if f.rollingID == "" {
		return "rolling-1", nil
	}
	return f.rollingID, nil
}

func (f *fakeCompactionEngine) compactCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.compactCalls
}

func (f *fakeCompactionEngine) rollingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rollingSummarizeCalls
}

// makeCompactionDeps builds a CompactionDeps fixture pinned to a
// (tier, capTokens, currentHistoryTokens) tuple. The history reader
// returns a single user message whose runeCount/4 + framing yields
// the requested currentHistoryTokens — that way the pre-send hook's
// internal tokenize step lands on the test-specified count.
func makeCompactionDeps(t *testing.T, eng compaction.Engine, tier compaction.CompactionAggressiveness,
	cap int, modelTooSmall *atomic.Bool) *CompactionDeps {
	t.Helper()
	return &CompactionDeps{
		Engine:         eng,
		Aggressiveness: func() compaction.CompactionAggressiveness { return tier },
		CompactionModel: func() (compaction.ProviderProfileRef, bool) {
			return compaction.ProviderProfileRef{}, false
		},
		RecentWindow: func() int { return 4 },
		MaxContextTokens: func(_ compaction.ProviderProfileRef) (int, bool) {
			if cap <= 0 {
				return 0, false
			}
			return cap, true
		},
	}
}

// fillerMessage returns a coreag.Message with content sized so the
// tokenize estimate (runeCount/4 + 4 framing) hits the requested
// targetTokens (approximately — the test's assertions use thresholds
// not exact counts).
func fillerMessage(role string, targetTokens int) coreag.Message {
	if targetTokens <= 0 {
		return coreag.Message{Role: role, Content: ""}
	}
	// runesPerToken=4, messageFramingOverhead=4. Subtract the
	// systemPrompt-slot and per-message framing first.
	contentTokens := targetTokens
	if contentTokens < 0 {
		contentTokens = 0
	}
	runes := contentTokens * 4
	if runes <= 0 {
		runes = 1
	}
	return coreag.Message{Role: role, Content: strings.Repeat("x", runes)}
}

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

// TestChatRunner_PreSendCompaction_OffTier_OverCap_ReturnsSessionFull
// asserts the off tier honestly fails when the would-be request would
// exceed the cap (WP08 acceptance: "Off-tier session honestly fails on
// cap with ErrSessionFull").
func TestChatRunner_PreSendCompaction_OffTier_OverCap_ReturnsSessionFull(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{}
	// Cap = 100; history pre-loaded with ~150 tokens of filler so the
	// off-tier branch trips the over-cap check.
	hist := []coreag.Message{fillerMessage("user", 150)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessOff, 100, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "second turn")
	if gerr == nil || !errors.Is(gerr, compaction.ErrSessionFull) {
		t.Fatalf("StartStream: got err=%v, want ErrSessionFull", gerr)
	}
	if eng.compactCount() != 0 || eng.rollingCount() != 0 {
		t.Errorf("off-tier should not invoke compaction; compact=%d rolling=%d",
			eng.compactCount(), eng.rollingCount())
	}
}

// TestChatRunner_PreSendCompaction_OffTier_UnderCap_NoCompact asserts
// the off tier passes through cleanly when the request stays under cap.
func TestChatRunner_PreSendCompaction_OffTier_UnderCap_NoCompact(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{}
	hist := []coreag.Message{fillerMessage("user", 5)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessOff, 1000, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "second turn")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.compactCount() != 0 || eng.rollingCount() != 0 {
		t.Errorf("under-cap should not compact; compact=%d rolling=%d",
			eng.compactCount(), eng.rollingCount())
	}
}

// TestChatRunner_PreSendCompaction_Threshold_NotTriggered asserts the
// threshold tier does NOT compact when current/cap < TriggerPct (WP08
// acceptance: "Threshold-tier session compacts exactly when ... and
// not before").
func TestChatRunner_PreSendCompaction_Threshold_NotTriggered(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{}
	// Balanced tier triggers at 0.80. Cap=1000, history ~10 tokens
	// → ratio ~ 0.01, well below 0.80.
	hist := []coreag.Message{fillerMessage("user", 10)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessBalanced, 1000, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "small turn")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.compactCount() != 0 {
		t.Errorf("under-threshold should not compact; got %d", eng.compactCount())
	}
}

// TestChatRunner_PreSendCompaction_Threshold_Triggered asserts the
// threshold tier compacts exactly once when current/cap >= TriggerPct.
func TestChatRunner_PreSendCompaction_Threshold_Triggered(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{}
	// Balanced tier triggers at 0.80. Cap=100, history ~90 tokens of
	// filler → ratio ~ 0.90, above 0.80.
	hist := []coreag.Message{fillerMessage("user", 90)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessBalanced, 100, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "trigger turn")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.compactCount() != 1 {
		t.Errorf("threshold trigger should run Compact exactly once; got %d", eng.compactCount())
	}
}

// TestChatRunner_PreSendCompaction_Maximal_RollsEveryTurn asserts the
// maximal tier rolls on every send (WP08 acceptance: "Maximal-tier
// session compacts on every turn with rolling summary semantics").
func TestChatRunner_PreSendCompaction_Maximal_RollsEveryTurn(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{}
	hist := []coreag.Message{fillerMessage("user", 5)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessMaximal, 1000, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "first roll")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.rollingCount() != 1 {
		t.Errorf("first send should run RollingSummarize once; got %d", eng.rollingCount())
	}
	// Second send: should roll again.
	subID2, gerr2 := runner.StartStream(context.Background(), "profile-1", "session-1", "", "second roll")
	if gerr2 != nil {
		t.Fatalf("StartStream2: %v", gerr2)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID2)
	if eng.rollingCount() != 2 {
		t.Errorf("second send should run RollingSummarize again; got %d", eng.rollingCount())
	}
}

// TestChatRunner_PreSendCompaction_Maximal_TooSmall_FallsBackAggressive
// asserts the rolling-mode model-too-small case silently falls back to
// the aggressive tier (plan §2.5 R2 graceful-degrade).
func TestChatRunner_PreSendCompaction_Maximal_TooSmall_FallsBackAggressive(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{
		rollingErr: &compaction.ErrCompactionModelTooSmall{NeedsTokens: 10000, ModelMaxTokens: 1000},
	}
	hist := []coreag.Message{fillerMessage("user", 5)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessMaximal, 1000, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "rolling fail")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.rollingCount() != 1 {
		t.Errorf("RollingSummarize should still be attempted once; got %d", eng.rollingCount())
	}
	if eng.compactCount() != 1 {
		t.Errorf("aggressive-fallback Compact should run once; got %d", eng.compactCount())
	}
}

// TestChatRunner_PreSendCompaction_Threshold_TooSmall_ReturnsSessionFull
// asserts a model-too-small in threshold mode surfaces ErrSessionFull
// to the caller (R1: "If the user is over cap and we can't compact,
// fail honestly").
func TestChatRunner_PreSendCompaction_Threshold_TooSmall_ReturnsSessionFull(t *testing.T) {
	t.Parallel()
	eng := &fakeCompactionEngine{
		compactErr: &compaction.ErrCompactionModelTooSmall{NeedsTokens: 10000, ModelMaxTokens: 1000},
	}
	hist := []coreag.Message{fillerMessage("user", 90)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessBalanced, 100, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "trigger turn")
	if gerr == nil || !errors.Is(gerr, compaction.ErrSessionFull) {
		t.Fatalf("StartStream: got err=%v, want ErrSessionFull", gerr)
	}
}

// TestChatRunner_PreSendCompaction_HARNESS_COMPACTION_OFF_ShortCircuits
// asserts the env-var opt-out skips the hook entirely (WP08 acceptance:
// "HARNESS_COMPACTION=off short-circuits the hook").
func TestChatRunner_PreSendCompaction_HARNESS_COMPACTION_OFF_ShortCircuits(t *testing.T) {
	// Not parallel: mutates env var.
	t.Setenv("HARNESS_COMPACTION", "off")
	eng := &fakeCompactionEngine{}
	hist := []coreag.Message{fillerMessage("user", 200)}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        &recordingBroker{},
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader:   func() (coreag.Graph, error) { return minimalChatGraph(), nil },
		MaxTurns:      func() int { return 25 },
		Compaction:    makeCompactionDeps(t, eng, compaction.AggressivenessBalanced, 100, nil),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, gerr := runner.StartStream(context.Background(), "profile-1", "session-1", "", "would normally trigger")
	if gerr != nil {
		t.Fatalf("StartStream: %v", gerr)
	}
	time.Sleep(20 * time.Millisecond)
	_ = runner.StopStream(context.Background(), subID)
	if eng.compactCount() != 0 || eng.rollingCount() != 0 {
		t.Errorf("HARNESS_COMPACTION=off should short-circuit; compact=%d rolling=%d",
			eng.compactCount(), eng.rollingCount())
	}
}

// stubRegistry is a no-op corellm.Registry stub for tests that don't
// fire the model executor (the minimal graph stops at the AskNode).
type stubRegistry struct{}

func (stubRegistry) RegisterAdapter(_ corellm.ProviderAdapter)    {}
func (stubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (stubRegistry) Evict(_ string) error                           { return nil }
func (stubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, errors.New("stub")
}
func (stubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (stubRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return nil, errors.New("stub: no stream")
}
