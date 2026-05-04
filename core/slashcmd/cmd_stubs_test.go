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
	if !strings.Contains(res.Text, "agent-kernel-graph") {
		t.Errorf("Text missing owning-mission reference: %q", res.Text)
	}
	if got, _ := res.Metadata[MetaKeyOwningMission].(string); got != owningMissionAgentKernelGraph {
		t.Errorf("owningMission = %q, want %q", got, owningMissionAgentKernelGraph)
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
