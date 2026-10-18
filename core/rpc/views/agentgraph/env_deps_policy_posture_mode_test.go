package agentgraph_test

// trust-surfaces-that-fire-01PMZ202 WP23 (AN-04, second seam).
//
// cedar.WithPostureMode (core/policy/cedar/posture.go) had zero
// non-test callers: plan_mode's write-denial lived entirely in
// autonomy.planModePreset (an empty AutoApproveFamilies), which only
// changes whether a tool call auto-approves or parks on a confirmation
// prompt — a user answering "allow" to that prompt, or a stale
// session-level Allow-always grant, still let the write through,
// because nothing at the Cedar gate itself knew the session was in
// plan_mode. PolicyGateAdapter.WithPostureMode closes that: it
// re-wraps the SAME underlying gate env.Policy already points at, so
// plan_mode denies write-class actions at the Cedar layer regardless
// of what the knob-level auto-approve set says.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestPolicyGateAdapter_WithPostureMode_PlanModeDeniesWrites is
// AC-23c: plan mode denies a write through the gate. Wraps
// cedar.AllowAll{} — which would otherwise permit everything
// (NotApplicable -> enforce -> nil) — so a denial can only come from
// the posture-mode wrap itself, not from an embedded policy bundle.
func TestPolicyGateAdapter_WithPostureMode_PlanModeDeniesWrites(t *testing.T) {
	t.Parallel()

	base := graphview.NewPolicyGateAdapter(cedar.AllowAll{})
	planMode := base.WithPostureMode(autonomy.PostureModePlanMode)

	if err := planMode.CheckFileWrite(context.Background(), "/tmp/wp23/x"); err == nil {
		t.Fatal("CheckFileWrite under plan_mode = nil error, want denied")
	}
	if err := planMode.CheckStateWrite(context.Background(), "file"); err == nil {
		t.Fatal("CheckStateWrite under plan_mode = nil error, want denied")
	}
}

// TestPolicyGateAdapter_WithPostureMode_PlanModeStillAllowsReads pins
// the "narrows, never broadens" contract: plan_mode denies the
// write-class actions, not everything — a read must still pass through
// to the underlying (here permissive) gate.
func TestPolicyGateAdapter_WithPostureMode_PlanModeStillAllowsReads(t *testing.T) {
	t.Parallel()

	base := graphview.NewPolicyGateAdapter(cedar.AllowAll{})
	planMode := base.WithPostureMode(autonomy.PostureModePlanMode)

	if err := planMode.CheckFileRead(context.Background(), "/tmp/wp23/x"); err != nil {
		t.Fatalf("CheckFileRead under plan_mode = %v, want nil (reads are not denied by plan_mode)", err)
	}
}

// TestPolicyGateAdapter_WithPostureMode_EmptyModeIsPassthrough is the
// regression guard: a session with no active posture mode (the
// overwhelming common case) must see byte-identical behaviour to a
// gate that was never wrapped at all — cedar.WithPostureMode's own
// "any other mode value is a transparent pass-through" contract,
// exercised through PolicyGateAdapter specifically.
func TestPolicyGateAdapter_WithPostureMode_EmptyModeIsPassthrough(t *testing.T) {
	t.Parallel()

	base := graphview.NewPolicyGateAdapter(cedar.AllowAll{})
	unwrapped := base.WithPostureMode("")

	if err := unwrapped.CheckFileWrite(context.Background(), "/tmp/wp23/x"); err != nil {
		t.Fatalf("CheckFileWrite with mode=\"\" = %v, want nil (AllowAll passthrough)", err)
	}
}

// TestPolicyGateAdapter_WithPostureMode_NilAdapterDoesNotPanic: a nil
// receiver must not panic when re-wrapped — it simply has no gate to
// wrap, so WithPostureMode hands the nil receiver back unchanged rather
// than dereferencing it.
func TestPolicyGateAdapter_WithPostureMode_NilAdapterDoesNotPanic(t *testing.T) {
	t.Parallel()
	var a *graphview.PolicyGateAdapter
	_ = a.WithPostureMode(autonomy.PostureModePlanMode)
}
