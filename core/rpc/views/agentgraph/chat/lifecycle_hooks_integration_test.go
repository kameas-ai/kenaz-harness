package chat

// trust-surfaces-that-fire-01PMZ202 WP09 / UNIT-8, AC-06.
//
// These integration tests drive a REAL core/hooks.Runner backed by a
// REAL core/hooks.Registry with a REAL saved pre_tool_use hook through a
// full production chat_default.yaml run. They are deliberately NOT the
// vacuous shape spec.md calls out — the six pre-existing
// core/agentgraph/tool_invocation_test.go tests construct an
// `Env{LifecycleHooks: <fake>}` literal directly, which proves the
// executor's read side works but says nothing about production wiring
// (all six passed before WP09, when nothing ever constructed a real
// LifecycleRunnerAdapter). Here, LifecycleHooks is set through the same
// `EnvDefaults func(*Env)` seam production wiring uses
// (core/rpc/api.go's envDefaults closure, mirrored by
// core/rpc/graph_manager_lifecycle_hooks_test.go's proof that
// newGraphManagerWithDeps populates exactly this field), and the
// adapter wraps a genuine Runner + Registry + saved Hook — no fake
// implements cedar.PermissionHookRunner or agentgraph.LifecycleHookRunner
// here.

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
)

// TestChatGraph_LifecycleHooks_PreToolUseBlocks: a real pre_tool_use
// hook returning decision:"block" stops the tool call. MergeOutputs
// semantics are asserted (the tool implementation must never run) rather
// than assumed.
func TestChatGraph_LifecycleHooks_PreToolUseBlocks(t *testing.T) {
	t.Parallel()

	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("wp09-deny-all",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{Decision: "block", Reason: "wp09-test-block"}, nil
		},
		hooks.BuiltinDescriptor{ID: "wp09-deny-all", Name: "wp09-deny-all", Events: []string{hooks.EventPreToolUse}},
	)
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-deny", Name: "deny", Event: hooks.EventPreToolUse,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: "wp09-deny-all",
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
			Content:      "",
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-1", Name: "search__web", Arguments: `{"q":"hello"}`},
			},
		},
	})
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "cannot search right now"}},
		resp:   coreag.LLMResponse{Content: "cannot search right now", FinishReason: "stop"},
	})
	tools := newStubTools("search__web")
	// Deliberately no queued result: if the tool is ever actually
	// dispatched, stubTools.Call still returns a default {"ok"} result
	// rather than failing the test — the assertion below on
	// tools.snapshotCalls() is what must catch a wiring regression, not
	// an empty-queue panic.

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
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	if calls := tools.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("tools.calls = %+v, want empty — the pre_tool_use hook returned decision:block and must have stopped dispatch entirely", calls)
	}
	if got := llm.calls.Load(); got != 2 {
		t.Errorf("llm.calls = %d, want 2 (blocked tool_use turn + finish turn)", got)
	}
}

// TestChatGraph_LifecycleHooks_UpdatedInputRewritesArgs: a real
// pre_tool_use hook returning updated_input rewrites the arguments the
// tool implementation actually receives.
func TestChatGraph_LifecycleHooks_UpdatedInputRewritesArgs(t *testing.T) {
	t.Parallel()

	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("wp09-rewrite",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{UpdatedInput: []byte(`{"q":"rewritten-by-hook"}`)}, nil
		},
		hooks.BuiltinDescriptor{ID: "wp09-rewrite", Name: "wp09-rewrite", Events: []string{hooks.EventPreToolUse}},
	)
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-rewrite", Name: "rewrite", Event: hooks.EventPreToolUse,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: "wp09-rewrite",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	runner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	llm := &stubLLM{}
	llm.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "search__web", ToolArgs: `{"q":"original"}`},
		},
		resp: coreag.LLMResponse{
			Content:      "",
			FinishReason: "tool_use",
			ToolCalls: []coreag.ToolCallRequest{
				{ID: "tu-1", Name: "search__web", Arguments: `{"q":"original"}`},
			},
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
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "search original"}}},
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

	if _, err := chatRunner.StartStream(context.Background(), "profile-1", "session-1", "", "search original"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("Reason = %q, want non-error; msg=%q", closed.Reason, closed.Message)
	}

	calls := tools.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("len(tools.calls) = %d, want 1; calls=%+v", len(calls), calls)
	}
	if got, _ := calls[0].Args["q"].(string); got != "rewritten-by-hook" {
		t.Errorf("tools.calls[0].Args[\"q\"] = %q, want %q (the hook's updated_input must reach the dispatched call)", got, "rewritten-by-hook")
	}
}
