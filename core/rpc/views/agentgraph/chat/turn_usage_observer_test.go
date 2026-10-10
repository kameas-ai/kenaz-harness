package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
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
	// ended is kept apart from recs so the started/failed assertions
	// predating TurnEnded (ml-producer-01MLPRD01 WP02) stay exact.
	ended []turnEndRecord
}

type turnEndRecord struct {
	sessionID             string
	outcome               string
	modelCalls, toolCalls int
	dur                   time.Duration
	unattended            bool
}

func (f *fakeTurnUsage) TurnEnded(ctx context.Context, sessionID, outcome string, modelCalls, toolCalls int, dur time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, turnEndRecord{
		sessionID: sessionID, outcome: outcome,
		modelCalls: modelCalls, toolCalls: toolCalls, dur: dur,
		unattended: runposture.IsUnattended(ctx),
	})
}

func (f *fakeTurnUsage) endedSnapshot() []turnEndRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]turnEndRecord(nil), f.ended...)
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

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-42", "", testTurn("hello")); err != nil {
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

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-42", "", testTurn("hello")); err != nil {
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

// waitEnded polls for the TurnEnded record; it is reported before the
// stream-closed payload, so this normally returns on the first look.
func waitEnded(t *testing.T, usage *fakeTurnUsage) turnEndRecord {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ended := usage.endedSnapshot(); len(ended) > 0 {
			if len(ended) != 1 {
				t.Fatalf("TurnEnded fired %d times, want once: %+v", len(ended), ended)
			}
			return ended[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("TurnEnded never fired")
	return turnEndRecord{}
}

// ml-producer-01MLPRD01 WP02: TurnEnded fires once beside the terminal
// switch, with the run's counters and posture.
func TestTurnUsage_CleanTurn_ReportsTurnEndedCompleted(t *testing.T) {
	t.Parallel()
	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "hi"}},
		resp:   coreag.LLMResponse{Content: "hi", FinishReason: "stop"},
	})
	usage := &fakeTurnUsage{}
	runner, broker := newTurnUsageRunner(t, llm, usage)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-43", "", testTurn("hello")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForClosed(t, broker)
	got := waitEnded(t, usage)
	if got.sessionID != "session-43" || got.outcome != TurnEndCompleted {
		t.Fatalf("TurnEnded = %+v, want session-43 / completed", got)
	}
	if got.modelCalls < 1 || got.toolCalls != 0 {
		t.Errorf("counts = model %d tool %d, want >=1 model call and no tool call", got.modelCalls, got.toolCalls)
	}
	if got.dur <= 0 {
		t.Errorf("dur = %v, want > 0", got.dur)
	}
	if got.unattended {
		t.Error("an attended StartStream reported an unattended ctx")
	}
}

func TestTurnUsage_BackendError_ReportsTurnEndedFailed_AndCarriesPosture(t *testing.T) {
	t.Parallel()
	llm := &blockingAfterTextLLM{
		deltas:   []string{"partial text"},
		finalErr: errors.New("transient provider error"),
		reached:  make(chan struct{}),
		proceed:  make(chan struct{}),
	}
	close(llm.proceed)
	usage := &fakeTurnUsage{}
	runner, broker := newTurnUsageRunner(t, llm, usage)

	if _, err := runner.StartStream(runposture.Unattended(context.Background()), "profile-1", "session-44", "", testTurn("hello")); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForClosed(t, broker)
	got := waitEnded(t, usage)
	if got.outcome != TurnEndFailed {
		t.Fatalf("outcome = %q, want failed", got.outcome)
	}
	if !got.unattended {
		t.Error("an unattended run's TurnEnded ctx lost the runposture marker")
	}
}
