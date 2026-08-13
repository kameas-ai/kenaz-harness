package agentgraph

// iter_gate_dispatch_test.go — integration tests for the FR-010 dispatch-path
// iteration-gate wiring.
//
// These tests verify that the toolloop.ShouldCountIteration gate is correctly
// consulted by both dispatch executors (toolDispatchExecutor and the
// builtin-tool node executor) before they call env.Counters.AddTool(). The gate function itself is covered
// in core/toolloop/loop_test.go; this file only exercises the agentgraph
// dispatch-path integration.
//
// Acceptance criteria (FR-010):
//   - After 1 dispatch of kenaz__sleep, env.Counters.ToolCallsMade == 0.
//   - After 1 dispatch of a non-passive tool, env.Counters.ToolCallsMade == 1.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/tools/sleep"
)

// iterGateStubTools wraps stubTools (defined in exec_compute_test.go) with
// pre-registered kenaz__sleep and a non-passive test tool.
func newIterGateTools() *stubTools {
	t := newStubTools()
	t.allow(sleep.ToolName, `{"slept_s":1}`, false)
	t.allow("kenaz__glob", `{"matches":[]}`, false)
	t.allow(builtinToolNameFor(NodeKindSubagentDispatch), `{"branch_id":"b1"}`, false)
	return t
}

// ---- toolDispatchExecutor (model-driven dispatch) ----

// TestToolDispatchExecutor_PassiveTool_DoesNotIncrementCounter verifies that
// dispatching kenaz__sleep via the model-driven tool_dispatch executor does
// NOT increment ToolCallsMade (FR-010).
func TestToolDispatchExecutor_PassiveTool_DoesNotIncrementCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := toolDispatchExecutor{}
	node := &Node{
		ID:    "dispatch",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{MaxConcurrent: 1},
	}
	inputs := PortValues{
		"tool_calls": []ToolCallRequest{
			{ID: "c1", Name: sleep.ToolName, Arguments: `{"seconds":1}`},
		},
	}
	if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 0 {
		t.Errorf("ToolCallsMade after kenaz__sleep = %d, want 0 (FR-010)", got)
	}
}

// TestToolDispatchExecutor_ActiveTool_IncrementsCounter verifies that
// dispatching a non-passive tool via tool_dispatch DOES increment
// ToolCallsMade.
func TestToolDispatchExecutor_ActiveTool_IncrementsCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := toolDispatchExecutor{}
	node := &Node{
		ID:    "dispatch",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{MaxConcurrent: 1},
	}
	inputs := PortValues{
		"tool_calls": []ToolCallRequest{
			{ID: "c1", Name: "kenaz__glob", Arguments: `{"pattern":"*.go"}`},
		},
	}
	if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 1 {
		t.Errorf("ToolCallsMade after kenaz__glob = %d, want 1", got)
	}
}

// ---- builtin tool nodes (the `tool` archetype) ----
//
// The FR-010 iteration contract is what the `sleep` KIND exists to
// express: agentgraph-total-convergence-01PMGX01 WP04 declares it
// `budget: none` / `budget_consumes: []` in sleep.yaml, and the runtime
// half is this gate. Both halves must agree, so both are tested through
// the executor the manifest actually registers.

// TestBuiltinToolExecutor_PassiveKind_DoesNotIncrementCounter verifies the
// FR-010 invariant through the `sleep` node kind.
func TestBuiltinToolExecutor_PassiveKind_DoesNotIncrementCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := builtinToolExecutor{kind: NodeKindSleep, toolName: builtinToolNameFor(NodeKindSleep)}
	if ex.toolName != sleep.ToolName {
		t.Fatalf("naming contract drift: kind sleep dispatches %q, want %q", ex.toolName, sleep.ToolName)
	}
	node := &Node{
		ID:    "sleep-node",
		Kind:  NodeKindSleep,
		Attrs: SleepAttrs{Seconds: 1},
	}
	// The tool is a stub, so Call() returns immediately without sleeping.
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 0 {
		t.Errorf("ToolCallsMade after the sleep node = %d, want 0 (FR-010)", got)
	}
}

// TestBuiltinToolExecutor_ActiveKind_IncrementsCounter verifies a
// non-passive tool kind still charges the iteration budget.
func TestBuiltinToolExecutor_ActiveKind_IncrementsCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := builtinToolExecutor{
		kind:     NodeKindSubagentDispatch,
		toolName: builtinToolNameFor(NodeKindSubagentDispatch),
	}
	node := &Node{
		ID:    "dispatch-node",
		Kind:  NodeKindSubagentDispatch,
		Attrs: SubagentDispatchAttrs{Profile: "explore", Prompt: "go"},
	}
	if _, err := ex.Execute(context.Background(), env, node, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 1 {
		t.Errorf("ToolCallsMade after the subagent_dispatch node = %d, want 1", got)
	}
}
