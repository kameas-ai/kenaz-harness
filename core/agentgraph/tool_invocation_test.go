package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// tool_invocation_test.go — WP05 of
// agentgraph-total-convergence-01PMGX01: one tool-invocation path.
//
// The headline behaviour change these tests pin is DECLARED, not
// incidental (spec §5 requires it): the v2 `pre_tool_use` /
// `post_tool_use` lifecycle hooks now fire on the `tool_dispatch` path.
// Before this WP they were wired only into the static `tool` kind,
// which spec §1.4 documents as dead in production — so a user who
// configured a pre_tool_use hook to BLOCK a tool call got a hook that
// never ran. Each test below fails against the pre-WP05 tree.

// dispatchOneCall runs a single model-emitted tool call through
// tool_dispatch and returns the aggregated result.
func dispatchOneCall(t *testing.T, env *Env, toolName, argsJSON string) Result {
	t.Helper()
	ex := toolDispatchExecutor{}
	node := &Node{ID: "dispatch", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{MaxConcurrent: 1}}
	res, err := ex.Execute(context.Background(), env, node, PortValues{
		"tool_calls": []ToolCallRequest{{ID: "c1", Name: toolName, Arguments: argsJSON}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

func firstToolResult(t *testing.T, res Result) ToolResult {
	t.Helper()
	results, ok := res.Outputs["tool_results"].([]ToolResult)
	if !ok || len(results) != 1 {
		t.Fatalf("tool_results = %#v, want exactly one ToolResult", res.Outputs["tool_results"])
	}
	return results[0]
}

// TestToolDispatch_PreToolUseHookCanBlock is the safety-relevant half:
// a pre_tool_use hook that returns Blocked must stop the dispatch on
// the path production actually runs.
func TestToolDispatch_PreToolUseHookCanBlock(t *testing.T) {
	t.Parallel()
	hooks := &fakeLifecycleHookRunner{
		preResult: LifecycleMergedOutput{Blocked: true, BlockReason: "denied by policy"},
	}
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks}
	applyEnvDefaults(env)

	res := dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
	tr := firstToolResult(t, res)
	if !tr.IsError || !strings.Contains(tr.Content, "denied by policy") {
		t.Errorf("blocked call result = %+v, want an is_error carrying the block reason", tr)
	}
	if got := len(tools.snapshotCalls()); got != 0 {
		t.Errorf("ToolRegistry.Call ran %d times, want 0 — the hook blocked the call", got)
	}
	if len(hooks.snapshotPre()) != 1 {
		t.Errorf("pre_tool_use fired %d times, want 1", len(hooks.snapshotPre()))
	}
}

// TestToolDispatch_PreToolUseHookRewritesArgs pins the updated_input
// path: the hook's args are what reaches the tool.
func TestToolDispatch_PreToolUseHookRewritesArgs(t *testing.T) {
	t.Parallel()
	hooks := &fakeLifecycleHookRunner{
		preResult: LifecycleMergedOutput{UpdatedInput: json.RawMessage(`{"pattern":"*.md"}`)},
	}
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks}
	applyEnvDefaults(env)

	dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
	call := tools.lastCall()
	if call.Args["pattern"] != "*.md" {
		t.Errorf("dispatched pattern = %v, want the hook-rewritten *.md", call.Args["pattern"])
	}
}

// TestToolDispatch_PreToolUseHookInjectsContext pins additional_context
// reaching the PendingContext seam from the dispatch path.
func TestToolDispatch_PreToolUseHookInjectsContext(t *testing.T) {
	t.Parallel()
	hooks := &fakeLifecycleHookRunner{
		preResult: LifecycleMergedOutput{AdditionalContext: "remember: repo is read-only"},
	}
	pending := &pendingContextRecorder{}
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks, PendingContext: pending}
	applyEnvDefaults(env)

	dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
	if len(pending.entries) != 1 || pending.entries[0] != "remember: repo is read-only" {
		t.Errorf("PendingContext entries = %v, want the hook's additional_context", pending.entries)
	}
}

// TestToolDispatch_PostToolUseHookFires covers both outcomes: a
// successful call fires post_tool_use and may rewrite the output; a
// failing call fires post_tool_use_failure with the error text.
func TestToolDispatch_PostToolUseHookFires(t *testing.T) {
	t.Parallel()

	t.Run("success rewrites output", func(t *testing.T) {
		t.Parallel()
		hooks := &fakeLifecycleHookRunner{
			postResult: LifecycleMergedOutput{UpdatedMCPOutput: json.RawMessage(`"redacted"`)},
		}
		tools := newStubTools()
		tools.allow("kenaz__glob", `{"matches":["secret.go"]}`, false)
		env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks}
		applyEnvDefaults(env)

		res := dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
		if got := firstToolResult(t, res).Content; got != "redacted" {
			t.Errorf("result content = %q, want the hook-rewritten %q", got, "redacted")
		}
		post := hooks.snapshotPost()
		if len(post) != 1 || post[0].IsFailure {
			t.Errorf("post_tool_use calls = %+v, want one success call", post)
		}
	})

	t.Run("failure fires post_tool_use_failure", func(t *testing.T) {
		t.Parallel()
		hooks := &fakeLifecycleHookRunner{}
		tools := newStubTools()
		tools.allow("kenaz__glob", "", false)
		tools.failWith("kenaz__glob", "boom")
		env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks}
		applyEnvDefaults(env)

		dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)
		post := hooks.snapshotPost()
		if len(post) != 1 || !post[0].IsFailure || !strings.Contains(post[0].ErrMsg, "boom") {
			t.Errorf("post_tool_use calls = %+v, want one failure call carrying the error", post)
		}
	})
}

