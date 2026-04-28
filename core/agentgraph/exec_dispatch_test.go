package agentgraph

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestToolDispatchExecutor_DispatchesEveryCall asserts the executor
// fans every upstream tool_calls record out through the kernel
// ToolRegistry and emits a Message-shaped slice ready to feed back
// into the next LLMNode call.
func TestToolDispatchExecutor_DispatchesEveryCall(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("server__alpha", "alpha-result", false)
	tools.allow("server__beta", "beta-result", false)

	env := &Env{
		RunID:    "run-1",
		Tools:    tools,
		Counters: &RunCounters{},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{ParallelDispatch: true, MaxConcurrent: 4},
	}
	calls := []ToolCallRequest{
		{ID: "1", Name: "server__alpha", Arguments: `{"a":1}`},
		{ID: "2", Name: "server__beta", Arguments: `{"b":2}`},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	results, ok := res.Outputs["tool_results"].([]ToolResult)
	if !ok {
		t.Fatalf("tool_results: got %T", res.Outputs["tool_results"])
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	msgs, ok := res.Outputs["messages"].([]Message)
	if !ok {
		t.Fatalf("messages: got %T", res.Outputs["messages"])
	}
	if len(msgs) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(msgs))
	}
	for i, want := range []string{"alpha-result", "beta-result"} {
		if msgs[i].Role != "tool" {
			t.Errorf("messages[%d].Role = %q, want tool", i, msgs[i].Role)
		}
		if msgs[i].Content != want {
			t.Errorf("messages[%d].Content = %q, want %q", i, msgs[i].Content, want)
		}
	}

	// Both tools should have been called; order may vary because
	// parallel_dispatch is on. Sort and compare names.
	tools.mu.Lock()
	got := make([]string, 0, len(tools.calls))
	for _, c := range tools.calls {
		got = append(got, c.Name)
	}
	tools.mu.Unlock()
	sort.Strings(got)
	want := []string{"server__alpha", "server__beta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dispatched names = %v, want %v", got, want)
	}

	// tool_call_count surfaces the dispatch count.
	if got, ok := res.Outputs["tool_call_count"].(int); !ok || got != 2 {
		t.Errorf("tool_call_count = %v (%T), want 2", res.Outputs["tool_call_count"], res.Outputs["tool_call_count"])
	}
}

// TestToolDispatchExecutor_NoCallsIsNoOp asserts an empty tool_calls
// input produces empty output slices and signals tool_call_count==0
// so the LoopNode condition can break the agent loop.
func TestToolDispatchExecutor_NoCallsIsNoOp(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	env := &Env{
		Tools: tools,
		State: NewRunState(),
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": []ToolCallRequest{}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := res.Outputs["tool_call_count"]; got != 0 {
		t.Errorf("tool_call_count = %v, want 0", got)
	}
	if results, ok := res.Outputs["tool_results"].([]ToolResult); !ok || len(results) != 0 {
		t.Errorf("tool_results = %+v (%T), want empty slice", results, res.Outputs["tool_results"])
	}
	tools.mu.Lock()
	defer tools.mu.Unlock()
	if len(tools.calls) != 0 {
		t.Errorf("tools.calls = %d, want 0", len(tools.calls))
	}
}

// TestToolDispatchExecutor_PassesAssistantThrough asserts the executor
// surfaces the upstream LLMNode's `assistant` Message + assistant_text
// onto its output ports so an outside-loop session_write can read the
// final assistant turn from the LoopNode-flattened outputs.
func TestToolDispatchExecutor_PassesAssistantThrough(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	env := &Env{
		Tools: tools,
		State: NewRunState(),
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	asst := Message{Role: "assistant", Content: "final answer"}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{
			"tool_calls": []ToolCallRequest{},
			"assistant":  asst,
		})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, ok := res.Outputs["assistant_text"].(string); !ok || got != "final answer" {
		t.Errorf("assistant_text = %v, want 'final answer'", res.Outputs["assistant_text"])
	}
	if got, ok := res.Outputs["assistant"].(Message); !ok || got.Content != "final answer" {
		t.Errorf("assistant = %+v, want %+v", res.Outputs["assistant"], asst)
	}
}

// TestToolDispatchExecutor_FiresHookPostTool asserts the kernel's tool
// post-hook callback registry fires once per dispatched tool call so
// the chassis-side artifact-output capture seam re-introduced by the
// tool-dispatch-node mission catches every result.
func TestToolDispatchExecutor_FiresHookPostTool(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("svc__doit", "captured", false)

	env := &Env{
		Tools:     tools,
		SessionID: "session-7",
		State:     NewRunState(),
	}
	applyEnvDefaults(env)

	var fires atomic.Int32
	var seenName atomic.Value
	var seenResult atomic.Value
	env.Hooks.RegisterToolPostHook(func(_ context.Context, sessID, name, args, result string, _ time.Duration) {
		fires.Add(1)
		seenName.Store(name)
		seenResult.Store(result)
		if sessID != "session-7" {
			t.Errorf("session id = %q, want session-7", sessID)
		}
	})

	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	calls := []ToolCallRequest{
		{ID: "x", Name: "svc__doit", Arguments: `{"a":1}`},
	}
	if _, err := (toolDispatchExecutor{}).Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fires.Load() != 1 {
		t.Errorf("hook fires = %d, want 1", fires.Load())
	}
	if got := seenName.Load().(string); got != "svc__doit" {
		t.Errorf("tool name = %q, want svc__doit", got)
	}
	if got := seenResult.Load().(string); got != "captured" {
		t.Errorf("tool result = %q, want captured", got)
	}
}

// TestToolDispatchExecutor_ToolErrorBecomesIsError asserts a
// pool-level error from env.Tools.Call surfaces as IsError=true on the
// result and DOES NOT abort the dispatch — the model needs to see
// every call's outcome to recover.
func TestToolDispatchExecutor_ToolErrorBecomesIsError(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.deny("svc__broken")

	env := &Env{
		Tools: tools,
		State: NewRunState(),
	}
	applyEnvDefaults(env)
	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	calls := []ToolCallRequest{
		{ID: "1", Name: "svc__broken", Arguments: `{}`},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	results := res.Outputs["tool_results"].([]ToolResult)
	if len(results) != 1 || !results[0].IsError {
		t.Errorf("expected IsError result; got %+v", results)
	}
}

// TestToolDispatchExecutor_BudgetGate asserts the executor short-
// circuits when the per-run tool-call cap would be exceeded by the
// pending dispatch, surfacing ErrBudgetExceeded with EventBudgetCapHit.
func TestToolDispatchExecutor_BudgetGate(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	env := &Env{
		Tools:    tools,
		Counters: &RunCounters{ToolCallsMade: 5},
		Budget:   Budget{MaxToolCallsPerRun: 6},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)
	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	calls := []ToolCallRequest{
		{ID: "1", Name: "svc__a", Arguments: `{}`},
		{ID: "2", Name: "svc__b", Arguments: `{}`},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("expected ErrBudgetExceeded; got err=%v", err)
	}
	// One EventBudgetCapHit event should be emitted.
	var capHits int
	for _, e := range res.Events.Events {
		if e.Kind == EventBudgetCapHit {
			capHits++
		}
	}
	if capHits != 1 {
		t.Errorf("EventBudgetCapHit count = %d, want 1", capHits)
	}
}
