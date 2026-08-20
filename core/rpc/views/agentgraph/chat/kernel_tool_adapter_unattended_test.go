package chat

// kernel_tool_adapter_unattended_test.go — mission
// model-scheduled-jobs-01PMSJ01 WP05, AC-004 + FR-008 + H-1.
//
// H-1: "confirm_each parks with no deadline, and the headless escape is
// process-global." A scheduled run must resolve confirm_each at rung 5
// with toolloop.HeadlessDeny, unconditionally — not the deployment's
// configured Headless policy, which answers a different question ("does
// this whole deployment have a human anywhere") than "is THIS run
// attended".

import (
	"context"
	"strings"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// TestConfirmLadder_UnattendedDeniesEvenWithALiveChannel is AC-004:
// "production-shaped fixture — confirm bus WITH a channel (assert
// HasChannel() == true), env var unset. A confirm_each tool call in an
// unattended run terminates within the deadline, is denied, and produces
// the audit record."
//
// The fixture's bus auto-APPROVES (Approved: true) and neither
// deps.Headless nor deps.HeadlessExplicit is set — so if the unattended
// posture is ignored, the call prompts, is approved, and dispatches,
// which fails this test on exactly that mutant (same technique as
// TestConfirmLadder_ExplicitHeadlessDenyBypassesLivePromptChannel above).
func TestConfirmLadder_UnattendedDeniesEvenWithALiveChannel(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)
	if !f.bus.HasChannel() {
		t.Fatal("fixture bus reports no channel — AC-004 requires HasChannel()==true")
	}

	type callOutcome struct {
		res coreag.ToolResult
		err error
	}
	done := make(chan callOutcome, 1)
	go func() {
		res, err := f.adapter.Call(runposture.Unattended(context.Background()), coreag.ToolCall{
			Name: "filesystem__write_file",
			Args: map[string]any{"path": "/etc/hosts"},
		})
		done <- callOutcome{res: res, err: err}
	}()

	var outcome callOutcome
	select {
	case outcome = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("unattended confirm_each call did not terminate within the deadline — it parked")
	}
	if outcome.err != nil {
		t.Fatalf("Call: %v", outcome.err)
	}
	res := outcome.res

	if !res.IsError {
		t.Fatal("unattended confirm_each dispatched — must deny by construction")
	}
	if !strings.Contains(res.Content, "unattended") {
		t.Errorf("deny reason does not name the unattended posture: %q", res.Content)
	}
	if got := f.promptCount(); got != 0 {
		t.Fatalf("prompted %d times on an unattended run, want 0", got)
	}
	if n := len(f.pool.dispatched()); n != 0 {
		t.Fatalf("dispatched %d calls under an unattended deny", n)
	}

	d := f.onlyDecision(t)
	if d.Path != audit.ToolConfirmPathHeadlessPolicy {
		t.Fatalf("path = %q, want %q", d.Path, audit.ToolConfirmPathHeadlessPolicy)
	}
	if d.Approved {
		t.Error("unattended deny recorded as approved")
	}
	if !strings.Contains(d.Reason, "unattended") {
		t.Errorf("audit reason does not name the unattended posture: %q", d.Reason)
	}
}

// TestConfirmLadder_InteractiveRunStillParksAndIsAnswerable is FR-008:
// the SAME adapter configuration, called WITHOUT the unattended marker,
// still reaches rung 6 and is answerable — proving WP05 is a per-run
// override, not a change to the default interactive behaviour.
func TestConfirmLadder_InteractiveRunStillParksAndIsAnswerable(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{Approved: true}, nil)

	res := f.call(t) // plain context.Background() — no unattended marker.
	if res.IsError {
		t.Fatalf("interactive call denied: %q", res.Content)
	}
	if got := f.promptCount(); got != 1 {
		t.Fatalf("prompted %d times, want exactly 1 (the ladder must still reach rung 6 and park)", got)
	}
	if n := len(f.pool.dispatched()); n != 1 {
		t.Fatalf("dispatched %d calls, want 1 (the answered approval must still dispatch)", n)
	}
	d := f.onlyDecision(t)
	if d.Path == audit.ToolConfirmPathHeadlessPolicy {
		t.Fatal("interactive call resolved at the headless rung — WP05 leaked into the default path")
	}
}

// TestConfirmLadder_UnattendedIgnoresDeploymentHeadlessAllow proves the
// unattended posture wins over a deployment configured HeadlessAllow —
// spec.md §5.4's point that a per-run posture must not read as "this
// deployment has no human" widening to "therefore this scheduled run may
// proceed unwatched too."
func TestConfirmLadder_UnattendedIgnoresDeploymentHeadlessAllow(t *testing.T) {
	t.Parallel()

	f := newLadder(t, toolloop.ConfirmDecision{}, func(_ *toolloop.ConfirmBus, _ *ladderFixture, deps *ConfirmDeps) {
		deps.Headless = toolloop.HeadlessAllow
		deps.HeadlessExplicit = true
	})

	res, err := f.adapter.Call(runposture.Unattended(context.Background()), coreag.ToolCall{
		Name: "filesystem__write_file",
		Args: map[string]any{"path": "/etc/hosts"},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !res.IsError {
		t.Fatal("unattended run dispatched under a deployment HeadlessAllow — must still deny")
	}
	d := f.onlyDecision(t)
	if d.Approved {
		t.Error("unattended run recorded as approved despite deployment HeadlessAllow")
	}
}
