package chat

// v0.86.0 unwired sweep (2026-10-04): hook additional_context used to be
// dropped on every chat run because nothing set env.PendingContext. These
// tests pin both halves — the runner hands its queue to the run's Env and
// a real pre_tool_use hook's additional_context lands in it (write half),
// and the LLMProviderAdapter drains the queue into the next model call's
// system prompt exactly once (read half).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
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

	// An auxiliary call (router / review / escalation — StreamToChat
	// false) must leave the queue alone.
	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base"}); err != nil {
		t.Fatalf("Generate (aux): %v", err)
	}
	if sys := reg.snapshot().System; strings.Contains(sys, "repo uses tabs") {
		t.Fatalf("an auxiliary call consumed the hook context:\n%s", sys)
	}
	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base", StreamToChat: true}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sys := reg.snapshot().System
	if !strings.Contains(sys, pendingContextHeading) || !strings.Contains(sys, "repo uses tabs") {
		t.Fatalf("hook context missing from the next model call's system prompt:\n%s", sys)
	}
	if strings.Contains(sys, "other session") {
		t.Fatalf("another session's hook context leaked into s1:\n%s", sys)
	}

	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base", StreamToChat: true}); err != nil {
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

// takeProbeLLM sits where env.LLM sits and runs the REAL adapter's
// takePendingContext against the runner's REAL queue for every request
// the routed graph issues, recording who took what. It lets the routed
// topology (assistant_turn, router, exit-gate review) decide which calls
// are primary through the StreamToChat flags its own executors set,
// instead of the test asserting a flag it set itself.
type takeProbeLLM struct {
	inner     *stubLLM
	runner    **ChatRunner
	sessionID string

	mu    sync.Mutex
	takes []probeTake
}

type probeTake struct {
	primary bool
	prompt  string
	got     []string
}

func (p *takeProbeLLM) Generate(ctx context.Context, req coreag.LLMRequest) (coreag.LLMResponse, error) {
	adapter := (&LLMProviderAdapter{}).WithSessionID(p.sessionID).withPendingContext((*p.runner).pendingContext)
	got := adapter.takePendingContext(req)
	p.mu.Lock()
	p.takes = append(p.takes, probeTake{primary: req.StreamToChat, prompt: req.SystemPrompt, got: got})
	p.mu.Unlock()
	return p.inner.Generate(ctx, req)
}

func (p *takeProbeLLM) snapshot() []probeTake {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]probeTake(nil), p.takes...)
}

