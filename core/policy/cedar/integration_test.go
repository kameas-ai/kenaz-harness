package cedar_test

// Integration test for one gate-hook end-to-end flow: a fake tool
// dispatcher consults the Cedar engine's CheckTool helper, refuses the
// dispatch on Deny, and surfaces a PolicyDeniedError to the caller.
//
// Black-box integration test (charter DIRECTIVE_036): exercises the
// engine through its public API only — no internal types referenced.

import (
	"context"
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
)

// fakeToolDispatcher emulates the structure of core/rpc/views/tools or
// core/toolloop's dispatch site. Real call sites wrap their existing
// dispatch with the same CheckTool / Evaluate sequence.
type fakeToolDispatcher struct {
	gate cedar.Gate
	// invoked records each tool whose backend Run was actually
	// invoked — denies must NOT show up here.
	invoked []string
}

func (d *fakeToolDispatcher) Dispatch(ctx context.Context, server, tool string) error {
	if err := cedar.CheckTool(ctx, d.gate, server, tool); err != nil {
		return err
	}
	d.invoked = append(d.invoked, server+"__"+tool)
	return nil
}

func TestIntegration_ToolDispatchGate_Deny(t *testing.T) {
	t.Parallel()

	const userPolicy = `
permit (principal, action, resource);

forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"filesystem__delete"
);
`
	e, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("user.cedar", []byte(userPolicy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	d := &fakeToolDispatcher{gate: e}

	// Allowed tool dispatches normally.
	if err := d.Dispatch(context.Background(), "websearch", "search"); err != nil {
		t.Fatalf("websearch__search should dispatch: %v", err)
	}
	if got := len(d.invoked); got != 1 {
		t.Fatalf("invoked len=%d want 1", got)
	}

	// Forbidden tool returns PolicyDeniedError and does NOT invoke.
	err = d.Dispatch(context.Background(), "filesystem", "delete")
	if err == nil {
		t.Fatal("expected PolicyDeniedError, got nil")
	}
	var pe *cedar.PolicyDeniedError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PolicyDeniedError, got %T (%v)", err, err)
	}
	if pe.Decision.Outcome != cedar.Deny {
		t.Fatalf("decision outcome = %s, want deny", pe.Decision.Outcome)
	}
	if pe.Decision.Resource != `Tool::"filesystem__delete"` {
		t.Fatalf("decision resource = %q, want Tool::\"filesystem__delete\"",
			pe.Decision.Resource)
	}
	// Dispatcher must not have invoked the forbidden tool.
	if len(d.invoked) != 1 {
		t.Fatalf("invoked len=%d after deny, want 1 (deny must short-circuit)",
			len(d.invoked))
	}

	// The audit log carries both decisions, newest first.
	recent := e.RecentDecisions(10)
	if len(recent) != 2 {
		t.Fatalf("RecentDecisions len=%d, want 2", len(recent))
	}
	if recent[0].Outcome != cedar.Deny {
		t.Fatalf("newest decision should be Deny, got %s", recent[0].Outcome)
	}
	if recent[1].Outcome != cedar.Allow {
		t.Fatalf("oldest decision should be Allow, got %s", recent[1].Outcome)
	}
}
