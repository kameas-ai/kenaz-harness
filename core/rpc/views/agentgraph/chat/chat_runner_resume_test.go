package chat

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// recordingPartialPersister is a fake PartialPersister that records
// every PersistPartial call so the long-turn-resilience-WP03 tests can
// pin the exact (sessionID, partialText, kind, recoverable) tuple the
// runner forwards on a stream-drop.
type recordingPartialPersister struct {
	mu    sync.Mutex
	calls []recordedPartialCall
	// nextID is the message id the persister returns; incremented per
	// call so concurrent tests get distinct ids. Starts at "partial-1".
	nextID int
	err    error
}

type recordedPartialCall struct {
	sessionID, partialText, kind string
	recoverable                  bool
	messageID                    string
}

func (p *recordingPartialPersister) PersistPartial(_ context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return "", p.err
	}
	p.nextID++
	id := "partial-" + intToA(p.nextID)
	p.calls = append(p.calls, recordedPartialCall{
		sessionID:   sessionID,
		partialText: partialText,
		kind:        kind,
		recoverable: recoverable,
		messageID:   id,
	})
	return id, nil
}

func (p *recordingPartialPersister) snapshot() []recordedPartialCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedPartialCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// intToA renders an int as a decimal string without pulling strconv in.
// Sufficient for test ids; never larger than three digits in practice.
func intToA(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	digits := make([]byte, 0, 8)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// dropAfterTextLLM is a minimal coreag.LLMProvider that emits the
// supplied text deltas, then returns an error. Triggers the chat
// runner's terminal partial-persist path.
type dropAfterTextLLM struct {
	deltas    []string
	emitTool  bool
	finalErr  error
}

func (d *dropAfterTextLLM) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	if sink, ok := coreag.StreamSinkFromContext(ctx); ok && sink != nil {
		for _, t := range d.deltas {
			sink.Emit(coreag.StreamEvent{Kind: coreag.StreamEventText, Text: t})
		}
		if d.emitTool {
			sink.Emit(coreag.StreamEvent{
				Kind:     coreag.StreamEventTool,
				ToolID:   "tu-1",
				ToolName: "shell",
				ToolArgs: `{"cmd":"ls"}`,
			})
		}
	}
	if d.finalErr != nil {
		return coreag.LLMResponse{}, d.finalErr
	}
	return coreag.LLMResponse{Content: "ok"}, nil
}

// buildResumeRunner wires a ChatRunner around a single-shot drop LLM,
// the production chat_default graph, and the recording PartialPersister.
// Returns the runner + broker + persister + history writer.
func buildResumeRunner(
	t *testing.T,
	llm *dropAfterTextLLM,
	persister *recordingPartialPersister,
	graphHistory []coreag.Message,
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
		History:       staticHistoryReader{msgs: graphHistory},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
		},
		PartialPersister: persister,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, writer
}

// TestChatRunner_PartialPersist_TextOnly_Recoverable verifies (a) on the
// long-turn-resilience-WP03 spec: stream errors with text content →
// row persisted with streaming_recoverable=true, the closed payload
// surfaces the partial message id + failure kind.
func TestChatRunner_PartialPersist_TextOnly_Recoverable(t *testing.T) {
	t.Parallel()
	llm := &dropAfterTextLLM{
		deltas:   []string{"hello ", "wor"},
		finalErr: errors.New("openrouter: stream read: transient provider error: Network connection lost"),
	}
	persister := &recordingPartialPersister{}
	runner, broker, _ := buildResumeRunner(t, llm, persister, []coreag.Message{
		{Role: "user", Content: "say hi"},
	})
	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "say hi")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	closed := waitForClosed(t, broker)
	_ = subID

	// Persister was called exactly once with the buffered text.
	calls := persister.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 PersistPartial call, got %d (%+v)", len(calls), calls)
	}
	c := calls[0]
	if c.sessionID != "session-1" {
		t.Errorf("sessionID = %q, want session-1", c.sessionID)
	}
	if c.partialText != "hello wor" {
		t.Errorf("partialText = %q, want %q", c.partialText, "hello wor")
	}
	if !c.recoverable {
		t.Errorf("recoverable = false, want true (no tool_use was emitted)")
	}
	if c.kind != "transient" {
		t.Errorf("kind = %q, want transient (network error message)", c.kind)
	}

	// Closed payload carries the partial message id + classification.
	if closed.PartialMessageID != "partial-1" {
		t.Errorf("PartialMessageID = %q, want partial-1", closed.PartialMessageID)
	}
	if closed.PartialFailureKind != "transient" {
		t.Errorf("PartialFailureKind = %q, want transient", closed.PartialFailureKind)
	}
	if !closed.PartialRecoverable {
		t.Errorf("PartialRecoverable = false, want true")
	}
}

