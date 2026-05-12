package agentgraph

// iter_gate_dispatch_test.go — integration tests for the FR-010 dispatch-path
// iteration-gate wiring.
//
// These tests verify that the toolloop.ShouldCountIteration gate is correctly
// consulted by both dispatch executors (toolDispatchExecutor and toolExecutor)
// before they call env.Counters.AddTool(). The gate function itself is covered
// in core/toolloop/loop_test.go; this file only exercises the agentgraph
// dispatch-path integration.
//
// Acceptance criteria (FR-010):
//   - After 1 dispatch of kaneaz__sleep, env.Counters.ToolCallsMade == 0.
//   - After 1 dispatch of a non-passive tool, env.Counters.ToolCallsMade == 1.

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/tools/sleep"
)

// iterGateStubTools wraps stubTools (defined in exec_compute_test.go) with
// pre-registered kaneaz__sleep and a non-passive test tool.
func newIterGateTools() *stubTools {
	t := newStubTools()
	t.allow(sleep.ToolName, `{"slept_s":1}`, false)
	t.allow("kaneaz__glob", `{"matches":[]}`, false)
	return t
}

// ---- toolDispatchExecutor (model-driven dispatch) ----

// TestToolDispatchExecutor_PassiveTool_DoesNotIncrementCounter verifies that
// dispatching kaneaz__sleep via the model-driven tool_dispatch executor does
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
		t.Errorf("ToolCallsMade after kaneaz__sleep = %d, want 0 (FR-010)", got)
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
			{ID: "c1", Name: "kaneaz__glob", Arguments: `{"pattern":"*.go"}`},
		},
	}
	if _, err := ex.Execute(context.Background(), env, node, inputs); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 1 {
		t.Errorf("ToolCallsMade after kaneaz__glob = %d, want 1", got)
	}
}

// ---- toolExecutor (static tool node) ----

// TestToolExecutor_PassiveTool_DoesNotIncrementCounter verifies the same
// FR-010 invariant through the static toolExecutor path (NodeKindTool).
func TestToolExecutor_PassiveTool_DoesNotIncrementCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := toolExecutor{}
	node := &Node{
		ID:    "sleep-node",
		Kind:  NodeKindTool,
		Attrs: ToolAttrs{Name: sleep.ToolName},
	}
	// Use a fast 0-second args payload — the tool is a stub, so the actual
	// Call() returns immediately without sleeping.
	if _, err := ex.Execute(context.Background(), env, node, PortValues{
		"args": map[string]any{"seconds": 1},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 0 {
		t.Errorf("ToolCallsMade after static kaneaz__sleep = %d, want 0 (FR-010)", got)
	}
}

// TestToolExecutor_ActiveTool_IncrementsCounter verifies the static
// toolExecutor still increments the counter for non-passive tools.
func TestToolExecutor_ActiveTool_IncrementsCounter(t *testing.T) {
	t.Parallel()
	tools := newIterGateTools()
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)

	ex := toolExecutor{}
	node := &Node{
		ID:    "glob-node",
		Kind:  NodeKindTool,
		Attrs: ToolAttrs{Name: "kaneaz__glob"},
	}
	if _, err := ex.Execute(context.Background(), env, node, PortValues{
		"args": map[string]any{"pattern": "*.go"},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if got := env.Counters.ToolCallsMade; got != 1 {
		t.Errorf("ToolCallsMade after static kaneaz__glob = %d, want 1", got)
	}
}
