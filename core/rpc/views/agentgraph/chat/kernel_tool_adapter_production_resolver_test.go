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
	"os"
	"path/filepath"
	"testing"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// productionPermsAdapter duplicates core/rpc's unexported
// chatPermsAdapter (api.go:6779) — that type is the only thing
// standing between a toolloop.PermissionResolver and this package's
// ToolPermissionResolver in production, and it cannot be imported here
// (core/rpc imports this package, not the reverse). Go's compiler only
// enforces that both types structurally satisfy the same two-type
// contract (toolloop.PermissionResolver in, ToolPermissionResolver
// out) — a signature mismatch is a compile error, but a *behavioural*
// difference is not. This copy is intentionally NOT byte-identical to
// the real one: chatPermsAdapter.Resolve guards `p.inner == nil` and
// returns auto_allow; this copy omits that guard because every test
// in this file constructs it with a non-nil inner (a real production
// resolver chain) — a nil-inner double would be testing a case these
// tests do not exercise. That omission is inert today, not free of
// drift risk: if this package ever grows a test that passes a nil
// inner expecting the real adapter's auto_allow fallback, this double
// will panic instead of matching production. Keep this comment
// updated if the real adapter's nil-guard semantics change.
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

// productionResolverWithFailSafe builds the resolver chain core/rpc/
// api.go now constructs at boot: NewStaticResolverFromDataDir, and on
// a load error (malformed mcp_servers.json) toolloop.
// NewFailSafeStaticResolver instead of a bare nil static arm — the
// same two-function sequence api.go's newLLMStack wiring runs, so a
// regression there (reverting to "leave staticPerms nil on error")
// makes this test fail the same way it would fail in production.
func productionResolverWithFailSafe(t *testing.T, dataDir string) toolloop.PermissionResolver {
	t.Helper()
	staticPerms, err := toolloop.NewStaticResolverFromDataDir(dataDir)
	if err != nil {
		staticPerms = toolloop.NewFailSafeStaticResolver("mcp_servers.json failed to load (" + err.Error() + "); tool calls require confirmation until it is repaired")
	}
	return toolloop.NewMergedResolver(staticPerms, nil)
}

