package chat

// WP07 correction (spec.md FR-004's amendment, lines 271-274, reviewer
// ruling 2026-09-14): a rater error was originally collapsed into one
// "always ask" branch regardless of cause. The amendment requires TWO
// distinct paths — a transient/malformed failure still prompts
// (FR-004 unchanged), but TRUE unreachability degrades to the SAME
// family floor WP06 already computes, using a baseline score of 0 in
// place of a live rating.
//
// Why this matters concretely: combined with rung 5 (left untouched —
// `unattended -> deny immediately`), collapsing both failure classes
// into "always ask" meant an autonomous UNATTENDED run with no network
// to the rater denied every un-granted MCP tool the instant a gate is
// wired — before this mission those calls silently succeeded. The
// owner's stated autonomous-mode goal is "run an agent for hours doing
// work and not stop it"; an offline rater turning every benign call
// into an instant denial is exactly the failure mode the amendment
// exists to prevent.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// unreachableErr is what a dial/DNS/credential failure looks like once
// wrapped per RiskRater's contract: errors.Is(err, risk.ErrUnreachable)
// must be true.
var unreachableErr = fmt.Errorf("dial tcp: lookup api.anthropic.com: no such host: %w", risk.ErrUnreachable)

// TestKernelToolAdapter_OfflineFloor_UnattendedBenignToolProceeds is the
// coordinator's proof requirement #1: unreachable rater + unattended +
// autonomous tier + a benign un-granted MCP tool -> PROCEEDS.
//
// Mutation-test property: if resolveLayer3OfflineFloor's unreachability
// classification were removed (routing errors.Is(rerr, risk.ErrUnreachable)
// through the ordinary "always ask" branch instead), this test would
// fail — rung 5's unattended fast path denies immediately once the call
// reaches a prompt, so result.IsError would flip to true.
func TestKernelToolAdapter_OfflineFloor_UnattendedBenignToolProceeds(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "filesystem", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "filesystem", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous) // RiskThreshold == 80

	prompted := false
	bus := newAutoApproveBus(&prompted)
	rec := &recordingAudit{}

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater().ScriptErrorFor("filesystem__read_file", unreachableErr)

	adapter := newKernelToolAdapter(pool, perms, "sess-offline-benign").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Audit: rec})
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(runposture.Unattended(context.Background()), makeCall("filesystem", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q — a benign un-granted tool must PROCEED when the rater is unreachable, not deny", result.Content)
	}
	if prompted {
		t.Fatal("the confirm bus was parked — an unreachable rater on a benign family must resolve via the offline floor, never reach a prompt")
	}
	if rater.CallCount() != 1 {
		t.Fatalf("rater called %d times, want 1", rater.CallCount())
	}

	decisions := rec.decisions(t)
	if len(decisions) != 1 {
		t.Fatalf("emitted %d decision records, want exactly 1: %+v", len(decisions), decisions)
	}
	d := decisions[0]
	if d.Path != audit.ToolConfirmPathLayer3OfflineFloor {
		t.Errorf("Path = %q, want %q — the offline degrade must be logged distinctly, not folded into a reused rater-failed path", d.Path, audit.ToolConfirmPathLayer3OfflineFloor)
	}
	if !d.Approved {
		t.Error("Approved = false, want true for a benign family under the offline floor")
	}
}

