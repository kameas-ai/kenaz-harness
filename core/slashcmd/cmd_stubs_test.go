package slashcmd

import (
	"context"
	"strings"
	"testing"
)

func TestStubCommand_MemorizeReturnsCannedMessage(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, err := r.Execute(context.Background(), "s", "/memorize hello world")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "/memorize") {
		t.Errorf("Text missing command name: %q", res.Text)
	}
	if !strings.Contains(res.Text, "agent-kernel-graph") {
		t.Errorf("Text missing owning-mission reference: %q", res.Text)
	}
	got, ok := res.Metadata[MetaKeyOwningMission].(string)
	if !ok {
		t.Fatalf("Metadata[%s] not a string: %v", MetaKeyOwningMission, res.Metadata[MetaKeyOwningMission])
	}
	if got != owningMissionAgentKernelGraph {
		t.Errorf("owningMission = %q, want %q", got, owningMissionAgentKernelGraph)
	}
}

func TestStubCommand_BranchMarkedComingSoon(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	cmd, ok := r.Lookup("branch")
	if !ok {
		t.Fatal("/branch not registered")
	}
	if !cmd.ComingSoon() {
		t.Error("/branch should be marked ComingSoon")
	}
}

func TestStubCommands_AllPointAtAgentKernelGraph(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	for _, name := range []string{"memorize", "recall", "forget", "branch"} {
		cmd, ok := r.Lookup(name)
		if !ok {
			t.Errorf("%q not registered", name)
			continue
		}
		res, err := cmd.Run(context.Background(), Env{Registry: r}, nil)
		if err != nil {
			t.Errorf("%s.Run: %v", name, err)
			continue
		}
		if got := res.Metadata[MetaKeyOwningMission]; got != owningMissionAgentKernelGraph {
			t.Errorf("%s owningMission = %v, want %q", name, got, owningMissionAgentKernelGraph)
		}
	}
}
