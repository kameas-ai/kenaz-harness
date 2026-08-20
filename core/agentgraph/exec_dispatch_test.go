package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// tool-error-legibility-01PMDL02 WP01: the tool-role Message built for
	// the next LLMNode iteration must carry the same IsError signal —
	// otherwise the model sees a failed call rendered as an ordinary
	// success.
	toolMsgs := res.Outputs["tool_messages"].([]Message)
	if len(toolMsgs) != 1 || !toolMsgs[0].IsError {
		t.Errorf("expected tool message IsError=true; got %+v", toolMsgs)
	}
}

// denyNamedToolPolicy is a PolicyGate test fake that denies CheckTool
// only for one specific fully-qualified tool name, allowing every
// other check. Used to prove the fan-out keeps dispatching siblings of
// a denied call, which a policy gate that denies unconditionally
// cannot distinguish from "the whole node stopped."
type denyNamedToolPolicy struct {
	denied string
	err    error
}

func (p denyNamedToolPolicy) CheckFileRead(_ context.Context, _ string) error   { return nil }
func (p denyNamedToolPolicy) CheckFileWrite(_ context.Context, _ string) error  { return nil }
func (p denyNamedToolPolicy) CheckStateRead(_ context.Context, _ string) error  { return nil }
func (p denyNamedToolPolicy) CheckStateWrite(_ context.Context, _ string) error { return nil }
func (p denyNamedToolPolicy) CheckTool(_ context.Context, toolName string) error {
	if toolName == p.denied {
		return p.err
	}
	return nil
}

// TestToolDispatchExecutor_PolicyDenyBlocksCall is UNIT-15's dispatch-
// boundary half (trust-surfaces-that-fire-01PMZ202 WP17): a Cedar
// policy deny surfaced through env.Policy.CheckTool becomes a
// model-visible is_error result — matching the pre-existing hook-block
// shape — and does NOT fail the whole node: a sibling call in the same
// fan-out still dispatches and succeeds.
// Mutation: comment out the env.Policy.CheckTool call in
// exec_dispatch.go. Must fail (the denied tool actually runs).
func TestToolDispatchExecutor_PolicyDenyBlocksCall(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("kenaz__ok", "ok-result", false)
	tools.allow("x__y", "should-never-run", false)

	env := &Env{
		Tools: tools,
		State: NewRunState(),
		Policy: denyNamedToolPolicy{
			denied: "x__y",
			err:    errors.New(`forbid: Action::"use_tool" on Tool::"x__y"`),
		},
	}
	applyEnvDefaults(env)
	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{},
	}
	calls := []ToolCallRequest{
		{ID: "1", Name: "x__y", Arguments: `{}`},
		{ID: "2", Name: "kenaz__ok", Arguments: `{}`},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	results := res.Outputs["tool_results"].([]ToolResult)
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if !results[0].IsError {
		t.Errorf("denied call: expected IsError=true, got %+v", results[0])
	}
	if !strings.Contains(results[0].Content, "denied") {
		t.Errorf("denied call: expected denial content, got %q", results[0].Content)
	}
	if results[1].IsError || results[1].Content != "ok-result" {
		t.Errorf("sibling call: expected the fan-out to still run, got %+v", results[1])
	}

	// The denied tool's underlying implementation must never actually
	// have been invoked — the deny short-circuits before dispatch.
	tools.mu.Lock()
	defer tools.mu.Unlock()
	for _, c := range tools.calls {
		if c.Name == "x__y" {
			t.Errorf("denied tool x__y was dispatched to the underlying registry")
		}
	}
}

// TestToolDispatchExecutor_EnvironmentDriftHintAppended is WP02 of
// tool-error-legibility-01PMDL02: a genuine dispatch-level error (not a
// well-formed IsError ToolResult) that signature-matches a known
// environment-drift case gets the standard diagnostic suffix appended,
// while an unrelated dispatch error (like the plain "denied: ..." case
// in TestToolDispatchExecutor_ToolErrorBecomesIsError above) is left
// untouched.
func TestToolDispatchExecutor_EnvironmentDriftHintAppended(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.failWith("svc__read_file", `open /workspace/gone.txt: no such file or directory`)

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
		{ID: "1", Name: "svc__read_file", Arguments: `{}`},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	results := res.Outputs["tool_results"].([]ToolResult)
	if len(results) != 1 || !results[0].IsError {
		t.Fatalf("expected IsError result; got %+v", results)
	}
	if !strings.Contains(results[0].Content, "no such file or directory") {
		t.Fatalf("original error text must be preserved verbatim; got %q", results[0].Content)
	}
	if !strings.Contains(results[0].Content, "Environment-drift hint:") {
		t.Errorf("expected environment-drift hint appended; got %q", results[0].Content)
	}
}

