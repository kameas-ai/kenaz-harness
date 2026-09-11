package chat

// trust-surfaces-that-fire-01PMZ202 WP24 (finding CHAT-05).
//
// Every other confirm_each test in this package (kernel_tool_adapter_
// confirm_test.go, _confirm_paths_test.go, _skipset_test.go,
// _autonomy_test.go) injects a fake syncPermResolver that hands the
// adapter a scripted PermVerdict{Policy: "confirm_each"} directly. That
// is exactly the gap the audit named: those tests prove the LADDER
// works once it is handed a confirm_each verdict, but nothing proved a
// PRODUCTION resolver — toolloop.NewStaticResolverFromDataDir reading
// a real file on real disk — could ever produce that verdict in the
// first place. Before WP24, no writer existed anywhere in the tree, so
// the file this resolver reads was always empty and every verdict was
// auto_allow.
//
// These tests write through the same Go call SetToolPolicy makes
// (toolloop.SetStaticRule), reconstruct the resolver exactly the way
// core/rpc/api.go does at chassis boot
// (toolloop.NewStaticResolverFromDataDir + toolloop.NewMergedResolver),
// and drive a real kernelToolAdapter.Call through it — no fake
// resolver anywhere in these two tests.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted):
//   - AC-24a/AC-24c: make toolloop.SetStaticRule a no-op (skip the
//     writeStaticConfigAtomic call) -> both this file's
//     TestProductionResolver_WrittenRuleParksOnConfirmBus AND
//     perms_test.go's TestSetStaticRule_WrittenRuleSurvivesFreshResolver
//     AND core/rpc/views/mcp's TestAPI_SetToolPolicy_WritesRealFile...
//     fail (the first times out waiting for a park that never happens;
//     the other two see policy=auto_allow / an empty rule list).
//   - AC-24b: strip AutoApproveFamilies out of promptSkipSet (return an
//     empty PromptSkipSet regardless of knobs) ->
//     TestProductionResolver_AutoApproveFamiliesChangesWhichCallsPark
//     fails: the read-classified call, no longer skipped, hits rung 5
//     (no confirmation channel attached to that adapter) and returns a
//     denied tool result instead of dispatching.

