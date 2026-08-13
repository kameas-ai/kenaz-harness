package chat

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
)

// CHARACTERIZATION TESTS — these pin a known gap, not desired behaviour.
//
// wiring-integrity-01PMAG04 inventory. They assert the CURRENT (wrong)
// behaviour on purpose, so the gap is visible to anyone reading the
// package instead of living only in a comment two packages away. When
// the confirm-each modal flow lands, these assertions invert and this
// file's name stops making sense — that is the intended signal.
//
// The chain:
//
//   - core/toolloop/perms.go:16-18 — "WP05 lands the confirm-each modal
//     flow; for now confirm_each is treated as auto_allow at dispatch
//     time while the resolver still surfaces the verdict for telemetry."
//     That WP05 was never landed.
//   - Because nothing ever blocks on confirm_each, kernelToolAdapter's
//     two permission branches (skipPrompt true / false) are identical in
//     effect: both Resolve, both return an error only on "deny", both
//     fall through otherwise. skipPrompt has nothing to skip.
//   - Which makes autonomy.AutoApproveFamilies inert — it was the last
//     ResolvedKnobs field believed to have a runtime consumer, so all
//     seven autonomy knobs are inert, not six.
//   - Settings.ConfirmEachEnabled() likewise has no Go consumer outside
//     its own package, while the frontend renders a toggle for it.
//
// Net user-visible effect: a tool configured "confirm each use" runs
// without confirmation, and the Settings toggle governing it does
// nothing.
//
// Why the existing kernel_tool_adapter_autonomy_test.go did not catch
// this: TestKernelToolAdapter_AutonomousSkipsPrompt scripts a
// confirm_each verdict and asserts the call SUCCEEDS, while
// TestKernelToolAdapter_StrictDoesNotSkipPrompt scripts an auto_allow
// verdict — so the strict case never exercises confirm_each at all.
// Swap the tiers between those two tests and both still pass. They are
// the "gate that cannot fail" shape #281 repaired on the CI surface.

func confirmEachAdapterCall(t *testing.T, knobs autonomy.ResolvedKnobs, policy string) (bool, int) {
	t.Helper()
	pool := &staticToolPool{server: "srv", tool: "t"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "srv", Tool: "t", Policy: policy, Reason: "test"},
	}
	adapter := newKernelToolAdapter(pool, perms, "sess")
	if knobs.AutoApproveFamilies != nil {
		adapter.withAutonomy(func() autonomy.ResolvedKnobs { return knobs })
	}
	res, err := adapter.Call(context.Background(), makeCall("srv", "t"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return res.IsError, len(perms.calls)
}

// A "confirm_each" verdict dispatches the tool anyway — no confirmation,
// no block, no error. This is the gap.
func TestConfirmEach_DoesNotActuallyConfirm(t *testing.T) {
	t.Parallel()
	isErr, calls := confirmEachAdapterCall(t, autonomy.ResolvedKnobs{}, "confirm_each")
	if calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", calls)
	}
	if isErr {
		t.Fatal("confirm_each blocked the call — if the modal flow has landed, invert this characterization test")
	}
}

// The knob produces no behavioural difference under a confirm_each
// verdict, because there is no prompt to skip. This is the assertion the
// existing autonomy tests should have made.
func TestAutoApproveFamilies_ProducesNoBehaviouralDifference(t *testing.T) {
	t.Parallel()

	strictErr, strictCalls := confirmEachAdapterCall(t, knobsFromTier(autonomy.TierStrict), "confirm_each")
	autoErr, autoCalls := confirmEachAdapterCall(t, knobsFromTier(autonomy.TierAutonomous), "confirm_each")

	// Precondition: the two tiers really do differ in the knob value, so
	// a null result here means the *consumer* is inert, not the resolver.
	if knobsFromTier(autonomy.TierStrict).AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatal("precondition: strict tier must not include shell-safe")
	}
	if !knobsFromTier(autonomy.TierAutonomous).AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatal("precondition: autonomous tier must include shell-safe")
	}

	if strictErr != autoErr || strictCalls != autoCalls {
		t.Fatalf("AutoApproveFamilies changed behaviour (strict: err=%v calls=%d; autonomous: err=%v calls=%d) — if that is now intended, replace this characterization test with a real assertion",
			strictErr, strictCalls, autoErr, autoCalls)
	}
}

// Explicit Cedar deny IS honoured, under every posture. This is the floor
// that still works and must keep working — it is the only thing standing
// between a tool call and dispatch today.
func TestConfirmEach_ExplicitDenyIsStillTheFloor(t *testing.T) {
	t.Parallel()
	for _, tier := range []autonomy.Tier{autonomy.TierStrict, autonomy.TierAutonomous} {
		isErr, _ := confirmEachAdapterCall(t, knobsFromTier(tier), "deny")
		if !isErr {
			t.Fatalf("tier %v: deny was not honoured — explicit Cedar deny must survive every autonomy posture", tier)
		}
	}
}
