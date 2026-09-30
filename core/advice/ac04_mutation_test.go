package advice

import (
	"errors"
	"testing"
)

// TestAC04_BranchNowMutatedToSuggestOnly_AutoActGateRefuses is
// laya-advisors-01LAYA001 WP07's AC-04 mutation proof: "a test that
// flips branch_now's safety class to suggest-only and asserts the
// auto-act path refuses (or gate fires) — content-anchored."
//
// This package (core/advice) cannot import core/advice/kinds/branchnow
// (that package imports THIS one — a cycle), so the real production
// registration in branchnow.go's init() never runs inside this test
// binary. This test is content-anchored the SAME way every kind
// package's own init() comment documents for
// scripts/ci/check-advice-kinds.sh's static grep: it registers a kind
// under the EXACT LITERAL "branch_now" (not a symbolic
// advicebranchnow.KindID import, which would require the cycle) —
// the identical id string spec.md §2's table and branchnow.go's
// production init() both use — proving that the ONE gate function
// (RequireCanAutoAct) branch_now's real auto-act call site
// (core/rpc/views/agentgraph/chat/advice_hook.go's evaluateBranchNow)
// invokes behaves correctly for that id under both safety classes.
//
// Complementary end-to-end proof (that the CALL SITE actually honors
// this gate, not just that the gate function itself is correct) lives
// in core/rpc/views/agentgraph/chat/advice_hook_test.go, which drives
// the REAL registered branch_now kind (that package DOES import
// branchnow) through fireAdvice end to end.
func TestAC04_BranchNowMutatedToSuggestOnly_AutoActGateRefuses(t *testing.T) {
	const branchNowID = "branch_now" // literal — mirrors branchnow.go's init()

	reversible := AdviceKind{
		ID:            branchNowID,
		PromptVersion: "v1",
		SafetyClass:   SafetyReversible,
		Extract:       func(input any) (Features, error) { return input, nil },
		RenderPrompt:  func(Features) (string, string) { return "sys", "user" },
	}
	if err := Register(reversible); err != nil {
		t.Fatalf("Register(branch_now, reversible): %v", err)
	}

	// Baseline: today's real safety class (reversible) passes the gate —
	// this is what makes evaluateBranchNow's auto-act branch reachable at
	// the Autonomous tier.
	if err := RequireCanAutoAct(branchNowID); err != nil {
		t.Fatalf("RequireCanAutoAct(%q) with SafetyReversible = %v, want nil", branchNowID, err)
	}

	// The mutation: flip branch_now's registration to suggest-only —
	// exactly the class compact_now/escalate_model already ship as.
	unregisterForTest(branchNowID)
	suggestOnly := reversible
	suggestOnly.SafetyClass = SafetySuggestOnly
	if err := Register(suggestOnly); err != nil {
		t.Fatalf("Register(branch_now, suggest_only): %v", err)
	}
	t.Cleanup(func() { unregisterForTest(branchNowID) })

	// The assertion the mission brief asks for: with the mutation in
	// place, the SAME gate call evaluateBranchNow makes before ever
	// invoking AutoActBranchNow now refuses.
	err := RequireCanAutoAct(branchNowID)
	if err == nil {
		t.Fatal("RequireCanAutoAct(branch_now) after flipping to SafetySuggestOnly = nil, want ErrSuggestOnlyCannotAutoAct — the auto-act gate did not fire on the mutation")
	}
	if !errors.Is(err, ErrSuggestOnlyCannotAutoAct) {
		t.Errorf("RequireCanAutoAct(branch_now) after mutation = %v, want wrapping ErrSuggestOnlyCannotAutoAct", err)
	}
}
