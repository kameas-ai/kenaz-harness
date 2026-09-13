package chat

// WP02 (risk-rated-autonomy-01PMRA01) — the safety-critical wiring test.
//
// TestKernelToolAdapter_AutonomousSkipsPrompt (kernel_tool_adapter_autonomy_test.go)
// documents today's hole directly in its own assertion: at the
// autonomous tier, a confirm_each verdict is auto-approved and the user
// is NEVER asked, regardless of what (if anything) a Cedar policy says
// about the specific call. The tests below wire a.gate and prove the new
// rung 0 closes that hole for the "no policy has an opinion" case, while
// leaving explicit Cedar forbid/permit exactly as authoritative as
// before.

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// fixedGate is a minimal cedar.Gate returning a fixed Decision for
// every Evaluate call, recording how many times it was consulted.
type fixedGate struct {
	decision cedar.Decision
	calls    int
}

func (g *fixedGate) Evaluate(
	_ context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	_ map[cedarlib.String]cedarlib.Value,
) cedar.Decision {
	g.calls++
	out := g.decision
	out.Action = action
	out.Principal = principal.String()
	out.Resource = resource.String()
	return out
}

// TestKernelToolAdapter_RiskGate_UnmatchedForcesPromptAtAutonomous is the
// mutation-critical test: it is byte-for-byte
// TestKernelToolAdapter_AutonomousSkipsPrompt's setup (same tier, same
// confirm_each verdict, same "the user must not be asked" assertion in
// the ORIGINAL test) except a.gate is wired to a Cedar engine that has
// NO opinion about this specific call (NotApplicable — "an empty policy
// set", spec acceptance criterion 1). Where the original test asserts
// prompted == false, this one asserts prompted == true: an unmatched
// action must ask even at the tier whose whole posture is "trust Cedar
// to decide, don't ask me".
func TestKernelToolAdapter_RiskGate_UnmatchedForcesPromptAtAutonomous(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "rm_rf"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{
			Server: "myserver",
			Tool:   "rm_rf",
			Policy: "confirm_each",
		},
	}

	knobs := knobsFromTier(autonomy.TierAutonomous)
	if promptSkipSet(knobs).SkipsTool("myserver", "rm_rf") == false {
		t.Fatalf("test precondition: autonomous tier's promptSkipSet should skip this destructive-shaped tool today (that is the exact hole WP02 closes); got skip-set %v / destructive posture %v",
			knobs.AutoApproveFamilies.Sorted(), knobs.DestructiveActionPosture)
	}

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-unmatched").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "rm_rf"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q (the fixture approves the prompt, so the call should proceed)", result.Content)
	}
	if !prompted {
		t.Fatal("autonomous posture did NOT prompt the user for an action Cedar had no opinion about — this is exactly the default-allow hole risk-rated-autonomy-01PMRA01 WP02 exists to close")
	}
	if gate.calls != 1 {
		t.Fatalf("gate called %d times, want 1", gate.calls)
	}
	// The autonomy-posture resolver must never even be consulted: rung 0
	// resolves (and prompts) before rung 1 is reached.
	if len(perms.calls) != 1 {
		t.Fatalf("perms resolver called %d times, want 1 (still consulted for the initial confirm_each verdict, at Call()'s dispatch step)", len(perms.calls))
	}
}

// TestKernelToolAdapter_RiskGate_ExplicitForbidDeniesBeforeAutonomyRuns
// pins layer 1: a Cedar forbid denies the call outright and the
// autonomy-posture prompt-skip-set rung (rung 1) is never reached — a
// forbid is a harder boundary than any autonomy tier.
func TestKernelToolAdapter_RiskGate_ExplicitForbidDeniesBeforeAutonomyRuns(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "rm_rf"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "rm_rf", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous)

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.Deny, Reason: "forbid: rm_rf"}}

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-forbid").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "rm_rf"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.IsError {
		t.Fatal("result.IsError = false, want true — an explicit Cedar forbid must deny the call")
	}
	if prompted {
		t.Fatal("user was prompted for a Cedar-forbidden call — a forbid must never reach the prompt")
	}
}

// TestKernelToolAdapter_RiskGate_ExplicitPermitSkipsPromptEntirely pins
// layer 2: an explicit Cedar permit allows the call outright, without
// ever reaching the autonomy-posture rung or the prompt.
func TestKernelToolAdapter_RiskGate_ExplicitPermitSkipsPromptEntirely(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	// Strict tier: if rung 0's permit result were ignored, the strict
	// tier's empty prompt-skip-set would force the ladder down to rung
	// 6 and prompt — so a passing "no prompt" result here is only
	// possible because layer 2 skipped the rest of the ladder entirely.
	knobs := knobsFromTier(autonomy.TierStrict)

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: false})
	})

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.Allow, Reason: "permit: read_file"}}

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-permit").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q (an explicit Cedar permit must allow the call)", result.Content)
	}
	if prompted {
		t.Fatal("user was prompted despite an explicit Cedar permit — layer 2 must skip the rest of the ladder")
	}
}

// TestKernelToolAdapter_RiskGate_NilGateUnchanged pins backward
// compatibility: an adapter that never calls withGate behaves exactly
// as it did before WP02 — this is what every pre-existing test in this
// package (which does not call withGate) already exercises implicitly;
// this test names the property directly so a future refactor cannot
// silently start requiring a gate.
func TestKernelToolAdapter_RiskGate_NilGateUnchanged(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "echo"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "echo", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous)

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: false})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-nilgate").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	// Deliberately no withGate call.

	result, err := adapter.Call(context.Background(), makeCall("myserver", "echo"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if prompted {
		t.Fatal("nil gate must not change pre-WP02 behaviour: autonomous tier should still skip the prompt")
	}
}
