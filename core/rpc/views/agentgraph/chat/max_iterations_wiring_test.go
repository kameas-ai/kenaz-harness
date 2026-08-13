package chat

// autonomy-knobs-live-01PMAG02 WP01 — maxIterations reconciliation.
//
// The pure-resolution half (feeding Settings.EffectiveMaxAgentTurns
// into the autonomy global layer, session/project overrides winning
// over it) is pinned in core/rpc's autonomy_maxturns_test.go, next to
// the production closure that builds real Layers from the session +
// project managers. These tests pin the other half: once
// chat.Config.AutonomyKnobs is wired, StartStream actually applies the
// resolved MaxIterations onto the LoopNode, and preserves the legacy
// cfg.MaxTurns() value when no AutonomyKnobs provider is wired at all.

import (
	"context"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// maxIterationsTestGraph is minimalChatGraph() plus a disconnected
// LoopNode, so applyMaxTurnsDial (which walks every node regardless of
// entrypoint reachability — see TestApplyMaxTurnsDial in
// chat_runner_test.go) has something to mutate. The LoopNode is never
// reached by the kernel; these tests only assert on the graph mutation
// StartStream performs before kernel.Run, not on run completion.
func maxIterationsTestGraph() coreag.Graph {
	return coreag.Graph{
		ID:          "test_chat_loop",
		Name:        "test chat graph with loop",
		Entrypoints: []string{"ask_user"},
		Nodes: []coreag.Node{
			{ID: "ask_user", Kind: coreag.NodeKindAsk, Attrs: coreag.AskAttrs{Question: "what?"}},
			{ID: "loop", Kind: coreag.NodeKindLoop, Attrs: coreag.LoopAttrs{MaxIterations: 3}},
		},
	}
}

// captureGraphEnvDefaults returns an EnvDefaults callback that records
// the first Env it sees, plus a helper to read the LoopNode's
// max_iterations off the captured graph once the run goroutine has
// reached kernel.Run.
type graphCapture struct {
	mu  sync.Mutex
	env *coreag.Env
}

func (c *graphCapture) capture(env *coreag.Env) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.env == nil {
		c.env = env
	}
}

