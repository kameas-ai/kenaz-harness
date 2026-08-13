package chat

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Confirm-each ENFORCEMENT (confirm-each-enforcement-01PMAG05 WP06).
//
// This file was confirm_each_gap_test.go. The wiring-integrity-01PMAG04
// inventory left three CHARACTERIZATION tests in it — tests that pinned
// a known gap rather than desired behaviour, on the theory that the
// assertions would invert once the feature landed and "that is the
// intended signal". A tool configured "confirm each use" dispatched
// without confirming, and the autonomy knob that was supposed to govern
// that had no behaviour to govern.
//
// They have inverted, and the file is renamed to say what it now tests.
// What each test USED to assert is recorded above it, because the
// inversion is the evidence that the fix is real:
//
//   - core/toolloop/perms.go no longer says confirm_each is "treated as
//     auto_allow at dispatch time" — a confirm_each verdict parks the
//     call on a toolloop.ConfirmBus until the user answers.
//   - kernelToolAdapter's two permission branches are no longer
//     identical: skipPrompt finally has a prompt to skip.
//   - autonomy.AutoApproveFamilies therefore has a real runtime consumer.
//
// A characterization test that never gets revisited is worse than no
// test: it looks like coverage and asserts the bug. Renaming the file is
// part of closing that loop — nothing here now describes a gap.

// confirmEachAdapterCall drives one adapter call under the given knobs
// and resolver verdict, with a live ConfirmBus that answers any prompt
// immediately with `approve`. Returns whether the call errored, how many
// times the resolver was consulted, and whether the user was prompted at
// all.
func confirmEachAdapterCall(t *testing.T, knobs autonomy.ResolvedKnobs, policy string, approve bool) (isErr bool, resolverCalls int, prompted bool) {
	t.Helper()
	// "echo" classifies into the shell-safe family, which is the
	// family the tier preconditions below assert on. The prompt-skip set
	// is per-family (WP04), so a nondescript name like "t" would be
	// unclassifiable and would prompt under every tier — testing the
	// classifier's fail-closed default instead of the knob.
	pool := &staticToolPool{server: "srv", tool: "echo"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "srv", Tool: "echo", Policy: policy, Reason: "test"},
	}

	// The publisher answers synchronously: Pending's decision channel is
	// buffered, so a re-entrant Resolve resolves the very call that
	// published. That keeps this helper straight-line.
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{
			Approved: approve,
			Reason:   "scripted by test",
		})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess").withConfirm(bus)
	if knobs.AutoApproveFamilies != nil {
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	}
	res, err := adapter.Call(context.Background(), makeCall("srv", "echo"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return res.IsError, len(perms.calls), prompted
}

// WAS: TestConfirmEach_DoesNotActuallyConfirm — "a confirm_each verdict
// dispatches the tool anyway: no confirmation, no block, no error".
//
// NOW: a confirm_each verdict prompts, and the answer decides. Approve
// dispatches; deny returns a tool error the model can read (FR-001).
func TestConfirmEach_ActuallyConfirms(t *testing.T) {
	t.Parallel()

	isErr, calls, prompted := confirmEachAdapterCall(t, autonomy.ResolvedKnobs{}, "confirm_each", true)
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
	if !prompted {
		t.Fatal("confirm_each did not prompt — the verdict is being treated as auto_allow again")
	}
	if isErr {
		t.Fatal("approved confirmation still returned a tool error")
	}

	isErr, _, prompted = confirmEachAdapterCall(t, autonomy.ResolvedKnobs{}, "confirm_each", false)
	if !prompted {
		t.Fatal("confirm_each did not prompt on the deny path")
	}
	if !isErr {
		t.Fatal("denied confirmation dispatched anyway")
	}
}

// WAS: TestAutoApproveFamilies_ProducesNoBehaviouralDifference — the knob
// changed nothing because there was no prompt to skip.
//
// NOW: it produces a measurable behavioural difference (FR-004). Strict
// is prompted; autonomous is not. The spec's acceptance criteria name
// this inversion explicitly.
func TestAutoApproveFamilies_ProducesBehaviouralDifference(t *testing.T) {
	t.Parallel()

	// Precondition: the two tiers really do differ in the knob value, so
	// a difference here is attributable to the knob and not to noise.
	if knobsFromTier(autonomy.TierStrict).AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatal("precondition: strict tier must not include shell-safe")
	}
	if !knobsFromTier(autonomy.TierAutonomous).AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatal("precondition: autonomous tier must include shell-safe")
	}

	// Deny any prompt that fires, so "was the user asked?" also shows up
	// as a difference in the dispatch outcome.
	strictErr, strictCalls, strictPrompted := confirmEachAdapterCall(
		t, knobsFromTier(autonomy.TierStrict), "confirm_each", false)
	autoErr, autoCalls, autoPrompted := confirmEachAdapterCall(
		t, knobsFromTier(autonomy.TierAutonomous), "confirm_each", false)

	if !strictPrompted {
		t.Fatal("strict tier did not prompt on a confirm_each verdict")
	}
	if autoPrompted {
		t.Fatal("autonomous tier prompted — AutoApproveFamilies did not skip the prompt")
	}
	if !strictErr {
		t.Fatal("strict tier dispatched a denied call")
	}
	if autoErr {
		t.Fatal("autonomous tier blocked a call its posture auto-approves")
	}
	// Both tiers still consult the resolver — the skip is of the prompt,
	// not of the policy check, so an explicit deny stays reachable.
	if strictCalls != 1 || autoCalls != 1 {
		t.Fatalf("resolver calls: strict=%d autonomous=%d, want 1 each", strictCalls, autoCalls)
	}
}

