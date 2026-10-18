package cedar_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestThreeLayerResolve_Layer1ForbidWins pins layer 1: an explicit
// forbid is a hard Deny, and the rating layer is never consulted (there
// is nothing to consult in WP02, but the shape must hold once WP05 wires
// a real rater in — a forbid must short-circuit before any rater call).
func TestThreeLayerResolve_Layer1ForbidWins(t *testing.T) {
	t.Parallel()
	g := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Deny, Reason: "forbid: rm_rf"}}
	d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "bash"), nil, 40)
	if d.Outcome != cedar.Deny {
		t.Fatalf("Outcome = %v, want Deny", d.Outcome)
	}
	if len(g.calls) != 1 {
		t.Fatalf("gate called %d times, want 1", len(g.calls))
	}
}

// TestThreeLayerResolve_Layer2PermitWins pins layer 2: an explicit
// permit is a hard Allow.
func TestThreeLayerResolve_Layer2PermitWins(t *testing.T) {
	t.Parallel()
	g := &fakeGate{fixed: cedar.Decision{Outcome: cedar.Allow, Reason: "permit: read_file"}}
	d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "read_file"), nil, 40)
	if d.Outcome != cedar.Allow {
		t.Fatalf("Outcome = %v, want Allow", d.Outcome)
	}
}

// TestThreeLayerResolve_Layer3UnmatchedAsks is the safety-property test
// for WP02: an action Cedar has no opinion about (NotApplicable) must
// resolve to Confirm, never Allow. This is the exact hole
// (enforce()'s historical NotApplicable -> nil) the mission exists to
// close one layer up. See TestThreeLayerResolve_MutationProof below for
// the falsifiability check this test's presence enables.
func TestThreeLayerResolve_Layer3UnmatchedAsks(t *testing.T) {
	t.Parallel()
	g := &fakeGate{fixed: cedar.Decision{Outcome: cedar.NotApplicable}}
	d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "rm_rf"), nil, 40)
	if d.Outcome != cedar.Confirm {
		t.Fatalf("Outcome = %v, want Confirm — an unmatched action must never silently resolve to Allow", d.Outcome)
	}
}

// TestThreeLayerResolve_NilGateAsks pins that a nil Gate — matching
// every other Gate-typed helper's "nil means AllowAll" convention —
// still resolves an unmatched action to Confirm, not Allow. AllowAll
// always answers NotApplicable, so this exercises the exact same layer-3
// stub path as the test above via a different entry (no gate wired at
// all, e.g. a pre-boot chassis state) — the safe direction must hold
// even when nothing is configured.
func TestThreeLayerResolve_NilGateAsks(t *testing.T) {
	t.Parallel()
	d := cedar.ThreeLayerResolve(context.Background(), nil, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "anything"), nil, 40)
	if d.Outcome != cedar.Confirm {
		t.Fatalf("Outcome = %v, want Confirm (nil gate -> AllowAll -> NotApplicable -> layer 3)", d.Outcome)
	}
}

// TestThreeLayerResolve_ThresholdZeroBypassesRater pins the FR-008/FR-003
// property that WP03's threshold parameter already governs even before a
// real rater exists (WP05): threshold<=0 (strict tier) must be
// distinguishable, in the returned Decision, from "no rater exists yet"
// so WP05's implementation has a real condition to branch on rather than
// inventing the bypass rule from scratch. This is a real behavioural
// pin, not a copy-into-a-struct: the Reason differs, and — per this
// function's own doc comment — WP05 is required to check this exact
// condition before spending a model call.
func TestThreeLayerResolve_ThresholdZeroBypassesRater(t *testing.T) {
	t.Parallel()
	g := &fakeGate{fixed: cedar.Decision{Outcome: cedar.NotApplicable}}
	d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "rm_rf"), nil, 0)
	if d.Outcome != cedar.Confirm {
		t.Fatalf("Outcome = %v, want Confirm", d.Outcome)
	}
	if !strings.Contains(d.Reason, "bypassed") {
		t.Fatalf("Reason = %q, want it to name the rater bypass (threshold=0)", d.Reason)
	}
}

// TestThreeLayerResolve_PositiveThresholdDoesNotClaimBypass is the
// negative control for the test above: a positive threshold must NOT
// produce the same "bypassed" reason, since the (future) rater is not
// being skipped in that case.
func TestThreeLayerResolve_PositiveThresholdDoesNotClaimBypass(t *testing.T) {
	t.Parallel()
	g := &fakeGate{fixed: cedar.Decision{Outcome: cedar.NotApplicable}}
	d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "rm_rf"), nil, 40)
	if d.Outcome != cedar.Confirm {
		t.Fatalf("Outcome = %v, want Confirm", d.Outcome)
	}
	if strings.Contains(d.Reason, "bypassed") {
		t.Fatalf("Reason = %q, must not claim a bypass when threshold > 0", d.Reason)
	}
}

// TestThreeLayerResolve_NeverReturnsNotApplicable pins the contract
// documented on ThreeLayerResolve: callers that pattern-match on
// Allow/Deny/Confirm without a NotApplicable case must never be handed
// one. Every Cedar outcome the underlying gate can produce is exercised.
func TestThreeLayerResolve_NeverReturnsNotApplicable(t *testing.T) {
	t.Parallel()
	for _, in := range []cedar.Outcome{cedar.Allow, cedar.Deny, cedar.NotApplicable} {
		g := &fakeGate{fixed: cedar.Decision{Outcome: in}}
		d := cedar.ThreeLayerResolve(context.Background(), g, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("", "x"), nil, 40)
		if d.Outcome == cedar.NotApplicable {
			t.Fatalf("input %v: ThreeLayerResolve returned NotApplicable, which is not in its documented output range {Allow, Deny, Confirm}", in)
		}
	}
}