// TestProductionResolver_CorruptFileDoesNotAutoAllow reproduces the
// review's empirical probe on trust-surfaces-that-fire-01PMZ202 WP24
// almost verbatim: write a real `deny` rule through the production
// writer, confirm the production resolver enforces it, then corrupt
// mcp_servers.json on disk (a truncated write, a disk error, a hand-
// edit) and confirm the resolver does NOT silently fall back to
// auto_allow. Before this fix, a malformed file left the static
// resolver nil, and NewMergedResolver's nil-static normalization is
// auto_allow — converting "shell exec is denied" into "shell exec is
// silently allowed" with only a log line. The fix must degrade to
// confirm_each instead: the user is asked, never silently allowed.
//
// productionResolverWithFailSafe (above) mirrors core/rpc/api.go's
// newLLMStack wiring — same two functions, same order, same branch —
// but is not a call into api.go itself; api.New is too heavyweight to
// construct in this package's tests, which is why every other test in
// this file goes through the same kind of mirrored helper
// (productionResolver above) rather than api.New. This test's
// mutation coverage is therefore at the toolloop layer, not api.go's
// wiring line: it catches a regression in NewFailSafeStaticResolver
// itself, or in this file's helper drifting from api.go's actual
// sequence, but NOT a regression where api.go's real wiring stops
// calling NewFailSafeStaticResolver while this helper still does.
//
// MUTATION EVIDENCE (run and confirmed to fail, then reverted): change
// NewFailSafeStaticResolver's rule literal in core/toolloop/perms.go
// from PolicyConfirmEach to PolicyAutoAllow, and this test's post-
// corruption assertion fails with Policy=auto_allow, Reason=<the
// fail-safe reason string> — reproducing the review's exact
// POST-CORRUPTION probe output (Policy:auto_allow, Reason:"" in the
// review's case, since the review's probe predates this fix and had
// no fail-safe reason string to carry).
func TestProductionResolver_CorruptFileDoesNotAutoAllow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Pre-corruption: a real deny rule, written through the same
	// SetStaticRule call SetToolPolicy makes, is enforced.
	if err := toolloop.SetStaticRule(dir, toolloop.StaticRule{
		Server: "github", Tool: "exec", Policy: toolloop.PolicyDeny, Reason: "shell exec disabled",
	}); err != nil {
		t.Fatalf("SetStaticRule: %v", err)
	}
	pre := productionResolverWithFailSafe(t, dir)
	preRes, err := pre.Resolve(context.Background(), "sess-corrupt", "github", "exec")
	if err != nil {
		t.Fatalf("pre-corruption Resolve: %v", err)
	}
	if preRes.Policy != toolloop.PolicyDeny {
		t.Fatalf("pre-corruption Policy = %q, want %q", preRes.Policy, toolloop.PolicyDeny)
	}

	// Corrupt the file in place — truncated JSON, the way a crash
	// mid-write or a bad hand-edit would leave it. (The writer itself
	// is atomic; this simulates damage from OUTSIDE the writer's
	// control, which is exactly the case the nil-static fallback was
	// silently mishandling.)
	path := filepath.Join(dir, "mcp_servers.json")
	if err := os.WriteFile(path, []byte(`{"version": 1, "rules": [{"server": "git`), 0o600); err != nil {
		t.Fatalf("corrupt mcp_servers.json: %v", err)
	}

	// Sanity: the corrupted file really does make
	// NewStaticResolverFromDataDir fail, so the fail-safe branch this
	// test exercises is the one actually reached.
	if _, loadErr := toolloop.NewStaticResolverFromDataDir(dir); loadErr == nil {
		t.Fatal("NewStaticResolverFromDataDir returned no error on truncated JSON — test fixture is not actually corrupt")
	}

	post := productionResolverWithFailSafe(t, dir)
	postRes, err := post.Resolve(context.Background(), "sess-corrupt", "github", "exec")
	if err != nil {
		t.Fatalf("post-corruption Resolve: %v", err)
	}
	if postRes.Policy == toolloop.PolicyAutoAllow {
		t.Fatalf("post-corruption Policy = auto_allow — a corrupt mcp_servers.json silently disabled the user's configured deny rule (reason=%q)", postRes.Reason)
	}
	if postRes.Policy != toolloop.PolicyConfirmEach {
		t.Fatalf("post-corruption Policy = %q, want %q (fail-safe degrade)", postRes.Policy, toolloop.PolicyConfirmEach)
	}
	if postRes.Reason == "" {
		t.Fatal("post-corruption Reason is empty — the degrade must explain WHY the call now requires confirmation")
	}

	// End-to-end: a real kernelToolAdapter.Call through the same
	// post-corruption resolver parks on ConfirmBus rather than
	// silently dispatching, exactly like the pre-existing
	// TestProductionResolver_WrittenRuleParksOnConfirmBus proves for
	// an intact confirm_each rule.
	pool := &countingToolPool{server: "github", tool: "exec"}
	spy := &confirmSpy{}
	bus := toolloop.NewConfirmBus(spy.publish)
	adapter := newKernelToolAdapter(pool, productionPermsAdapter{inner: post}, "sess-corrupt").withConfirm(bus)

	done := make(chan callResult, 1)
	go func() {
		res, err := adapter.Call(context.Background(), coreag.ToolCall{Name: "github__exec"})
		done <- callResult{res: res, err: err}
	}()
	awaitParked(t, bus, 1)
	if got := pool.dispatched(); len(got) != 0 {
		t.Fatalf("tool dispatched without confirmation against a corrupted mcp_servers.json: %v", got)
	}

	events := spy.snapshot()
	if err := bus.Resolve("sess-corrupt", events[0].CallID, toolloop.ConfirmDecision{Approved: false}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after denial")
	}
	if got := pool.dispatched(); len(got) != 0 {
		t.Fatalf("dispatched = %v, want zero — the call was denied at the confirm prompt", got)
	}
}