// TestToolDispatchExecutor_PanicingToolYieldsIsError asserts that a panicking
// tool goroutine produces an is_error ToolResult (visible to the model), that
// sibling tool calls in the same fan-out still complete, and that the test
// process survives (FR-001).
func TestToolDispatchExecutor_PanicingToolYieldsIsError(t *testing.T) {
	t.Parallel()

	// panicTools satisfies ToolRegistry: "panic__tool" panics, "ok__tool" returns normally.
	pt := &panicTools{}

	env := &Env{
		RunID:    "run-panic",
		Tools:    pt,
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
		{ID: "c1", Name: "panic__tool", Arguments: `{}`},
		{ID: "c2", Name: "ok__tool", Arguments: `{}`},
	}

	// Execute must not panic; the test process survives.
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": calls})
	if err != nil {
		t.Fatalf("Execute returned unexpected error: %v", err)
	}

	results, ok := res.Outputs["tool_results"].([]ToolResult)
	if !ok {
		t.Fatalf("tool_results type: got %T, want []ToolResult", res.Outputs["tool_results"])
	}
	if len(results) != 2 {
		t.Fatalf("len(tool_results) = %d, want 2", len(results))
	}

	// Results preserve original order: c1 (panic), c2 (ok).
	if !results[0].IsError {
		t.Errorf("results[0].IsError = false; panicking tool should yield IsError=true")
	}
	if results[1].IsError {
		t.Errorf("results[1].IsError = true; ok tool should not be an error")
	}
	if results[1].Content != "ok-result" {
		t.Errorf("results[1].Content = %q, want %q", results[1].Content, "ok-result")
	}
}

// panicTools is a minimal ToolRegistry implementation for the panic test.
// "panic__tool" panics with a fixed string; "ok__tool" returns a normal result.
type panicTools struct{}

func (p *panicTools) Has(name string) bool {
	return name == "panic__tool" || name == "ok__tool"
}

func (p *panicTools) Call(_ context.Context, c ToolCall) (ToolResult, error) {
	if c.Name == "panic__tool" {
		panic("injected panic for test")
	}
	return ToolResult{Content: "ok-result"}, nil
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

// TestToolDispatchExecutor_DoomLoopFiresOnNthRepeat asserts the doom-loop
// guard trips on the Nth (default 3) near-identical call to the same
// tool with the same (normalized) arguments, across separate Execute
// invocations that share a run-scoped RunState — mirroring how a real
// run re-fires tool_dispatch once per LoopNode iteration.
func TestToolDispatchExecutor_DoomLoopFiresOnNthRepeat(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("svc__search", "no matches", true)

	env := &Env{
		RunID:    "run-doom-1",
		Tools:    tools,
		Counters: &RunCounters{},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)
	node := &Node{ID: "td", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{}}

	// Same tool, byte-identical arguments, three separate turns.
	call := ToolCallRequest{ID: "c", Name: "svc__search", Arguments: `{"query":"widgets"}`}

	var lastRes Result
	for i := 0; i < 3; i++ {
		call.ID = fmt.Sprintf("c%d", i)
		res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
			PortValues{"tool_calls": []ToolCallRequest{call}})
		if err != nil {
			t.Fatalf("Execute iter %d: %v", i, err)
		}
		lastRes = res
	}

	if got, _ := lastRes.Outputs["should_replan"].(bool); !got {
		t.Fatalf("should_replan = %v on 3rd identical call, want true", lastRes.Outputs["should_replan"])
	}
	var found bool
	for _, e := range lastRes.Events.Events {
		if e.Kind == EventDoomLoopDetected {
			found = true
			var payload map[string]any
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("EventDoomLoopDetected payload does not decode: %v", err)
			}
			if payload["tool"] != "svc__search" {
				t.Errorf("payload[tool] = %v, want svc__search", payload["tool"])
			}
			if e.RunID != "run-doom-1" {
				t.Errorf("event RunID = %q, want run-doom-1", e.RunID)
			}
		}
	}
	if !found {
		t.Fatalf("EventDoomLoopDetected not emitted on 3rd identical call")
	}

	// Sanity: the kind is a registered member of AllEventKinds so log
	// consumers that whitelist known kinds accept it.
	var registered bool
	for _, k := range AllEventKinds() {
		if k == EventDoomLoopDetected {
			registered = true
		}
	}
	if !registered {
		t.Fatalf("EventDoomLoopDetected missing from AllEventKinds()")
	}
}