// TestKernelToolAdapter_OfflineFloor_UnattendedDestructiveToolStillDenies
// is the coordinator's proof requirement #1's other half: same setup,
// destructive-family tool -> still surfaces/denies. The family floor
// (81, above every tier's threshold) holds even with zero live rating.
//
// Mutation-test property: if ApplyFamilyFloor's floor-raising behaviour
// were removed (see floor.go's own mutation-test doc comment), the
// offline baseline score would stay 0 for every family including
// destructive, and this test's Approved-false / IsError-true assertions
// would flip.
func TestKernelToolAdapter_OfflineFloor_UnattendedDestructiveToolStillDenies(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "filesystem", tool: "delete_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "filesystem", Tool: "delete_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous) // RiskThreshold == 80, the top tier

	prompted := false
	bus := newAutoApproveBus(&prompted)
	rec := &recordingAudit{}

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	rater := risk.NewFakeRater().ScriptErrorFor("filesystem__delete_file", unreachableErr)

	adapter := newKernelToolAdapter(pool, perms, "sess-offline-destructive").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Audit: rec})
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	result, err := adapter.Call(runposture.Unattended(context.Background()), makeCall("filesystem", "delete_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a deny for a destructive-family tool under the offline floor, got success")
	}
	if prompted {
		t.Error("the confirm bus was parked — an unattended run must deny immediately (rung-5 semantics), never actually wait for an answer")
	}

	decisions := rec.decisions(t)
	// Two records for one call, deliberately: the offline-floor marker
	// ("we went blind") and the eventual unattended-immediate-deny
	// ("here is what happened next") are two distinct facts — see
	// audit.go's ToolConfirmPathLayer3OfflineFloor doc comment.
	if len(decisions) != 2 {
		t.Fatalf("emitted %d decision records, want exactly 2: %+v", len(decisions), decisions)
	}
	first, second := decisions[0], decisions[1]
	if first.Path != audit.ToolConfirmPathLayer3OfflineFloor {
		t.Errorf("first record Path = %q, want %q", first.Path, audit.ToolConfirmPathLayer3OfflineFloor)
	}
	if first.Approved {
		t.Error("first record Approved = true, want false — the family floor must not clear the autonomous threshold")
	}
	if second.Path != audit.ToolConfirmPathLayer3Timeout {
		t.Errorf("second record Path = %q, want %q (the unattended-immediate-deny path)", second.Path, audit.ToolConfirmPathLayer3Timeout)
	}
	if second.Approved {
		t.Error("second record Approved = true, want false")
	}
}

// TestKernelToolAdapter_OfflineFloor_TransientErrorStillPrompts pins
// FR-004's UNCHANGED half of the amendment: a transient/malformed rater
// failure (NOT wrapped as risk.ErrUnreachable) must still prompt exactly
// as it did before this fix — the offline floor engages ONLY for true
// unreachability.
func TestKernelToolAdapter_OfflineFloor_TransientErrorStillPrompts(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "filesystem", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "filesystem", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold)

	prompted := false
	bus := newAutoApproveBus(&prompted)
	rec := &recordingAudit{}

	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}
	transientErr := errors.New("risk: rater response is not valid JSON: unexpected EOF")
	rater := risk.NewFakeRater().ScriptErrorFor("filesystem__read_file", transientErr)

	adapter := newKernelToolAdapter(pool, perms, "sess-offline-transient").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Audit: rec})
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withRater(rater)

	// Attended (no runposture.Unattended) — a transient error should
	// prompt exactly as before, and the fixture answers it.
	result, err := adapter.Call(context.Background(), makeCall("filesystem", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("a transient/malformed rater failure must still prompt (FR-004 unchanged) — it must NOT take the offline-floor path")
	}

	decisions := rec.decisions(t)
	if len(decisions) != 1 {
		t.Fatalf("emitted %d decision records, want exactly 1: %+v", len(decisions), decisions)
	}
	if decisions[0].Path == audit.ToolConfirmPathLayer3OfflineFloor {
		t.Error("a transient error must NOT be logged under the offline-floor path")
	}
	if decisions[0].Path != audit.ToolConfirmPathPrompted {
		t.Errorf("Path = %q, want %q", decisions[0].Path, audit.ToolConfirmPathPrompted)
	}
}

// TestErrUnreachable_ClassificationSurface is a package-boundary sanity
// check: toolloop's family constants used throughout these tests must
// still be the values floor.go keys off, so a rename anywhere doesn't
// silently desync the fixtures above from the real classification.
func TestErrUnreachable_ClassificationSurface(t *testing.T) {
	t.Parallel()
	if toolloop.FamilyDestructive != "destructive" {
		t.Fatalf("toolloop.FamilyDestructive = %q, want %q", toolloop.FamilyDestructive, "destructive")
	}
	if !errors.Is(unreachableErr, risk.ErrUnreachable) {
		t.Fatal("test fixture unreachableErr does not satisfy errors.Is(_, risk.ErrUnreachable)")
	}
}
