package chat

import (
	"context"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
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
//
// UPDATED (owner directive 2026-09-09, applyBudgetTierDial): this
// runner has no AutonomyKnobs provider wired, so autonomyKnobs()
// resolves EffectiveTier to autonomy.TierDefault (its documented
// no-provider fallback). TierDefault's call-volume ceiling
// (1500 LLM calls / 3000 tool calls, presets.go's budgetCeilingTable)
// is now what reaches env.Budget, not the graph's raw declared numbers
// (5000/10000) — the graph's numbers are the outer ceiling the dial can
// only narrow, and TierDefault genuinely narrows them. This is the
// intended behaviour change, not a regression: before
// applyBudgetTierDial, chat_default_classic.yaml's call-volume caps
// passed through completely unscaled regardless of tier, which is
// exactly the gap the owner's live "agent reached the per-run budget
// cap" report was filed against.
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
	// MaxLLMCallsPerRun / MaxToolCallsPerRun are no longer expected to
	// equal the graph's raw declared numbers: applyBudgetTierDial now
	// narrows them to the TierDefault ceiling (this runner has no
	// AutonomyKnobs provider wired, so autonomyKnobs() falls back to
	// TierDefault). See the test's doc comment above.
	wantTierCeiling := autonomy.BudgetCeilingForTier(autonomy.TierDefault)
	if got.MaxLLMCallsPerRun != wantTierCeiling.MaxLLMCallsPerRun {
		t.Errorf("env.Budget.MaxLLMCallsPerRun = %d, want TierDefault's ceiling %d", got.MaxLLMCallsPerRun, wantTierCeiling.MaxLLMCallsPerRun)
	}
	if got.MaxToolCallsPerRun != wantTierCeiling.MaxToolCallsPerRun {
		t.Errorf("env.Budget.MaxToolCallsPerRun = %d, want TierDefault's ceiling %d", got.MaxToolCallsPerRun, wantTierCeiling.MaxToolCallsPerRun)
	}
	// The tier ceiling must still be a real narrowing, not a no-op that
	// happens to equal the graph's numbers — guards against
	// budgetCeilingTable drifting to match chat_default_classic.yaml's
	// constants and this assertion passing vacuously.
	if wantTierCeiling.MaxLLMCallsPerRun >= graph.Budget.MaxLLMCallsPerRun || wantTierCeiling.MaxToolCallsPerRun >= graph.Budget.MaxToolCallsPerRun {
		t.Fatalf("premise broken: TierDefault's ceiling %+v is not below the graph's declared %+v — this test would no longer be checking the dial",
			wantTierCeiling, graph.Budget)
	}
	if got == (coreag.Budget{}) {
		t.Fatal("env.Budget is the zero value — production chat is running with no per-run caps at all")
	}
}
