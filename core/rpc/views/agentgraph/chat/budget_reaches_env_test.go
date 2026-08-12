package chat

import (
	"context"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// autonomy-knobs-live-01PMAG02 WP03 — end-to-end last-hop assertion.
//
// The unit tests on applyTokenCeilingKnob prove the helper is correct.
// This test proves the part that was actually broken: that the value
// reaches env.Budget on a real StartStream against the real
// chat_default.yaml. Wiring missions fail at the last hop, so the last
// hop is what needs the test.
//
// cfg.EnvDefaults runs after the Env literal is built, which gives a
// test a clean seam to observe the constructed Env.
func TestChatRunner_GraphBudgetReachesEnv(t *testing.T) {
	t.Parallel()

	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "hi"}},
		resp:   coreag.LLMResponse{Content: "hi", FinishReason: "stop"},
	})

	graph := loadProductionChatGraph(t)
	// Guard the premise: if chat_default.yaml ever drops its budget
	// block, this test would pass vacuously.
	if graph.Budget.MaxTokensPerRun <= 0 || graph.Budget.MaxLLMCallsPerRun <= 0 || graph.Budget.MaxToolCallsPerRun <= 0 {
		t.Fatalf("premise broken: chat_default.yaml declares no budget: %+v", graph.Budget)
	}

	var mu sync.Mutex
	var seen []coreag.Budget
	broker := &recordingBroker{}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "say hi"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			mu.Lock()
			seen = append(seen, env.Budget)
			mu.Unlock()
			env.LLM = llm
			env.Tools = newStubTools()
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "say hi"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	waitForClosed(t, broker)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("EnvDefaults never ran — cannot observe env.Budget")
	}
	got := seen[0]

	// The regression: env.Budget was the zero value on every chat run,
	// so every `if env.Budget.X > 0` guard in kernel.checkBudget
	// short-circuited and the declared caps enforced nothing.
	if got.MaxTokensPerRun != graph.Budget.MaxTokensPerRun {
		t.Errorf("env.Budget.MaxTokensPerRun = %d, want the graph's %d", got.MaxTokensPerRun, graph.Budget.MaxTokensPerRun)
	}
	if got.MaxLLMCallsPerRun != graph.Budget.MaxLLMCallsPerRun {
		t.Errorf("env.Budget.MaxLLMCallsPerRun = %d, want the graph's %d", got.MaxLLMCallsPerRun, graph.Budget.MaxLLMCallsPerRun)
	}
	if got.MaxToolCallsPerRun != graph.Budget.MaxToolCallsPerRun {
		t.Errorf("env.Budget.MaxToolCallsPerRun = %d, want the graph's %d", got.MaxToolCallsPerRun, graph.Budget.MaxToolCallsPerRun)
	}
	if got == (coreag.Budget{}) {
		t.Fatal("env.Budget is the zero value — production chat is running with no per-run caps at all")
	}
}