// TestChatRunner_PartialPersist_ToolUse_NotRecoverable verifies (b) on
// the spec: stream errors with tool_use → streaming_recoverable=false.
// The partial row still lands so the user keeps the visible text, but
// the Resume button is suppressed via the recoverable=false flag.
func TestChatRunner_PartialPersist_ToolUse_NotRecoverable(t *testing.T) {
	t.Parallel()
	llm := &dropAfterTextLLM{
		deltas:   []string{"running tool"},
		emitTool: true,
		finalErr: errors.New("openrouter: stream read: transient provider error: Network connection lost"),
	}
	persister := &recordingPartialPersister{}
	runner, broker, _ := buildResumeRunner(t, llm, persister, []coreag.Message{
		{Role: "user", Content: "do it"},
	})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-2", "", "do it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)

	calls := persister.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 PersistPartial call, got %d", len(calls))
	}
	if calls[0].recoverable {
		t.Errorf("recoverable = true, want false (tool_use was emitted before drop)")
	}
	if closed.PartialRecoverable {
		t.Errorf("closed.PartialRecoverable = true, want false")
	}
}

// TestChatRunner_PartialPersist_NoTextSeen_Skipped verifies the runner
// does not invoke PartialPersister when no text deltas were seen. The
// rationale: an empty bubble would surface a stale Resume button with
// no recovery upside; the WP00 frontend fallback handles the empty
// case via the stream-closed payload's reason=backend-error path.
func TestChatRunner_PartialPersist_NoTextSeen_Skipped(t *testing.T) {
	t.Parallel()
	llm := &dropAfterTextLLM{
		deltas:   nil,
		finalErr: errors.New("openrouter: pre-stream connection refused"),
	}
	persister := &recordingPartialPersister{}
	runner, broker, _ := buildResumeRunner(t, llm, persister, []coreag.Message{
		{Role: "user", Content: "ping"},
	})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-3", "", "ping"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)

	if got := len(persister.snapshot()); got != 0 {
		t.Errorf("expected 0 PersistPartial calls (no text seen), got %d", got)
	}
	if closed.PartialMessageID != "" {
		t.Errorf("PartialMessageID = %q, want empty (no partial row was persisted)", closed.PartialMessageID)
	}
}

// TestChatRunner_PartialPersist_NotInvokedOnCleanCompletion verifies
// the runner skips PartialPersister when the kernel exits cleanly. The
// existing SessionWriteNode-driven persistence is the canonical path
// for happy-case turns; the partial-persist seam is a strict fallback.
func TestChatRunner_PartialPersist_NotInvokedOnCleanCompletion(t *testing.T) {
	t.Parallel()
	llm := &dropAfterTextLLM{
		deltas: []string{"ok"},
	}
	persister := &recordingPartialPersister{}
	runner, broker, _ := buildResumeRunner(t, llm, persister, []coreag.Message{
		{Role: "user", Content: "ping"},
	})
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-4", "", "ping"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	// Wait for the run to wind down.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		seen := false
		for _, e := range broker.snapshot() {
			if e.topic == "llm:stream-closed" {
				seen = true
				break
			}
		}
		if seen {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(persister.snapshot()); got != 0 {
		t.Errorf("expected 0 PersistPartial calls on clean completion, got %d", got)
	}
}
