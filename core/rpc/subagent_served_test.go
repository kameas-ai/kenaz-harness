package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coresubagent "github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
)

type derivedRecorder struct {
	mu       sync.Mutex
	declared []string
}

func (d *derivedRecorder) fn(declared string) (string, string, bool) {
	d.mu.Lock()
	d.declared = append(d.declared, declared)
	d.mu.Unlock()
	return "test-profile", "derived-model", true
}

func (d *derivedRecorder) snapshot() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.declared...)
}

func dispatchSync(t *testing.T, stack *subagentSpawnerTestStack, profile string) (map[string]any, string) {
	t.Helper()
	parent, err := stack.sessionsAPI.Create(context.Background(), "parent session")
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	tool := coresubagent.New(coresubagent.Options{DataDir: t.TempDir(), Seam: stack.seam})
	ctx := toolloop.WithSessionID(context.Background(), parent.ID)
	raw, err := tool.Call(ctx, json.RawMessage(`{"profile":"`+profile+`","prompt":"go","run_in_background":false}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, parent.ID
}

// B11: a served workbench has no personal provider (DefaultProfile "")
// and the sub-agent still spawns, on the derived profile, with the
// profile's declared model handed to the derivation.
func TestSubagentSpawn_ServedEmptySlotsDerivesProfile(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "served worker done")
	rec := &derivedRecorder{}
	stack.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM: stack.llmAPI, Bus: stack.bus, Tasks: stack.tasks,
		DefaultProfile: func() string { return "" },
		DerivedModel:   rec.fn,
		Timeout:        5 * time.Second,
	}))
	out, _ := dispatchSync(t, stack, "explore")
	if out["status"] != "complete" {
		t.Fatalf("result = %+v, want complete", out)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "anthropic/claude-haiku-4.5" {
		t.Fatalf("DerivedModel calls = %v, want the explore profile's declared model", got)
	}
}

// An explicitly configured profile wins: the derivation is never consulted.
func TestSubagentSpawn_ExplicitProfileWinsOverDerived(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "desktop worker done")
	rec := &derivedRecorder{}
	stack.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM: stack.llmAPI, Bus: stack.bus, Tasks: stack.tasks,
		DefaultProfile: func() string { return "test-profile" },
		DerivedModel:   rec.fn,
		Timeout:        5 * time.Second,
	}))
	if out, _ := dispatchSync(t, stack, "explore"); out["status"] != "complete" {
		t.Fatalf("result = %+v", out)
	}
	if got := rec.snapshot(); len(got) != 0 {
		t.Fatalf("DerivedModel consulted %v with an explicit profile configured", got)
	}
}

// B11 orphan cleanup: a spawn that fails before the child's first turn
// leaves no child session and no branch row behind (real sqlite).
func TestSubagentSpawnFailure_DeletesOrphanChild(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "never")
	stack.seam.SetRunSpawner(NewSubagentRunSpawner(SubagentRunSpawnerDeps{
		LLM: stack.llmAPI, Bus: stack.bus, Tasks: stack.tasks,
		DefaultProfile: func() string { return "" },
	}))
	parent, err := stack.sessionsAPI.Create(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	before, err := stack.sessionsAPI.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, ferr := stack.seam.Fork(context.Background(), coreag.ForkRequest{
		ParentSessionID: parent.ID, Title: "New session (branch)", HandoffPrompt: "hi", ProfileID: "explore",
	})
	if ferr == nil || !strings.Contains(ferr.Error(), "no default LLM profile configured") {
		t.Fatalf("Fork err = %v, want the spawn failure", ferr)
	}
	after, err := stack.sessionsAPI.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("sessions %d -> %d: the unstarted child was left behind", len(before), len(after))
	}
	convMgr := newConversationManager(stack.core)
	brs, err := convMgr.ListByParent(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(brs) != 0 {
		t.Fatalf("branch rows left: %+v", brs)
	}
}

// OnRunDone fires once on completion without stealing WaitForChildRun.
func TestSubagentSpawn_OnRunDoneDoesNotConsumeWait(t *testing.T) {
	stack := buildSubagentSpawnerTestStack(t, "ok")
	stack.armSpawner(t, 5*time.Second)
	parent, err := stack.sessionsAPI.Create(context.Background(), "parent")
	if err != nil {
		t.Fatal(err)
	}
	doneCh := make(chan error, 2)
	h, err := stack.seam.Fork(context.Background(), coreag.ForkRequest{
		ParentSessionID: parent.ID, Title: "w", HandoffPrompt: "go",
		OnRunDone: func(e error) { doneCh <- e },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if werr := stack.seam.WaitForChildRun(ctx, h.BranchID); werr != nil {
		t.Fatalf("WaitForChildRun: %v", werr)
	}
	select {
	case e := <-doneCh:
		if e != nil && !errors.Is(e, context.Canceled) {
			t.Fatalf("OnRunDone err = %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnRunDone never fired")
	}
	if len(doneCh) != 0 {
		t.Fatal("OnRunDone fired twice")
	}
}
