package chat

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// stopDuringRedriveLLM overflows on its first Generate call — forcing the
// chat runner's overflow-recovery arm — and on every call after that
// blocks on its ctx until cancelled, standing in for a wedged provider
// mid-redrive. redriving is closed the first time a blocking call is
// reached, so a test can synchronize on "the redrive is now in flight"
// without a sleep-and-poll race.
//
// chat-turn-integrity-01PMZ606 WP04 / CHAT-03.
type stopDuringRedriveLLM struct {
	calls     atomic.Int32
	redriving chan struct{}
	once      sync.Once
}

func (f *stopDuringRedriveLLM) Generate(ctx context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	n := f.calls.Add(1)
	if n == 1 {
		return coreag.LLMResponse{}, overflowError()
	}
	f.once.Do(func() { close(f.redriving) })
	<-ctx.Done()
	return coreag.LLMResponse{}, ctx.Err()
}

// succeedingRedriveLLM overflows once, then succeeds — the "redrive that
// is not stopped still completes" counterpart to
// stopDuringRedriveLLM. Without this case, WP04's fix is satisfiable by
// cancelling every redrive unconditionally.
type succeedingRedriveLLM struct {
	calls atomic.Int32
}

func (f *succeedingRedriveLLM) Generate(_ context.Context, _ coreag.LLMRequest) (coreag.LLMResponse, error) {
	if f.calls.Add(1) == 1 {
		return coreag.LLMResponse{}, overflowError()
	}
	return coreag.LLMResponse{Content: "ok"}, nil
}

// overflowModelGraph is a one-node graph whose single model node is the
// entrypoint — the minimum topology that reaches env.LLM.Generate
// directly, so a scripted LLM fake can force the overflow arm without a
// full chat_default.yaml agent loop.
func overflowModelGraph() coreag.Graph {
	return coreag.Graph{
		ID:          "test_overflow_stop",
		Entrypoints: []string{"llm1"},
		Nodes: []coreag.Node{
			{ID: "llm1", Kind: coreag.NodeKindModel, Attrs: coreag.ModelAttrs{
				Provider: "test", Model: "m", MaxTokens: 100,
			}},
		},
	}
}

// buildOverflowStopRunner wires a ChatRunner whose graph is
// overflowModelGraph and whose env.LLM is swapped for llm via
// EnvDefaults — the same seam buildIntegrationRunnerWithGraph uses to
// bypass the real corellm.Registry/LLMProviderAdapter machinery in
// favor of a scripted fake.
func buildOverflowStopRunner(t *testing.T, llm coreag.LLMProvider, budget int) (*ChatRunner, *recordingBroker) {
	t.Helper()
	broker := &recordingBroker{}
	engine := &fakeCompactionEngine{}
	graph := overflowModelGraph()

	runner, err := New(Config{
		Kernel:                coreag.NewKernel(),
		Registry:              stubRegistry{},
		Broker:                broker,
		HistoryWriter:         &recordingHistoryWriter{},
		History:               staticHistoryReader{},
		GraphLoader:           func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:              func() int { return 25 },
		Compaction:            &CompactionDeps{Engine: engine},
		MaxOverflowRecoveries: func() int { return budget },
		EnvDefaults:           func(env *coreag.Env) { env.LLM = llm },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker
}

// TestChatRunner_StopStream_ReachesOverflowRedrive is AC-005
// (chat-turn-integrity-01PMZ606 WP04): pressing Stop during an overflow
// redrive must end the turn instead of the RPC hanging on <-sub.done for
// as long as the (here, permanently wedged) provider takes.
//
// Before WP04, recoverFromOverflow's redrive ran on
// context.WithCancel(context.Background()) — no parent link to the
// run's own context, so sub.cancel() (what StopStream calls) could never
// reach it, and StopStream's <-sub.done blocked forever because
// close(sub.done) cannot run until driveRun's Kernel.Run call returns.
func TestChatRunner_StopStream_ReachesOverflowRedrive(t *testing.T) {
	t.Parallel()
	llm := &stopDuringRedriveLLM{redriving: make(chan struct{})}
	runner, broker := buildOverflowStopRunner(t, llm, 1)

	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "model-1", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	select {
	case <-llm.redriving:
	case <-time.After(2 * time.Second):
		t.Fatalf("redrive never reached the blocking LLM call; the overflow-recovery arm did not fire as expected")
	}

	// StopStream itself blocks on <-sub.done, so it must run in its own
	// goroutine for the test to enforce an explicit deadline on it. The
	// failure mode under test is a hang, and a hang under `go test`
	// reads as an unrelated harness timeout without this.
	stopDone := make(chan struct{})
	go func() {
		_ = runner.StopStream(context.Background(), subID)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("StopStream did not return within the deadline — Stop did not reach the overflow redrive")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			closed, ok := e.payload.(StreamClosedPayload)
			if !ok {
				continue
			}
			if closed.Reason != "stop-called" {
				t.Fatalf("stream-closed reason = %q, want %q", closed.Reason, "stop-called")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stream-closed payload observed after Stop; events = %+v", broker.snapshot())
}

// TestChatRunner_OverflowRedrive_NotStoppedStillCompletes pins that
// WP04's fix is not satisfiable by cancelling every redrive
// unconditionally: a redrive nobody stops must still complete and
// report "completed".
func TestChatRunner_OverflowRedrive_NotStoppedStillCompletes(t *testing.T) {
	t.Parallel()
	llm := &succeedingRedriveLLM{}
	runner, broker := buildOverflowStopRunner(t, llm, 1)

	subID, err := runner.StartStream(context.Background(), "profile-1", "session-1", "model-1", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if subID == "" {
		t.Fatalf("StartStream returned an empty sub id")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range broker.snapshot() {
			closed, ok := e.payload.(StreamClosedPayload)
			if !ok {
				continue
			}
			if closed.Reason != "completed" {
				t.Fatalf("stream-closed reason = %q, want %q (nobody stopped this run)", closed.Reason, "completed")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no stream-closed payload observed; events = %+v", broker.snapshot())
}
