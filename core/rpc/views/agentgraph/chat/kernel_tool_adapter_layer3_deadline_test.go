package chat

// WP07 (risk-rated-autonomy-01PMRA01) — the layer-3-originated confirm
// prompt gets its own bounded deadline that DENIES on expiry, so an
// unattended run never parks forever on the first un-granted MCP tool.
// The pre-existing "no deadline" invariant for an organically reached
// rung-6 confirm_each prompt (layer==0, owner decision 1) must be
// completely unaffected — proved below by configuring a tiny
// layer3PromptTimeout and confirming a layer==0 dispatch ignores it.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// TestKernelToolAdapter_Layer3PromptDeniesOnDeadline is WP07's
// bounded-time proof (mirrors the pattern
// TestApproval_NoWatcherFailsClosedWithinBoundedTime uses in
// core/rpc/views/agentgraph/resolve_approval_test.go — that test is a
// documented load-dependent flake, so this one uses a generous outer
// bound and a short configured deadline rather than asserting tight
// timing).
//
// Mutation-test property: flip promptConfirmEach's timeout branch to
// deliver Approved=true instead of false and this test must fail —
// both the IsError assertion and the Approved assertion below would
// flip.
func TestKernelToolAdapter_Layer3PromptDeniesOnDeadline(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold)

	rec := &recordingAudit{}
	// Nobody ever answers — simulates an unattended run parked on a
	// layer-3-originated prompt.
	bus := toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {})
	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}

	adapter := newKernelToolAdapter(pool, perms, "sess-layer3-deadline").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Audit: rec})
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	adapter.withLayer3PromptTimeout(30 * time.Millisecond)

	start := time.Now()
	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Call: %v (WP07: the run must continue with a denial, not abort with an error)", err)
	}
	if !result.IsError {
		t.Fatal("expected a deny result on deadline expiry, got success — WP07 owner directive: never allow on timeout, at any tier")
	}
	// Generous upper bound: the 30ms deadline should fire well within a
	// couple seconds even under CI scheduling pressure.
	if elapsed > 5*time.Second {
		t.Fatalf("Call took %s, want well under 5s for a 30ms configured deadline", elapsed)
	}

	decisions := rec.decisions(t)
	if len(decisions) != 1 {
		t.Fatalf("emitted %d decision records, want exactly 1: %+v", len(decisions), decisions)
	}
	d := decisions[0]
	if d.Path != audit.ToolConfirmPathLayer3Timeout {
		t.Errorf("Path = %q, want %q", d.Path, audit.ToolConfirmPathLayer3Timeout)
	}
	if d.Layer != 3 {
		t.Errorf("Layer = %d, want 3", d.Layer)
	}
	if d.Approved {
		t.Fatal("timeout decision recorded Approved=true, want false — never allow on timeout")
	}
	if d.Reason == "" {
		t.Error("timeout decision has no reason — the model must be told why the call was denied")
	}
}

// TestKernelToolAdapter_Layer0PromptIgnoresLayer3Deadline pins that
// WP07's new deadline is scoped to layer==3 ONLY. An organically reached
// rung-6 confirm_each prompt (layer==0, no risk gate involved at all)
// must keep the pre-existing "no deadline" behaviour (owner decision 1)
// exactly as it was — proved here by configuring an aggressively short
// layer3PromptTimeout and confirming a layer==0 dispatch still blocks
// past it, resolving only when the CALLER's ctx expires.
func TestKernelToolAdapter_Layer0PromptIgnoresLayer3Deadline(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "echo"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "echo", Policy: "confirm_each"},
	}
	// No gate wired at all: this call reaches rung 6 (layer==0) the
	// ordinary way, never touching rung 0's layer-3 machinery.
	bus := toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {})

	adapter := newKernelToolAdapter(pool, perms, "sess-layer0-nodeadline").withConfirm(bus)
	// Deliberately aggressive: if this leaked into the layer==0 path, the
	// call would return in ~10ms instead of waiting for ctx.
	adapter.withLayer3PromptTimeout(10 * time.Millisecond)

	const callerBound = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), callerBound)
	defer cancel()

	start := time.Now()
	_, err := adapter.Call(ctx, makeCall("myserver", "echo"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected the caller's ctx cancellation to surface as an error")
	}
	if elapsed < callerBound/2 {
		t.Fatalf("Call returned after %s, want close to the caller's %s bound — the 10ms layer3PromptTimeout must NOT apply to a layer==0 organic prompt", elapsed, callerBound)
	}
}

// TestKernelToolAdapter_Layer3PromptUnattendedDeniesImmediately is the
// WP07 refinement: an unattended run posture denies a layer-3 prompt
// IMMEDIATELY rather than waiting out layer3PromptTimeout — nobody is
// present to answer regardless of the bound, so waiting even the
// generous default (tuned for an attended human who stepped away) would
// make every layer-3 call in an unattended run needlessly slow. Mirrors
// rung 5's own unattended-always-denies-without-waiting semantics,
// reached from the layer-3 entry point that bypasses rungs 1-5.
func TestKernelToolAdapter_Layer3PromptUnattendedDeniesImmediately(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierBold)

	rec := &recordingAudit{}
	promptedAt := false
	// Never resolves — if the adapter incorrectly parked here instead of
	// denying immediately, this test would hang until its own bound.
	bus := toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) { promptedAt = true })
	gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.NotApplicable}}

	adapter := newKernelToolAdapter(pool, perms, "sess-layer3-unattended").
		withConfirm(bus).
		withConfirmDeps(ConfirmDeps{Audit: rec})
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(gate)
	// A generous bound: if the unattended fast path did NOT fire, this
	// test would take (up to) this long instead of returning
	// immediately — proving the fast path matters, not just that SOME
	// bound exists.
	adapter.withLayer3PromptTimeout(5 * time.Second)

	start := time.Now()
	result, err := adapter.Call(runposture.Unattended(context.Background()), makeCall("myserver", "read_file"))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected a deny result for an unattended layer-3 prompt")
	}
	if promptedAt {
		t.Error("the confirm bus was parked (publisher fired) — an unattended run must deny before ever announcing a prompt nobody can answer")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Call took %s, want near-immediate — the unattended fast path must not wait out layer3PromptTimeout (5s)", elapsed)
	}

	decisions := rec.decisions(t)
	if len(decisions) != 1 {
		t.Fatalf("emitted %d decision records, want exactly 1: %+v", len(decisions), decisions)
	}
	if decisions[0].Approved {
		t.Fatal("unattended layer-3 decision recorded Approved=true, want false")
	}
}
