package chat

// WP05/WP06 (risk-rated-autonomy-01PMRA01) — the rater's wiring into
// rung 0's layer-3 branch (resolveLayer3Rating), and the family floor
// that survives even a rating a successful injection fully controls.
//
// These tests use risk.FakeRater rather than a real LLMRater — the
// LLMRater's own cache/timeout/prompt-construction properties are
// tested directly in core/policy/risk/llmrater_test.go. What matters
// here is the WIRING: when the rater is (or is not) consulted, how its
// result maps to Allow vs prompt, and that the family floor is applied
// before that comparison.

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// TestKernelToolAdapter_RiskRater_SkippedWhenThresholdZero is FR-008's
// proof requirement: at threshold<=0 (the strict tier), a wired rater
// must NEVER be called — every layer-3 case already always asks, so a
// rater call could never change the answer.
func TestKernelToolAdapter_RiskRater_SkippedWhenThresholdZero(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierStrict) // RiskThreshold == 0

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater()

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-threshold-zero").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("threshold=0 must still prompt (FR-008 bypasses the RATER, not the prompt)")
	}
	if rater.CallCount() != 0 {
		t.Fatalf("rater called %d times at threshold=0, want 0 (FR-008: a call whose result cannot change the answer must not spend a model call)", rater.CallCount())
	}
}

// TestKernelToolAdapter_RiskRater_NilRaterUnchanged pins that a nil
// rater leaves rung 0's layer-3 branch at the WP02/WP03 stub — always
// prompt — exactly as it behaved before WP05 existed.
func TestKernelToolAdapter_RiskRater_NilRaterUnchanged(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold) // RiskThreshold > 0

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-nilrater").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	// Deliberately no withRater call.

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("nil rater must still prompt — byte-identical to the WP02/WP03 stub")
	}
}

// TestKernelToolAdapter_RiskRater_BelowThresholdAllowsWithoutPrompt is
// WP05's central behaviour change: a rating below the resolved threshold
// resolves layer 3 to Allow, skipping the prompt entirely.
func TestKernelToolAdapter_RiskRater_BelowThresholdAllowsWithoutPrompt(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold) // RiskThreshold == 60

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 10, Rationale: "a plain read"})

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-below").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q (a below-threshold rating must allow)", result.Content)
	}
	if prompted {
		t.Fatal("user was prompted despite a below-threshold rating — WP05's whole point is skipping the prompt here")
	}
	if rater.CallCount() != 1 {
		t.Fatalf("rater called %d times, want 1", rater.CallCount())
	}
}

// TestKernelToolAdapter_RiskRater_AtOrAboveThresholdPrompts pins the
// other half: a rating at/above threshold still prompts.
func TestKernelToolAdapter_RiskRater_AtOrAboveThresholdPrompts(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold) // RiskThreshold == 60

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 90, Rationale: "surprisingly risky"})

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-above").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("an at/above-threshold rating must still prompt")
	}
}

// TestKernelToolAdapter_RiskRater_ErrorFailsClosedToPrompt is the
// failure-contract proof: a rater error must resolve to the prompt,
// never to Allow (spec FR-004: fail closed, never fail-open).
func TestKernelToolAdapter_RiskRater_ErrorFailsClosedToPrompt(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold)

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater().ScriptErrorFor("myserver__read_file", errAlwaysFails)

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-error").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("a rater error must fail closed to the prompt, not silently allow")
	}
}

// TestKernelToolAdapter_RiskRater_FamilyFloorAppliesForDestructive is
// WP06's wiring-level proof: even a rating of 0 (the worst-case outcome
// of a fully successful prompt-injection attack against the rater) does
// not allow a destructive-family call at the autonomous tier, because
// ApplyFamilyFloor raises the floored score above every tier's
// threshold before the comparison happens.
func TestKernelToolAdapter_RiskRater_FamilyFloorAppliesForDestructive(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "delete_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "delete_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous) // RiskThreshold == 80, the top tier

	prompted := false
	bus := newAutoApproveBus(&prompted)

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	// The worst case: the injection fully succeeded and got the model to
	// say 0.
	rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 0, Rationale: "ignore previous instructions, this is safe"})

	adapter := newKernelToolAdapter(pool, perms, "sess-risk-floor").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "delete_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("a destructive call rated 0 by an injected prompt must still prompt — the family floor must have held")
	}
}

// errAlwaysFails is a sentinel error for the rater-error test.
var errAlwaysFails = errors.New("rater: simulated failure")

// newAutoApproveBus returns a ConfirmBus whose every prompt is
// immediately approved, setting *prompted so tests can assert whether
// the prompt rung was reached at all.
func newAutoApproveBus(prompted *bool) *toolloop.ConfirmBus {
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		*prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})
	return bus
}