// TestRoutedTurn_PostToolUseContextSkipsExitGate is the review's M2 pin.
// Routed graph: assistant_turn calls a tool AND chooses "done", so the
// post_tool_use hook's additional_context is queued and the very next
// model call is the exit gate's private verdict. Before the fix every
// Generate drained the queue, so the verdict consumed the context and
// the assistant never saw it. Now the exit gate takes nothing and the
// session's next assistant_turn (turn 2) receives it.
func TestRoutedTurn_PostToolUseContextSkipsExitGate(t *testing.T) {
	t.Parallel()
	const hookContext = "sweep-860 M2: the search index is stale, re-verify dates"

	builtins := hooks.NewBuiltinRegistry()
	builtins.RegisterGenericFire("m2-post",
		func(_ context.Context, _ string, _ any, _ map[string]any) (hooks.HookOutput, error) {
			return hooks.HookOutput{AdditionalContext: hookContext}, nil
		},
		hooks.BuiltinDescriptor{ID: "m2-post", Name: "m2-post", Events: []string{hooks.EventPostToolUse}},
	)
	registry, err := hooks.NewRegistry("")
	if err != nil {
		t.Fatalf("hooks.NewRegistry: %v", err)
	}
	if err := registry.Add(hooks.Hook{
		ID: "h-m2", Name: "m2", Event: hooks.EventPostToolUse,
		Kind: hooks.KindBuiltin, Enabled: true, Builtin: "m2-post",
	}); err != nil {
		t.Fatalf("registry.Add: %v", err)
	}
	hookRunner := hooks.NewRunner(hooks.Config{Registry: registry, Builtins: builtins})

	stub := &stubLLM{}
	// Turn 1, call 1: assistant_turn — a tool call plus the fused "done".
	stub.push(stubLLMResponse{
		stream: []coreag.StreamEvent{
			{Kind: coreag.StreamEventTool, ToolID: "tu-1", ToolName: "search__web", ToolArgs: `{"q":"x"}`},
		},
		resp: coreag.LLMResponse{
			Content:      `checked {"next_choice": "done"}`,
			FinishReason: "tool_use",
			ToolCalls:    []coreag.ToolCallRequest{{ID: "tu-1", Name: "search__web", Arguments: `{"q":"x"}`}},
		},
	})
	// Turn 1, call 2: the exit gate's verdict.
	stub.push(stubLLMResponse{
		resp: coreag.LLMResponse{Content: `{"verdict": "pass", "reason": "ok"}`, FinishReason: "stop"},
	})
	// Turn 2, call 1: assistant_turn.
	stub.push(stubLLMResponse{
		stream: []coreag.StreamEvent{{Kind: coreag.StreamEventText, Text: "second"}},
		resp:   coreag.LLMResponse{Content: `second {"next_choice": "done"}`, FinishReason: "stop"},
	})
	// Turn 2, call 2: the exit gate's verdict.
	stub.push(stubLLMResponse{
		resp: coreag.LLMResponse{Content: `{"verdict": "pass", "reason": "ok"}`, FinishReason: "stop"},
	})
	tools := newStubTools("search__web")
	tools.push(coreag.ToolResult{Content: `{"result":"ok"}`})

	var chatRunner *ChatRunner
	probe := &takeProbeLLM{inner: stub, runner: &chatRunner, sessionID: "session-m2"}
	broker := &recordingBroker{}
	graph := loadRoutedChatGraph(t)
	chatRunner, err = New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      stubRegistry{},
		Broker:        broker,
		HistoryWriter: &recordingHistoryWriter{},
		History:       staticHistoryReader{msgs: []coreag.Message{{Role: "user", Content: "search x"}}},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		EnvDefaults: func(env *coreag.Env) {
			env.LLM = probe
			env.Tools = tools
			env.LifecycleHooks = &hooks.LifecycleRunnerAdapter{Runner: hookRunner}
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for turn := 1; turn <= 2; turn++ {
		if _, err := chatRunner.StartStream(context.Background(), "profile-1", "session-m2", "", "search x"); err != nil {
			t.Fatalf("turn %d StartStream: %v", turn, err)
		}
		if closed := waitForClosedN(t, broker, turn); closed.Reason == "backend-error" {
			t.Fatalf("turn %d failed: %q", turn, closed.Message)
		}
	}

	takes := probe.snapshot()
	if len(takes) < 4 {
		t.Fatalf("model calls = %d, want >= 4 (2 turns x assistant_turn + exit gate): %+v", len(takes), takes)
	}
	sawAuxAfterHook := false
	deliveredTo := -1
	for i, tk := range takes {
		if !tk.primary {
			if i > 0 {
				sawAuxAfterHook = true
			}
			if len(tk.got) > 0 {
				t.Fatalf("auxiliary call %d (prompt %.60q) consumed the hook context %q — it belongs to assistant_turn", i, tk.prompt, tk.got)
			}
			continue
		}
		for _, g := range tk.got {
			if g == hookContext {
				deliveredTo = i
			}
		}
	}
	if !sawAuxAfterHook {
		t.Fatal("no auxiliary (exit-gate) call followed the tool call — the scenario did not exercise M2")
	}
	if deliveredTo < 0 {
		t.Fatalf("hook context never reached an assistant_turn call: %+v", takes)
	}
	if deliveredTo == 0 {
		t.Fatalf("context delivered to the call that PRECEDED the tool call — impossible ordering: %+v", takes)
	}
}

// waitForClosedN waits for the n-th llm:stream-closed event (1-based).
func waitForClosedN(t *testing.T, broker *recordingBroker, n int) StreamClosedPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		seen := 0
		for _, e := range broker.snapshot() {
			if e.topic == "llm:stream-closed" {
				seen++
				if seen == n {
					return e.payload.(StreamClosedPayload)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not see stream-closed #%d within 3s", n)
	return StreamClosedPayload{}
}

// failingRegistry is capturingRegistry whose Stream always errors.
type failingRegistry struct{ capturingRegistry }

func (r *failingRegistry) Stream(_ context.Context, req corellm.GenerationRequest) (corellm.Stream, error) {
	r.mu.Lock()
	r.lastReq = req
	r.mu.Unlock()
	return nil, errors.New("provider down")
}

// TestPendingContext_FailedPrimaryCallRequeues pins review L3: a provider
// error on the primary call used to lose the drained hook context.
func TestPendingContext_FailedPrimaryCallRequeues(t *testing.T) {
	q := newPendingContextQueue()
	_ = q.AppendSystemContext(context.Background(), "s1", "older")
	adapter := NewLLMProviderAdapter(&failingRegistry{}, "p", "m", nil, nil).
		WithSessionID("s1").
		withPendingContext(q)
	if _, err := adapter.Generate(context.Background(), coreag.LLMRequest{SystemPrompt: "base", StreamToChat: true}); err == nil {
		t.Fatal("Generate succeeded against a failing registry")
	}
	_ = q.AppendSystemContext(context.Background(), "s1", "newer")
	got := q.take("s1")
	if len(got) != 2 || got[0] != "older" || got[1] != "newer" {
		t.Fatalf("queue after failed call = %q, want [older newer] (re-queued at the front)", got)
	}
}

// TestPendingContext_ForgetSession pins review L5 (delete teardown).
func TestPendingContext_ForgetSession(t *testing.T) {
	r := &ChatRunner{pendingContext: newPendingContextQueue()}
	_ = r.pendingContext.AppendSystemContext(context.Background(), "gone", "x")
	_ = r.pendingContext.AppendSystemContext(context.Background(), "kept", "y")
	r.ForgetSession("gone")
	if got := r.pendingContext.take("gone"); got != nil {
		t.Fatalf("deleted session's queue survived: %q", got)
	}
	if got := r.pendingContext.take("kept"); len(got) != 1 {
		t.Fatalf("ForgetSession disturbed another session: %q", got)
	}
	var nilRunner *ChatRunner
	nilRunner.ForgetSession("x") // nil-safe
}
