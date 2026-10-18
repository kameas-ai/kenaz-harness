package rpc

// wf_tool_caller_adapter_test.go — automation-actually-runs-01PMZ404
// UNIT-6 (AC-007). corewf.Deps.Tools (the ToolCaller a tool_call step
// dispatches against) was never assigned in production, so every
// tool_call step failed with "no ToolCaller wired" (core/workflows/
// runners.go:376) regardless of the pool's state. wfToolCallerAdapter is
// the third adapter over the same toolloop.MCPPool + wfToolGate pairing
// wfMCPCallerAdapter (mcp_call) and wfToolDispatcherAdapter (model_turn)
// already use (spec D-5: one Cedar/permission/confirm-each path for all
// three dispatch surfaces).
//
// Tests:
//  1. TestWfToolCallerAdapter_HappyPath — args marshalled, string result
//     unwrapped, ToolResult.IsError left false.
//  2. TestWfToolCallerAdapter_NilPool — nil pool returns a non-nil error,
//     never a panic.
//  3. TestWfToolCallerAdapter_ToolExecForbidDiscriminates — the SAME
//     proof TestWfToolDispatcher_ToolExecForbidDiscriminates makes for
//     the model_turn path, run against wfToolCallerAdapter instead: a
//     forbid on kenaz__bash denies via the real Cedar engine and never
//     reaches the pool; an unrelated tool still dispatches. Proves the
//     tool_call surface shares the identical gate, not a fourth
//     independently-drifting copy of it.
//  4. TestWfToolCallerAdapter_ProductionPath_ReachesPool — drives the
//     REAL production wiring (core.New + rpc.New, api_cedar_gate_wiring_
//     test.go's cedarWiringAPI helper) through a tool_call step and
//     asserts the failure is NOT "no ToolCaller wired" — i.e. wfDeps.
//     Tools is non-nil in the actual constructed Engine, not merely in a
//     hand-built test fixture (CLAUDE.md blind spot #2 / spec §10 rule
//     3). *Mutation*: comment out the `wfDeps.Tools = ...` assignment at
//     core/rpc/api.go and this test goes red with "no ToolCaller wired"
//     — recorded in the WP commit body, not re-run here.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

func TestWfToolCallerAdapter_HappyPath(t *testing.T) {
	t.Parallel()
	pool := &recordingPool{output: json.RawMessage(`"tool output"`)}
	adapter := &wfToolCallerAdapter{pool: pool}

	got, err := adapter.Call(context.Background(), "kenaz__bash", map[string]any{"cmd": "echo hi"})
	if err != nil {
		t.Fatalf("Call: unexpected error: %v", err)
	}
	if got.IsError {
		t.Errorf("ToolResult.IsError = true, want false")
	}
	if got.Content != "tool output" {
		t.Errorf("Content = %q, want unwrapped %q", got.Content, "tool output")
	}
	calls := pool.snapshot()
	if len(calls) != 1 {
		t.Fatalf("pool called %d times, want 1", len(calls))
	}
	if calls[0].Server != "kenaz" || calls[0].Tool != "bash" {
		t.Errorf("dispatched to %s.%s, want kenaz.bash", calls[0].Server, calls[0].Tool)
	}
	var decoded map[string]any
	if err := json.Unmarshal(calls[0].Args, &decoded); err != nil {
		t.Fatalf("args not valid JSON: %v", err)
	}
	if decoded["cmd"] != "echo hi" {
		t.Errorf("args = %v, want cmd=echo hi", decoded)
	}
}

func TestWfToolCallerAdapter_NilPool(t *testing.T) {
	t.Parallel()
	adapter := &wfToolCallerAdapter{}
	_, err := adapter.Call(context.Background(), "kenaz__bash", nil)
	if err == nil {
		t.Fatal("expected a non-nil error for a nil pool, got nil")
	}
}

// TestWfToolCallerAdapter_ToolExecForbidDiscriminates mirrors
// TestWfToolDispatcher_ToolExecForbidDiscriminates (wf_tool_gate_test.go)
// for the tool_call surface: same forbid rule, same paired permit leg,
// proving wfToolCallerAdapter goes through the identical Cedar-backed
// gate rather than a bespoke one.
func TestWfToolCallerAdapter_ToolExecForbidDiscriminates(t *testing.T) {
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	if err := e.SetPolicyText("tool-exec-forbid.cedar", []byte(`
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"kenaz__bash"
);
`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	t.Run("deny leg: forbidden tool", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		adapter := &wfToolCallerAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		_, err := adapter.Call(context.Background(), "kenaz__bash", map[string]any{"cmd": "echo hi"})
		if err == nil {
			t.Fatal("expected a denial for kenaz__bash (tool_exec forbid), got nil")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Errorf("err = %v, want it to name a denial", err)
		}
		if n := len(pool.snapshot()); n != 0 {
			t.Errorf("pool was called %d times, want 0 — a tool_exec-denied call must never reach the pool", n)
		}
	})

	t.Run("permit leg: same forbid, different tool", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		adapter := &wfToolCallerAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		res, err := adapter.Call(context.Background(), "kenaz__websearch", map[string]any{"query": "x"})
		if err != nil {
			t.Fatalf("kenaz__websearch: unexpected denial: %v", err)
		}
		if res.IsError {
			t.Errorf("ToolResult.IsError = true on the permit leg")
		}
		if n := len(pool.snapshot()); n != 1 {
			t.Fatalf("pool was called %d times, want 1 — an unrelated tool must still dispatch", n)
		}
	})
}

const wfToolCallProbeYAML = `
id: zz-wftoolcall-probe
name: "wf tool_call probe"
version: 1
steps:
  - name: call
    kind: tool_call
    tool_name: "zzz_nonexistent_server__zzz_nonexistent_tool"
`

// TestWfToolCallerAdapter_ProductionPath_ReachesPool proves wfDeps.Tools
// is non-nil in the real constructed rpc.API, not only in a hand-built
// fixture. The workflow targets a deliberately nonexistent server/tool
// so it MUST fail — the assertion is on the SHAPE of the failure, not on
// success: "no ToolCaller wired" (the pre-UNIT-6 error) means the
// dependency was never reached; any other failure (unknown server /
// unknown tool) means the pool WAS reached and rejected the call on its
// own terms, which is what UNIT-6 wires.
func TestWfToolCallerAdapter_ProductionPath_ReachesPool(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	saved, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wfToolCallProbeYAML})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := api.Workflows().RunWithOptions(ctx, workflowsview.RunRequest{ID: saved.ID})
	if err != nil {
		t.Fatalf("RunWithOptions: %v", err)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(res.Steps))
	}
	stepErr := res.Steps[0].Err
	if stepErr == "" {
		// A nonexistent server/tool succeeding would itself be a bug in
		// the fixture, not proof of anything about wiring.
		t.Fatal("expected the nonexistent server/tool to fail the step, it succeeded")
	}
	if strings.Contains(stepErr, "no ToolCaller wired") {
		t.Fatalf("step failed with %q — wfDeps.Tools was not reached (production wiring regressed)", stepErr)
	}
}