// TestToolInvocation_BothPathsShareTheGate is the convergence assertion
// itself: the tool-node path and the model-dispatch path must apply the
// same hook policy to the same tool. It runs one tool both ways against
// identical hook wiring and requires identical observable outcomes.
func TestToolInvocation_BothPathsShareTheGate(t *testing.T) {
	t.Parallel()

	newEnv := func() (*Env, *fakeLifecycleHookRunner, *stubTools) {
		hooks := &fakeLifecycleHookRunner{
			preResult: LifecycleMergedOutput{Blocked: true, BlockReason: "nope"},
		}
		tools := newStubTools()
		tools.allow(builtinToolNameFor(NodeKindSubagentDispatch), "ok", false)
		env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks}
		applyEnvDefaults(env)
		return env, hooks, tools
	}

	// Path A: the builtin-tool node kind.
	envA, hooksA, toolsA := newEnv()
	exA := builtinToolExecutor{
		kind:     NodeKindSubagentDispatch,
		toolName: builtinToolNameFor(NodeKindSubagentDispatch),
	}
	resA, err := exA.Execute(context.Background(), envA,
		&Node{ID: "n", Kind: NodeKindSubagentDispatch,
			Attrs: SubagentDispatchAttrs{Profile: "explore", Prompt: "go"}}, nil)
	if err != nil {
		t.Fatalf("node path Execute: %v", err)
	}
	trA, _ := resA.Outputs["result"].(ToolResult)

	// Path B: the same tool, model-dispatched.
	envB, hooksB, toolsB := newEnv()
	trB := firstToolResult(t, dispatchOneCall(t, envB,
		builtinToolNameFor(NodeKindSubagentDispatch), `{"profile":"explore","prompt":"go"}`))

	if trA.IsError != trB.IsError || trA.Content != trB.Content {
		t.Errorf("the two tool-invocation paths disagreed on a blocked call:\n node path: %+v\n dispatch:  %+v", trA, trB)
	}
	if len(hooksA.snapshotPre()) != len(hooksB.snapshotPre()) {
		t.Errorf("pre_tool_use fired %d times on the node path and %d on the dispatch path",
			len(hooksA.snapshotPre()), len(hooksB.snapshotPre()))
	}
	if len(toolsA.snapshotCalls()) != 0 || len(toolsB.snapshotCalls()) != 0 {
		t.Errorf("a blocked call reached the ToolRegistry: node=%d dispatch=%d",
			len(toolsA.snapshotCalls()), len(toolsB.snapshotCalls()))
	}
}

// TestToolInvocation_IterationGateIsShared pins that the FR-010 passive
// gate is applied by the one shared helper, so a passive tool costs no
// iteration slot no matter which path invoked it.
func TestToolInvocation_IterationGateIsShared(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", SessionID: "s"}
	applyEnvDefaults(env)
	chargeToolIteration(env, "kenaz__sleep")
	if got := env.Counters.ToolCallsMade; got != 0 {
		t.Errorf("passive tool charged %d iterations, want 0 (FR-010)", got)
	}
	chargeToolIteration(env, "kenaz__glob")
	if got := env.Counters.ToolCallsMade; got != 1 {
		t.Errorf("active tool charged %d iterations, want 1", got)
	}
}

// TestToolPostDispatch_FailureSkipsResultEvent documents the one place
// the two paths intentionally diverge AFTER the shared helper returns:
// on failure the helper emits no EventToolResult, because the caller
// decides whether the failure becomes a returned error (tool node) or a
// model-readable is_error record (dispatch fan-out).
func TestToolPostDispatch_FailureSkipsResultEvent(t *testing.T) {
	t.Parallel()
	env := &Env{RunID: "r", SessionID: "s"}
	applyEnvDefaults(env)
	tc := toolCallContext{NodeID: "n", NodeKind: NodeKindToolDispatch, ToolName: "kenaz__glob"}
	_, events := toolPostDispatch(context.Background(), env, tc, nil, ToolResult{}, errors.New("boom"), nil)
	for _, e := range events.Events {
		if e.Kind == EventToolResult {
			t.Errorf("failure path emitted %s; the caller owns failure reporting", EventToolResult)
		}
	}
}