func (c *graphCapture) loopMaxIterations(t *testing.T) int {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		env := c.env
		c.mu.Unlock()
		if env != nil {
			for _, n := range env.Graph.Nodes {
				if n.Kind == coreag.NodeKindLoop {
					return n.Attrs.(coreag.LoopAttrs).MaxIterations
				}
			}
			t.Fatal("captured graph has no LoopNode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("EnvDefaults callback never observed an Env")
	return 0
}

// FR-005: with no AutonomyKnobsProvider wired at all (the pre-mission
// shape, and every test/boot path that hasn't migrated), the LoopNode
// cap must come from cfg.MaxTurns() exactly as before this WP.
func TestChatRunner_MaxIterations_NoProviderUsesLegacyMaxTurns(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := maxIterationsTestGraph()
	capture := &graphCapture{}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   capture.capture,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := capture.loopMaxIterations(t); got != 25 {
		t.Errorf("LoopNode.MaxIterations = %d, want 25 (cfg.MaxTurns(), unchanged since AutonomyKnobs is nil)", got)
	}
}

// FR-005, the AutonomyKnobs-wired half: a resolved MaxIterations that
// matches the legacy cfg.MaxTurns() value (the "no project/session
// override" case, since production wiring seeds the global layer from
// Settings) must produce the identical LoopNode cap.
func TestChatRunner_MaxIterations_DefaultTierMatchesLegacyMaxTurns(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := maxIterationsTestGraph()
	capture := &graphCapture{}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   capture.capture,
		AutonomyKnobs: func(_ context.Context, sessionID string) autonomy.ResolvedKnobs {
			return autonomy.ResolvedKnobs{MaxIterations: 25}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := capture.loopMaxIterations(t); got != 25 {
		t.Errorf("LoopNode.MaxIterations = %d, want 25 (resolved knob matches the pre-mission default)", got)
	}
}

// Acceptance criterion: raising (or lowering) the autonomy panel's
// iteration dial changes the LoopNode cap. Simulated here by an
// AutonomyKnobsProvider whose resolved MaxIterations differs from
// cfg.MaxTurns() — the shape a session-layer override produces once
// production wiring resolves it (see core/rpc's
// resolveAutonomyKnobsWithSettingsFallback tests for the resolution
// half).
func TestChatRunner_MaxIterations_ResolvedKnobOverridesLegacyMaxTurns(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := maxIterationsTestGraph()
	capture := &graphCapture{}

	// AutonomyKnobs is evaluated inside the run goroutine (same as
	// EnvDefaults), so the observed sessionID needs the same mutex
	// discipline as graphCapture — read via loopMaxIterations() below
	// only after the goroutine has definitely reached kernel.Run.
	var sawMu sync.Mutex
	var sawSessionID string
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   capture.capture,
		AutonomyKnobs: func(_ context.Context, sessionID string) autonomy.ResolvedKnobs {
			sawMu.Lock()
			sawSessionID = sessionID
			sawMu.Unlock()
			return autonomy.ResolvedKnobs{MaxIterations: 7}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-override-test", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := capture.loopMaxIterations(t); got != 7 {
		t.Errorf("LoopNode.MaxIterations = %d, want 7 (the resolved session-layer override)", got)
	}
	sawMu.Lock()
	got := sawSessionID
	sawMu.Unlock()
	if got != "session-override-test" {
		t.Errorf("AutonomyKnobsProvider saw sessionID %q, want %q — the provider must be session-scoped", got, "session-override-test")
	}
}

// A resolved MaxIterations of zero (an unset/misconfigured knob) must
// not zero out the LoopNode cap — it falls back to cfg.MaxTurns().
func TestChatRunner_MaxIterations_ZeroResolvedFallsBackToLegacy(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := maxIterationsTestGraph()
	capture := &graphCapture{}

	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   capture.capture,
		AutonomyKnobs: func(_ context.Context, sessionID string) autonomy.ResolvedKnobs {
			return autonomy.ResolvedKnobs{} // zero value: MaxIterations == 0
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello")
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if got := capture.loopMaxIterations(t); got != 25 {
		t.Errorf("LoopNode.MaxIterations = %d, want 25 (fallback to cfg.MaxTurns() on a zero resolved knob)", got)
	}
}

// fix F8: the AutonomyKnobsProvider does real I/O in production (the
// global/project/session store reads core/rpc/api.go's
// autonomyKnobsProvider closure performs). Before this fix,
// StartStream called it directly from ~6 separate sites (maxTurns,
// the ask-tool catalog filter, WithRecapStyle's per-Generate closure,
// WithAskOnAmbiguity's per-Generate closure, Budget, NodeErrorPolicy)
// plus kernelToolAdapter re-invoked it on every tool call — dozens of
// redundant store reads per chatty turn. This pins that StartStream
// now resolves it exactly once and threads the resulting value to
// every consumer instead.
func TestChatRunner_MaxIterations_AutonomyKnobsProviderCalledExactlyOnce(t *testing.T) {
	t.Parallel()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := maxIterationsTestGraph()
	capture := &graphCapture{}

	var mu sync.Mutex
	calls := 0
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults:   capture.capture,
		AutonomyKnobs: func(_ context.Context, sessionID string) autonomy.ResolvedKnobs {
			mu.Lock()
			calls++
			mu.Unlock()
			return autonomy.ResolvedKnobs{MaxIterations: 25, RecapStyle: autonomy.RecapBrief}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "hello"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	// StartStream resolves synchronously before it returns; the async
	// run goroutine (Generate, tool calls) must not trigger a second
	// resolution, so it is safe to assert this immediately.
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("AutonomyKnobsProvider called %d times, want exactly 1", got)
	}
}
