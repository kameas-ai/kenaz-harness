package agentgraph_test

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestPolicyGateAdapter_CheckTool_ComposesBothActions is UNIT-15's
// wiring proof for the ACTUAL production seam (core/rpc/api.go:6670
// constructs PolicyGateAdapter with the process Cedar gate and assigns
// it to deps.Policy — this is what a real agentgraph run consults, not
// a hand-rolled test fake). CheckTool must consult both
// Action::"use_tool" (the coarse family every shipped tool policy
// names) and tool_exec (the finer-grained sibling); a forbid on either
// action must deny the call.
func TestPolicyGateAdapter_CheckTool_ComposesBothActions(t *testing.T) {
	t.Parallel()

	newEngineWithForbid := func(t *testing.T, forbid string) *cedar.Engine {
		t.Helper()
		e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		if forbid == "" {
			return e
		}
		if err := e.SetPolicyText("extra.cedar", []byte(forbid)); err != nil {
			t.Fatalf("SetPolicyText: %v", err)
		}
		return e
	}

	t.Run("use_tool forbid denies", func(t *testing.T) {
		t.Parallel()
		e := newEngineWithForbid(t, `
forbid (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"x__y"
);
`)
		adapter := graphview.NewPolicyGateAdapter(e)
		err := adapter.CheckTool(context.Background(), "x__y")
		if err == nil {
			t.Fatal("expected deny for x__y (use_tool forbid), got nil")
		}
	})

	t.Run("tool_exec forbid denies", func(t *testing.T) {
		t.Parallel()
		e := newEngineWithForbid(t, `
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"x__y"
);
`)
		adapter := graphview.NewPolicyGateAdapter(e)
		err := adapter.CheckTool(context.Background(), "x__y")
		if err == nil {
			t.Fatal("expected deny for x__y (tool_exec forbid), got nil")
		}
	})

	t.Run("no matching policy allows", func(t *testing.T) {
		t.Parallel()
		e := newEngineWithForbid(t, "")
		adapter := graphview.NewPolicyGateAdapter(e)
		// A first-party kenaz tool with the default policy bundle
		// loaded: both use_tool and tool_exec permit it.
		if err := adapter.CheckTool(context.Background(), "kenaz__bash"); err != nil {
			t.Fatalf("expected allow for kenaz__bash, got %v", err)
		}
	})

	t.Run("nil gate allows (AllowAll fallback)", func(t *testing.T) {
		t.Parallel()
		adapter := graphview.NewPolicyGateAdapter(nil)
		if err := adapter.CheckTool(context.Background(), "anything__at_all"); err != nil {
			t.Fatalf("expected allow with nil gate, got %v", err)
		}
	})
}
