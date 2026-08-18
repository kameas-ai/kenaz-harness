package slashcmd

import (
	"context"
	"strings"
	"testing"
)

// The four v1 commands previously registered as stubs (/memorize,
// /recall, /forget, /branch) are now real implementations wired against
// MemoryGateway / BranchGateway. The stubCommand type itself is kept as
// a building block for future "registered but not yet wired" commands;
// these tests cover that the helper still produces the canned
// coming-soon body so a downstream caller registering a stub still
// renders correctly.
//
// Dated keep-decision (engineer-truth-pass-01PMTP01 WP07, per CLAUDE.md
// §"Disposition: delete vs. finish" — a justification names the blocker
// and the owner): stamped 2026-08-18. Owner: unassigned; nothing
// currently registers a stubCommand, so it has zero non-test callers
// today, same as a true dead symbol — but CLAUDE.md is explicit that a
// useful half-built feature (here, a reusable "advertised but not yet
// wired" shape) deleted is a product decision made by a linter, and
// this type is exactly the shape a future command needs on day one of
// being announced-but-unwired. The blocker to closing this entry is a
// concrete command choosing to use it; if none has by the next sweep
// that revisits this file, that sweep should re-ask whether to delete.

func TestStubCommand_HelperStillRendersCannedBody(t *testing.T) {
	t.Parallel()
	stub := stubCommand{name: "future", description: "Future command (coming soon)."}
	res, err := stub.Run(context.Background(), Env{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "/future") {
		t.Errorf("Text missing command name: %q", res.Text)
	}
	if !strings.Contains(res.Text, "registered but not yet wired") {
		t.Errorf("Text missing the mission-agnostic not-yet-wired phrasing: %q", res.Text)
	}
	if strings.Contains(res.Text, "agent-kernel-graph") {
		t.Errorf("Text still names the archived agent-kernel-graph mission: %q", res.Text)
	}
	if got, _ := res.Metadata[MetaKeyOwningMission].(string); got != owningMissionUnassigned {
		t.Errorf("owningMission = %q, want %q", got, owningMissionUnassigned)
	}
	if !stub.ComingSoon() {
		t.Error("stubCommand.ComingSoon() should be true")
	}
}

func TestRealCommands_NotMarkedComingSoon(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	for _, name := range []string{"memorize", "recall", "forget", "branch"} {
		cmd, ok := r.Lookup(name)
		if !ok {
			t.Errorf("%q not registered", name)
			continue
		}
		if cmd.ComingSoon() {
			t.Errorf("/%s should no longer be marked ComingSoon — wired in v0.3.0", name)
		}
	}
}