// TestToolDispatchExecutor_DoomLoopDoesNotFireOnFirstTwoCalls asserts the
// guard stays quiet before the threshold is reached.
func TestToolDispatchExecutor_DoomLoopDoesNotFireOnFirstTwoCalls(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("svc__search", "no matches", true)
	env := &Env{
		RunID:    "run-doom-2",
		Tools:    tools,
		Counters: &RunCounters{},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)
	node := &Node{ID: "td", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{}}
	call := ToolCallRequest{ID: "c", Name: "svc__search", Arguments: `{"query":"widgets"}`}

	for i := 0; i < 2; i++ {
		call.ID = fmt.Sprintf("c%d", i)
		res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
			PortValues{"tool_calls": []ToolCallRequest{call}})
		if err != nil {
			t.Fatalf("Execute iter %d: %v", i, err)
		}
		if _, ok := res.Outputs["should_replan"]; ok {
			t.Fatalf("iter %d: should_replan set before threshold reached", i)
		}
		for _, e := range res.Events.Events {
			if e.Kind == EventDoomLoopDetected {
				t.Fatalf("iter %d: EventDoomLoopDetected fired before threshold reached", i)
			}
		}
	}
}

// TestToolDispatchExecutor_DoomLoopNotTrippedByPagination is the
// negative case the spec calls out explicitly: a model paginating
// through results with a changing offset must never be misdetected as a
// doom loop, even though it calls the same tool repeatedly.
func TestToolDispatchExecutor_DoomLoopNotTrippedByPagination(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("svc__list", "page", false)
	env := &Env{
		RunID:    "run-doom-3",
		Tools:    tools,
		Counters: &RunCounters{},
		State:    NewRunState(),
	}
	applyEnvDefaults(env)
	node := &Node{ID: "td", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{}}

	for page := 0; page < 5; page++ {
		call := ToolCallRequest{
			ID:        fmt.Sprintf("p%d", page),
			Name:      "svc__list",
			Arguments: fmt.Sprintf(`{"offset":%d,"limit":50}`, page*50),
		}
		res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
			PortValues{"tool_calls": []ToolCallRequest{call}})
		if err != nil {
			t.Fatalf("Execute page %d: %v", page, err)
		}
		if _, ok := res.Outputs["should_replan"]; ok {
			t.Fatalf("page %d: should_replan set for a varying-offset pagination call", page)
		}
		for _, e := range res.Events.Events {
			if e.Kind == EventDoomLoopDetected {
				t.Fatalf("page %d: EventDoomLoopDetected fired for a varying-offset pagination call", page)
			}
		}
	}
}

// TestToolDispatchExecutor_DoomLoopThresholdConfigurable asserts
// Budget.DoomLoopThreshold overrides DefaultDoomLoopThreshold.
func TestToolDispatchExecutor_DoomLoopThresholdConfigurable(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("svc__search", "no matches", true)
	env := &Env{
		RunID:    "run-doom-4",
		Tools:    tools,
		Counters: &RunCounters{},
		State:    NewRunState(),
		Budget:   Budget{DoomLoopThreshold: 2},
	}
	applyEnvDefaults(env)
	node := &Node{ID: "td", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{}}
	call := ToolCallRequest{Name: "svc__search", Arguments: `{"query":"widgets"}`}

	call.ID = "c0"
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": []ToolCallRequest{call}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, ok := res.Outputs["should_replan"]; ok {
		t.Fatalf("should_replan set on 1st call with threshold=2")
	}

	call.ID = "c1"
	res, err = toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": []ToolCallRequest{call}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := res.Outputs["should_replan"].(bool); !got {
		t.Fatalf("should_replan = %v on 2nd call with threshold=2, want true", res.Outputs["should_replan"])
	}
}
