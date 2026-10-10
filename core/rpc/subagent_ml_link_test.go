package rpc

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// The registration points WP03 wires must accept the recorder as-is.
var (
	_ coreag.ToolCallObserver = (*mlproducer.Recorder)(nil)
	_ chat.TurnUsageObserver  = (*mlproducer.Recorder)(nil)
	_ mlproducer.ParentLinker = (*mlproducer.Recorder)(nil)
	_ session.DeleteObserver  = (*mlproducer.Recorder)(nil).SessionDeleted
	_                         = bash.Options{EnvProvider: (*mlproducer.Recorder)(nil).EnvProvider(nil)}
)

// ml-producer-01MLPRD01 WP02 (spec §12 A-3): the spawner links a child
// to its parent for the ML producer only from an attended spawn, or when
// the parent is itself a linked child (nested chain to an attended root).
func TestLinkMLParent_OnlyAttendedChainsLink(t *testing.T) {
	rec := mlproducer.NewRecorder(mlproducer.Config{SweepInterval: -1})
	defer rec.Close()

	attended := context.Background()
	unattended := runposture.Unattended(context.Background())

	// A subagent spawned from an attended chat: linked.
	linkMLParent(attended, rec, "child-1", "root-1")
	if !rec.IsLinked("child-1") {
		t.Fatal("child of an attended session was not linked")
	}
	// Its own subagent: the spawn ctx is the child's (unattended), but the
	// parent is linked — the grandchild chains to root-1.
	linkMLParent(unattended, rec, "grandchild-1", "child-1")
	if !rec.IsLinked("grandchild-1") {
		t.Fatal("nested child of a linked child was not linked")
	}
	if env := rec.ProcessEnv("grandchild-1"); len(env) != 1 || env[0] != "KENAZ_ACTOR=agent" {
		// No hasher configured: only the actor marker.
		t.Fatalf("ProcessEnv = %v", env)
	}
	// A subagent of a scheduled chat: the spawn ctx is unattended and the
	// parent is not linked — never linked, so its work stays dropped.
	linkMLParent(unattended, rec, "child-2", "scheduled-root")
	if rec.IsLinked("child-2") {
		t.Fatal("a subagent of a scheduled (unattended) run was linked")
	}
	// nil linker is inert.
	linkMLParent(attended, nil, "c", "p")
}
