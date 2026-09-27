package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// turn_usage_observer_test.go — Config.TurnUsage, the conversation-lifecycle
// half of the usage-telemetry seam. Both tests drive a REAL StartStream through
// the production chat graph; nothing calls the observer directly.

type turnUsageRecord struct {
	event       string // "started" | "failed"
	sessionID   string
	detail      string // providerKind, or failureKind
	recoverable bool
}

type fakeTurnUsage struct {
	mu   sync.Mutex
	recs []turnUsageRecord
}

func (f *fakeTurnUsage) TurnStarted(_ context.Context, sessionID, providerKind string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, turnUsageRecord{event: "started", sessionID: sessionID, detail: providerKind})
}

func (f *fakeTurnUsage) TurnFailed(_ context.Context, sessionID, failureKind string, recoverable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, turnUsageRecord{event: "failed", sessionID: sessionID, detail: failureKind, recoverable: recoverable})
}

func (f *fakeTurnUsage) snapshot() []turnUsageRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]turnUsageRecord(nil), f.recs...)
}

func newTurnUsageRunner(t *testing.T, llm coreag.LLMProvider, usage TurnUsageObserver) (*ChatRunner, *recordingBroker) {
	t.Helper()
	graph := loadProductionChatGraph(t)
	broker := &recordingBroker{}
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "hello"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
			env.Tools = newStubTools()
		},
		TurnUsage: usage,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker
}

func TestTurnUsage_CleanTurn_ReportsStartedOnceAndNoFailure(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "hi"}},
		resp:   coreag.LLMResponse{Content: "hi", FinishReason: "stop"},
	})
	usage := &fakeTurnUsage{}
	runner, broker := newTurnUsageRunner(t, llm, usage)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-42", "", "hello"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("fixture: the clean turn failed: %q", closed.Message)
	}

	recs := usage.snapshot()
	if len(recs) != 1 || recs[0].event != "started" || recs[0].sessionID != "session-42" {
		t.Fatalf("records = %+v, want exactly one started for session-42", recs)
	}
}

func TestTurnUsage_BackendError_ReportsAClosedKindNeverTheMessage(t *testing.T) {
	t.Parallel()
	const secretBearing = "openrouter: stream read: transient provider error for /Users/alice/secret-repo: sk-or-LEAKED"
	llm := &blockingAfterTextLLM{
		deltas:   []string{"partial text"},
		finalErr: errors.New(secretBearing),
		reached:  make(chan struct{}),
		proceed:  make(chan struct{}),
	}
	close(llm.proceed)
	usage := &fakeTurnUsage{}
	runner, broker := newTurnUsageRunner(t, llm, usage)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-42", "", "hello"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason != "backend-error" {
		t.Fatalf("closed.Reason = %q, want backend-error", closed.Reason)
	}
	// TurnFailed is reported before the closed payload is emitted, but give
	// the goroutine a beat in case ordering ever changes.
	deadline := time.Now().Add(time.Second)
	var failed *turnUsageRecord
	for time.Now().Before(deadline) && failed == nil {
		for _, r := range usage.snapshot() {
			if r.event == "failed" {
				r := r
				failed = &r
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if failed == nil {
		t.Fatalf("a backend-error turn reported no failure; records = %+v", usage.snapshot())
	}
	closedSet := map[string]bool{"auth": true, "transient": true, "unknown": true}
	if !closedSet[failed.detail] {
		t.Errorf("failure kind %q is outside the classifier's closed set", failed.detail)
	}
	for _, leak := range []string{"/Users/alice", "sk-or-LEAKED", "openrouter", "secret-repo"} {
		if strings.Contains(failed.detail, leak) {
			t.Errorf("failure kind carries %q — the error MESSAGE crossed the telemetry seam", leak)
		}
	}
	if !failed.recoverable {
		t.Error("no tool ran, so the failure should be reported recoverable")
	}
}