import (
	"context"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// productionPermsAdapter duplicates core/rpc's unexported
// chatPermsAdapter exactly (api.go:6741) — that type is the only thing
// standing between a toolloop.PermissionResolver and this package's
// ToolPermissionResolver in production, and it cannot be imported here
// (core/rpc imports this package, not the reverse). Any drift between
// this copy and the real one would show up as a compile error in
// api.go, not as a silently-divergent test double, because both are
// structurally required to satisfy the same two-type contract
// (toolloop.PermissionResolver in, ToolPermissionResolver out).
type productionPermsAdapter struct {
	inner toolloop.PermissionResolver
}

func (p productionPermsAdapter) Resolve(ctx context.Context, sessionID, server, tool string) (PermVerdict, error) {
	res, err := p.inner.Resolve(ctx, sessionID, server, tool)
	if err != nil {
		return PermVerdict{}, err
	}
	return PermVerdict{
		Server: res.Server,
		Tool:   res.Tool,
		Policy: string(res.Policy),
		Reason: res.Reason,
	}, nil
}

// productionResolver builds the exact resolver chain core/rpc/api.go
// constructs at boot from a DataDir: NewStaticResolverFromDataDir
// wrapped in NewMergedResolver (session arm nil — this test does not
// exercise per-session overrides, which are a separate, still-unwired
// seam per spec.md §14.9).
func productionResolver(t *testing.T, dataDir string) toolloop.PermissionResolver {
	t.Helper()
	staticPerms, err := toolloop.NewStaticResolverFromDataDir(dataDir)
	if err != nil {
		t.Fatalf("NewStaticResolverFromDataDir: %v", err)
	}
	return toolloop.NewMergedResolver(staticPerms, nil)
}

// AC-24a: a permission written through the writer (SetStaticRule, the
// function SetToolPolicy calls) yields a confirm_each resolution from
// the PRODUCTION resolver, and a tool call under it parks on
// ConfirmBus rather than silently dispatching.
func TestProductionResolver_WrittenRuleParksOnConfirmBus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := toolloop.SetStaticRule(dir, toolloop.StaticRule{
		Server: "filesystem", Tool: "*", Policy: toolloop.PolicyConfirmEach, Reason: "fs default",
	}); err != nil {
		t.Fatalf("SetStaticRule: %v", err)
	}

	perms := productionResolver(t, dir)
	pool := &countingToolPool{server: "filesystem", tool: "delete_file"}
	spy := &confirmSpy{}
	bus := toolloop.NewConfirmBus(spy.publish)
	adapter := newKernelToolAdapter(pool, productionPermsAdapter{inner: perms}, "sess-prod").withConfirm(bus)

	done := make(chan callResult, 1)
	go func() {
		res, err := adapter.Call(context.Background(), coreag.ToolCall{Name: "filesystem__delete_file"})
		done <- callResult{res: res, err: err}
	}()
	awaitParked(t, bus, 1)

	if got := pool.dispatched(); len(got) != 0 {
		t.Fatalf("tool dispatched before confirmation, via a resolver reading a written file: %v", got)
	}
	events := spy.snapshot()
	if len(events) != 1 || events[0].Server != "filesystem" || events[0].Tool != "delete_file" {
		t.Fatalf("confirm events = %+v, want exactly one for filesystem.delete_file", events)
	}
	if events[0].Reason != "fs default" {
		t.Fatalf("event reason = %q, want the rule's reason to survive the file round-trip", events[0].Reason)
	}

	if err := bus.Resolve("sess-prod", events[0].CallID, toolloop.ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.res.IsError {
			t.Fatalf("approved call failed: res=%+v err=%v", r.res, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after approval")
	}
	if got := pool.dispatched(); len(got) != 1 {
		t.Fatalf("dispatched = %v, want exactly one call after approval", got)
	}
}

// AC-24b: AutoApproveFamilies measurably changes which calls park,
// through the SAME production resolver. A read-classified tool skips
// the prompt when the family is auto-approved; a write-classified tool
// on the identical server still parks — proving the skip is per-family,
// not per-server, and that the "only consumer" of AutoApproveFamilies
// (promptSkipSet at kernel_tool_adapter.go:425-426) is reachable end to
// end from a written file, not just from a fake verdict.
func TestProductionResolver_AutoApproveFamiliesChangesWhichCallsPark(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := toolloop.SetStaticRule(dir, toolloop.StaticRule{
		Server: "filesystem", Tool: "*", Policy: toolloop.PolicyConfirmEach,
	}); err != nil {
		t.Fatalf("SetStaticRule: %v", err)
	}
	perms := productionResolver(t, dir)

	knobs := autonomy.ResolvedKnobs{AutoApproveFamilies: autonomy.NewFamilySet(autonomy.FamilyRead)}
	provider := func(ctx context.Context, sessionID string) autonomy.ResolvedKnobs { return knobs }

	// "list_files" classifies FamilyRead (toolloop.ClassifyToolFamily) —
	// AutoApproveFamilies=[read] must skip the prompt and dispatch
	// immediately, synchronously, with no ConfirmBus involvement.
	readPool := &countingToolPool{server: "filesystem", tool: "list_files"}
	readAdapter := newKernelToolAdapter(readPool, productionPermsAdapter{inner: perms}, "sess-read").
		withAutonomy(provider)
	res, err := readAdapter.Call(context.Background(), coreag.ToolCall{Name: "filesystem__list_files"})
	if err != nil {
		t.Fatalf("read-family Call err = %v, want nil (auto-approved by family skip)", err)
	}
	if res.IsError {
		t.Fatalf("read-family call returned an error result, want auto-approved dispatch: %q", res.Content)
	}
	if got := readPool.dispatched(); len(got) != 1 {
		t.Fatalf("dispatched = %v, want the read call to dispatch immediately with no confirmation", got)
	}

	// "write_file" classifies FamilyWrite, which is NOT in
	// AutoApproveFamilies — it must still park on the bus, proving the
	// skip did not widen past the one family the knob names.
	writePool := &countingToolPool{server: "filesystem", tool: "write_file"}
	spy := &confirmSpy{}
	bus := toolloop.NewConfirmBus(spy.publish)
	writeAdapter := newKernelToolAdapter(writePool, productionPermsAdapter{inner: perms}, "sess-write").
		withConfirm(bus).
		withAutonomy(provider)

	done := make(chan callResult, 1)
	go func() {
		res, err := writeAdapter.Call(context.Background(), coreag.ToolCall{Name: "filesystem__write_file"})
		done <- callResult{res: res, err: err}
	}()
	awaitParked(t, bus, 1)
	if got := writePool.dispatched(); len(got) != 0 {
		t.Fatalf("write call dispatched without confirmation despite AutoApproveFamilies=[read] only: %v", got)
	}

	ev := spy.snapshot()[0]
	if err := bus.Resolve("sess-write", ev.CallID, toolloop.ConfirmDecision{Approved: true}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.res.IsError {
			t.Fatalf("approved write call failed: res=%+v err=%v", r.res, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write Call did not return after approval")
	}
}
