package chat

// v0.86.0 unwired sweep (2026-10-04): hook additional_context used to be
// dropped on every chat run because nothing set env.PendingContext. These
// tests pin both halves — the runner hands its queue to the run's Env and
// a real pre_tool_use hook's additional_context lands in it (write half),
// and the LLMProviderAdapter drains the queue into the next model call's
// system prompt exactly once (read half).

import (
	"context"
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
)

func TestPendingContext_GenerateDrainsIntoSystemPromptOnce(t *testing.T) {
	q := newPendingContextQueue()
	reg := &capturingRegistry{}
	adapter := NewLLMProviderAdapter(reg, "p", "m", nil, nil).
		WithSessionID("s1").
		withPendingContext(q)

	var appender coreag.PendingContextAppender = q
	if err := appender.AppendSystemContext(context.Background(), "s1", "repo uses tabs"); err != nil {
		t.Fatalf("AppendSystemContext: %v", err)
	}
	// Another session's context must not leak into s1's prompt.
	_ = appender.AppendSystemContext(context.Background(), "s2", "other session")

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sys := reg.snapshot().System
	if !strings.Contains(sys, pendingContextHeading) || !strings.Contains(sys, "repo uses tabs") {
		t.Fatalf("hook context missing from the next model call's system prompt:\n%s", sys)
	}
	if strings.Contains(sys, "other session") {
		t.Fatalf("another session's hook context leaked into s1:\n%s", sys)
	}

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate #2: %v", err)
	}
	if sys2 := reg.snapshot().System; strings.Contains(sys2, "repo uses tabs") {
		t.Fatalf("drained context was re-sent on the following call:\n%s", sys2)
	}
	if got := q.drain("s2"); !strings.Contains(got, "other session") {
		t.Fatalf("s2's queue was disturbed by s1's drain: %q", got)
	}
}

func TestPendingContext_QueueIsBounded(t *testing.T) {
	q := newPendingContextQueue()
	for i := 0; i < maxPendingContextEntries+10; i++ {
		_ = q.AppendSystemContext(context.Background(), "s", "x")
	}
	if n := len(q.m["s"]); n != maxPendingContextEntries {
		t.Fatalf("queue length = %d, want cap %d", n, maxPendingContextEntries)
	}
}

// TestChatGraph_PreToolUseAdditionalContext_ReachesPendingQueue drives a
// REAL hooks.Runner + Registry + saved pre_tool_use hook through the
// production chat graph via the same EnvDefaults seam api.go uses. Before
// the sweep, StartStream never set env.PendingContext, so this queue
// stayed empty and the hook output vanished.
func TestChatGraph_PreToolUseAdditionalContext_ReachesPendingQueue(t *testing.T) {
	t.Parallel()

	const hookContext = "sweep-860: the repo forbids network calls in tests"
	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("sweep860-context",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{AdditionalContext: hookContext}, nil
		},
		hooks.BuiltinDescriptor{ID: "sweep860-context", Name: "sweep860-context", Events: []string{hooks.EventPreToolUse}},
	)
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-ctx", Name: "ctx", Event: hooks.EventPreToolUse,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: "sweep860-context",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	runner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "search__web", ToolArgs: `{"q":"hello"}`},
		},
		resp: coreag.LLMResponse{
			FinishReason: "tool_use",
			ToolCalls:    []coreag.ToolCallRequest{{ID: "tu-1", Name: "search__web", Arguments: `{"q":"hello"}`}},
		},
	})
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "done"}},
		resp:   coreag.LLMResponse{Content: "done", FinishReason: "stop"},
	})
	tools := newStubTools("search__web")
	tools.push(coreag.ToolResult{Content: `{"result":"ok"}`})

	broker := &recordingBroker{}
	graph := loadProductionChatGraph(t)
	chatRunner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "search hello"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = llm
			env.Tools = tools
			env.LifecycleHooks = &hooks.LifecycleRunnerAdapter{Runner: runner}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := chatRunner.StartStream(context.Background(), "profile-1", "session-1", "", "search hello"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if closed := waitForClosed(t, broker); closed.Reason == "backend-error" {
		t.Fatalf("run failed: %q", closed.Message)
	}
	if calls := tools.snapshotCalls(); len(calls) != 1 {
		t.Fatalf("tool calls = %d, want 1 (the hook must not block)", len(calls))
	}
	// env.LLM is the stub here, so nothing drained the queue: the entry
	// the real hook produced must still be waiting for the session's next
	// LLMProviderAdapter call (pinned by the drain test above).
	if got := chatRunner.pendingContext.drain("session-1"); !strings.Contains(got, hookContext) {
		t.Fatalf("pending context for session-1 = %q, want it to carry the hook's additional_context", got)
	}
}