// Explicit Cedar deny IS honoured, under every posture — and it
// short-circuits before the prompt, so the user is never shown an
// approvable row for a call policy already forbids (FR-005).
func TestConfirmEach_ExplicitDenyIsStillTheFloor(t *testing.T) {
	t.Parallel()
	for _, tier := range []autonomy.Tier{autonomy.TierStrict, autonomy.TierAutonomous} {
		isErr, _, prompted := confirmEachAdapterCall(t, knobsFromTier(tier), "deny", true)
		if !isErr {
			t.Fatalf("tier %v: deny was not honoured — explicit Cedar deny must survive every autonomy posture", tier)
		}
		if prompted {
			t.Fatalf("tier %v: deny prompted the user — deny must short-circuit before any prompt", tier)
		}
	}
}

// ── I11: the toggle must be observably load-bearing ────────────────────

// toggleProbe runs one confirm_each call under a fixed scripted answer
// (DENY) with Settings.ConfirmEachEnabled() pinned to `enabled`, and
// reports what the run observed.
//
// The answer is deny on purpose. Under an enabled toggle the user is
// asked and refuses, so the call must NOT dispatch; under a disabled
// toggle no one is asked and the call must dispatch. Any change that
// makes the toggle inert collapses those two into the same result — and
// TestConfirmEachEnabled_FlipsDispatchBehaviour below fails on the
// collapse itself, not on a hard-coded expectation of either outcome.
func toggleProbe(t *testing.T, enabled bool) (prompted bool, dispatched bool, isErr bool) {
	t.Helper()

	pool := &countingToolPool{server: "filesystem", tool: "write_file"}
	perms := &syncPermResolver{verdict: PermVerdict{
		Policy: string(toolloop.PolicyConfirmEach),
		Reason: "confirm each use",
	}}

	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{
			Approved: false,
			Reason:   "refused by the user",
		})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-toggle").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Enabled: func() bool { return enabled }})

	res, err := adapter.Call(context.Background(), coreag.ToolCall{
		Name: "filesystem__write_file",
		Args: map[string]any{"path": "/tmp/x"},
	})
	if err != nil {
		t.Fatalf("Call(enabled=%v): %v", enabled, err)
	}
	return prompted, len(pool.dispatched()) == 1, res.IsError
}

// The 01PMGX01 I11 mutation test: flipping Settings.ConfirmEachEnabled
// flips OBSERVABLE dispatch behaviour, and this test fails if it does
// not.
//
// The assertion is written as a comparison between the two runs rather
// than as two independent expectations, because that is the only shape
// that can catch the failure mode this mission exists to fix. Two
// separate tests, each asserting its own outcome, can both keep passing
// while the branch between them is deleted — that is exactly what
// happened to kernel_tool_adapter_autonomy_test.go's strict and
// autonomous cases, which stayed green with their tiers swapped (spec
// §1.3). Here, an inert toggle makes both runs identical and the
// difference assertions fail immediately.
func TestConfirmEachEnabled_FlipsDispatchBehaviour(t *testing.T) {
	t.Parallel()

	onPrompted, onDispatched, onErr := toggleProbe(t, true)
	offPrompted, offDispatched, offErr := toggleProbe(t, false)

	// The mutation assertion: the two configurations must not agree.
	if onPrompted == offPrompted && onDispatched == offDispatched && onErr == offErr {
		t.Fatalf(
			"Settings.ConfirmEachEnabled is INERT: enabled and disabled produced identical "+
				"behaviour (prompted=%v dispatched=%v isError=%v). The toggle is a control "+
				"the user can flip that governs nothing — the defect this mission exists to fix.",
			onPrompted, onDispatched, onErr)
	}

	// Having established that it matters, pin WHICH way round, so the
	// difference cannot be satisfied by an inverted implementation.
	if !onPrompted {
		t.Error("enabled: the user was not prompted")
	}
	if onDispatched {
		t.Error("enabled: the call dispatched despite the user refusing it")
	}
	if !onErr {
		t.Error("enabled: a refused call did not return a tool error the model can read")
	}
	if offPrompted {
		t.Error("disabled: the user was prompted anyway — the toggle did not suppress the prompt")
	}
	if !offDispatched {
		t.Error("disabled: the call did not dispatch — confirm_each must behave as auto-allow (FR-006)")
	}
	if offErr {
		t.Error("disabled: the call returned a tool error")
	}
}

// FR-006 is about whether the PROMPT is offered, not about widening
// policy. An explicit Cedar deny is still the floor with the toggle off:
// "don't ask me" must never become "let everything through".
func TestConfirmEachDisabled_DoesNotWeakenExplicitDeny(t *testing.T) {
	t.Parallel()

	pool := &countingToolPool{server: "filesystem", tool: "write_file"}
	perms := &syncPermResolver{verdict: PermVerdict{
		Policy: string(toolloop.PolicyDeny),
		Reason: "denied by policy",
	}}
	adapter := newKernelToolAdapter(pool, perms, "sess-toggle-deny").
		withConfirmDeps(ConfirmDeps{Enabled: func() bool { return false }})

	res, err := adapter.Call(context.Background(), coreag.ToolCall{Name: "filesystem__write_file"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatal("explicit deny dispatched with confirm-each disabled")
	}
	if n := len(pool.dispatched()); n != 0 {
		t.Fatalf("dispatched %d calls under an explicit deny", n)
	}
}
