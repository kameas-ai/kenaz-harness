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
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/compactionpolicy"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeCompactionEngine is a recording fake for compaction.SessionEngine. It
// counts Compact / RollingSummarize calls and replays a scripted
// reply, so the pre-send hook tests can pin "compact ran exactly K
// times under tier X" without standing up the real engine.
type fakeCompactionEngine struct {
	mu sync.Mutex

	compactCalls          int
	rollingSummarizeCalls int

	compactErr error
	rollingErr error
	compactID  string
	rollingID  string
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
func makeCompactionDeps(t *testing.T, eng compaction.SessionEngine, tier compactionpolicy.CompactionAggressiveness,
	cap int, modelTooSmall *atomic.Bool) *CompactionDeps {
	t.Helper()
	return &CompactionDeps{
		Engine:         eng,
		Aggressiveness: func() compactionpolicy.CompactionAggressiveness { return tier },
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
	// entry is the full seam payload so move-metadata assertions
	// (WP02 onward) have something to read.
	entry coreag.HistoryEntry
}

func (w *recordingHistoryWriter) AppendEntry(_ context.Context, sid string,
	e coreag.HistoryEntry) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, writerCall{
		sessionID: sid, role: e.Role, content: e.Content, entry: e,
	})
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

// TestChatRunner_StartStream_ArmsCompactionWatermark is the
// compaction-convergence-01PMDL05 structural half of the single-fire
// guarantee, carried forward through turn-context-runway-01PMAG03 WP02.
//
// It used to assert that every Env the chat runner builds set a hard
// suppression flag. That flag bought single-fire by welding the
// kernel's pre_call/post_tool sites shut for the whole turn, which is
// what made a chat turn uncompactable and is the ceiling WP02 removed;
// agentgraph-total-convergence-01PMGX01 WP08 then deleted the field
// entirely. The runner arms a growth watermark instead.
//
// The guarantee under test is unchanged and is asserted directly rather
// than through the mechanism: the first pre-call site of a chat run
// must NOT be able to compact, because the `compact` node at the head
// of the graph just did. The watermark delivers that by construction —
// the first observation latches the baseline, and no observation can
// exceed the baseline it just set. The kernel-level behavioural proofs
// live in compaction_kernel_test.go's
// TestKernel_ArmedWatermark_RefusesFirstPreCallSite and its post_tool
// counterpart.
func TestChatRunner_StartStream_ArmsCompactionWatermark(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := minimalChatGraph()

	var mu sync.Mutex
	var seenEnv *coreag.Env
	captureEnvDefaults := func(env *coreag.Env) {
		mu.Lock()
		defer mu.Unlock()
		seenEnv = env
	}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   captureEnvDefaults,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	// Drain the run goroutine until EnvDefaults has definitely fired.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		env := seenEnv
		mu.Unlock()
		if env != nil {
			if env.AutoCompaction == nil {
				t.Fatalf("Env.AutoCompaction = nil, want an armed watermark (kernel automatic compaction sites would be ungated on a chat-driven run, double-firing against the compact node)")
			}
			// The single-fire guarantee, asserted as behaviour: the
			// first site to observe the live context — whatever it
			// observes — must be refused.
			if env.AutoCompaction.Admit(500_000) {
				t.Fatalf("watermark admitted the FIRST pre-call observation; the compact node at the head of the graph already compacted this transcript, so this is the compaction-convergence-01PMDL05 double-fire")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("EnvDefaults callback never observed an Env")
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

// The eight TestChatRunner_PreSendCompaction_* tests that stood here
// were deleted by agentgraph-total-convergence-01PMGX01 WP08, which
// deleted the function they exercised. They drove StartStream against a
// graph with no compact node and asserted, one tier and one branch at a
// time, how many Engine calls the pre-kernel pass made and what error it
// returned.
//
// Their coverage is not lost — it is subsumed and widened by
// compaction_dial_golden_test.go, which pins the same decisions for all
// five tiers across ten scenarios in one byte-exact transcript, and
// which now drives them through the real path: a kernel run over a
// `compact` node dispatching into the bound FR-041 pipeline. Several
// behaviours those eight tests never covered (a maximal-tier roll firing
// even far under the trigger, an unknown model cap skipping the
// threshold tiers but not maximal, a generic engine error being
// swallowed where a model-too-small is promoted to ErrSessionFull) are
// in the golden.
//
// One thing they asserted genuinely changed and is re-pinned below
// rather than dropped: StartStream used to return ErrSessionFull
// synchronously, because compaction ran before the kernel did. It is now
// a node inside the run, so the condition reaches the user on the
// stream-closed payload — the same channel, with the same copy, that the
// overflow-recovery path has always used for the same condition.

// TestChatRunner_SessionFull_ArrivesOnStreamClosed pins the delivery
// contract for a session that is genuinely out of context.
//
// The `compact` node fails the run with compaction.ErrSessionFull; the
// kernel-exit switch must translate that into a terminal stream-closed
// payload carrying the sentinel's own copy, not a raw wrapped-error
// string that reads as an opaque backend failure. Getting this wrong is
// silent: the turn still ends, the user just gets gibberish instead of
// "your session is full".
func TestChatRunner_SessionFull_ArrivesOnStreamClosed(t *testing.T) {
	// Not parallel: shares the broker-drain timing pattern below.
	broker := &recordingBroker{}

	// A session over its cap on the "off" tier — the dial's honest
	// failure path.
	hist := []coreag.Message{fillerMessage("user", 150)}
	eng := &fakeCompactionEngine{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: hist},
		GraphLoader: func() (coreag.Graph, error) {
			return *compactHeadGraph(), nil
		},
		MaxTurns:           func() int { return 25 },
		Compaction:         makeCompactionDeps(t, eng, compactionpolicy.AggressivenessOff, 100, nil),
		CompactionPipeline: newTierPipeline("off"),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "model-1", "one more turn")
	if err != nil {
		t.Fatalf("StartStream returned an error; session-full now travels on the stream: %v", err)
	}
	if subID == "" {
		t.Fatalf("StartStream returned an empty sub id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range broker.snapshot() {
			closed, ok := ev.payload.(StreamClosedPayload)
			if !ok {
				continue
			}
			if closed.Reason != "backend-error" {
				t.Fatalf("stream-closed reason = %q, want %q", closed.Reason, "backend-error")
			}
			if closed.Message != compaction.ErrSessionFull.Error() {
				t.Fatalf("stream-closed message = %q, want the session-full copy %q",
					closed.Message, compaction.ErrSessionFull.Error())
			}
			// The discriminator the chat surface renders from. Without
			// it the frontend can only string-match Message, and the
			// copy is a human sentence that will be reworded.
			if closed.ErrorKind != StreamClosedErrorKindSessionFull {
				t.Fatalf("stream-closed error_kind = %q, want %q — the surface falls back to the generic \"Send failed\" heading, which is false here: the send succeeded",
					closed.ErrorKind, StreamClosedErrorKindSessionFull)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stream-closed payload observed; events = %+v", broker.snapshot())
}

// stubRegistry is a no-op corellm.Registry stub for tests that don't
// fire the model executor (the minimal graph stops at the AskNode).
type stubRegistry struct{}

func (stubRegistry) RegisterAdapter(_ corellm.ProviderAdapter)      {}
func (stubRegistry) LoadProfiles(_ []corellm.ProviderProfile) error { return nil }
func (stubRegistry) Evict(_ string) error                           { return nil }
func (stubRegistry) Profile(_ string) (corellm.ProviderProfile, error) {
	return corellm.ProviderProfile{}, errors.New("stub")
}
func (stubRegistry) PreflightAll(_ context.Context) []corellm.PreflightResult { return nil }
func (stubRegistry) Stream(_ context.Context, _ corellm.GenerationRequest) (corellm.Stream, error) {
	return nil, errors.New("stub: no stream")
}
