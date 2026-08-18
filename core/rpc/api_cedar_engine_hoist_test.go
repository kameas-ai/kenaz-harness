package rpc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// WP05 hoist tests (consent-surfaces-truth-01PMTR01) — "One Cedar engine
// per process; reload reaches live gates".
//
// THE DEFECT THESE PIN, PRECISELY. The v0.63.1 wiring suite
// (api_cedar_gate_wiring_test.go) proved policy PRESENT AT BOOT is
// enforced: every fixture there writes a .cedar file to <DataDir>/policy/
// BEFORE calling core.New / rpc.New, then boots and asserts the refusal.
// That suite is unmodified by this WP and stays green — see the
// TestCedarWiring_* / TestBuildCedarGate_* run below.
//
// What NONE of those tests exercise is the release note this WP fixes:
// boot -> SavePolicy (an IN-SESSION edit, after the process is already
// running) -> ReloadPolicies -> ListPolicies reports it loaded -> is the
// action ACTUALLY refused, in the SAME process, without a restart? Before
// the hoist the answer was no for twelve of the thirteen gate sites,
// because SavePolicy's reload only ever touched the one private engine
// the cedarpolicy view happened to hold — see cedarGate() and the
// cedarEngine field's doc comment on API in api.go.
//
// Every test below drives that exact path: api.CedarPolicy().SavePolicy
// (which internally calls Engine.Reload — see cedarpolicy/impl.go's
// reloadBestEffort) and/or an explicit api.CedarPolicy().ReloadPolicies,
// then asserts the REFUSAL at a live gate — never that a Gate field is
// merely non-nil.

// ---------------------------------------------------------------------------
// The headline acceptance (FR-006): both directions, one process, one site.
// ---------------------------------------------------------------------------

// TestCedarHoist_HeadlineAcceptance_InSessionSaveReachesMemoryGate is the
// FR-006 headline scenario verbatim: boot -> a memory write succeeds ->
// SavePolicy("forbid memory_write") returns ok -> ReloadPolicies returns
// nil -> ListPolicies reports it loaded -> the SAME memory write is
// refused -> DeletePolicy + reload -> it succeeds again. All in one
// process, with NO policy file present at boot (unlike the v0.63.1 suite).
//
// Mutation: revert the memory-write gate site (api.go's
// `gs.SetGate(&memoryGateAdapter{gate: a.cedarGate()})`) to its own
// `buildCedarGate(coreDataDir(c))` — the refusal assertion below must
// fail, because SavePolicy's reload would then be mutating a DIFFERENT
// engine than the one the memory gate reads.
func TestCedarHoist_HeadlineAcceptance_InSessionSaveReachesMemoryGate(t *testing.T) {
	api := cedarWiringAPI(t, "") // no policy file at boot
	ctx := context.Background()

	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef is nil — construction changed; this test no longer covers the wiring")
	}

	// 1. Boot state: a default install permits the write.
	if err := store.Add(ctx, memoryChunk()); err != nil {
		t.Fatalf("memory write refused on a default install (pre-edit): %v", err)
	}

	// 2. Author + save a forbid policy IN-SESSION (the process is already
	// running; this is not a file present before core.New).
	result, err := api.CedarPolicy().SavePolicy(ctx, "zz_wp05_forbid_memwrite.cedar", forbidPolicy(cedar.ActionMemoryWrite))
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("SavePolicy parse failed: %+v", result.Errors)
	}

	// 3. Explicit reload (SavePolicy already best-effort reloads; calling
	// it again mirrors the acceptance criteria's literal sequence and
	// proves the RPC-reachable ReloadPolicies path also works).
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	// 4. ListPolicies must report the new file loaded.
	files, err := api.CedarPolicy().ListPolicies(ctx)
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name == "zz_wp05_forbid_memwrite.cedar" && f.ParseOK {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListPolicies does not report zz_wp05_forbid_memwrite.cedar as loaded: %+v", files)
	}

	// 5. THE ASSERTION THAT MATTERS: the SAME memory write, in the SAME
	// process, is now refused. This is the exact scenario the v0.63.1
	// release commit recorded as broken.
	err = store.Add(ctx, memoryChunk())
	if err == nil {
		t.Fatal("memory write succeeded AFTER an in-session SavePolicy(forbid) + Reload — " +
			"the engine SavePolicy mutated is not the engine the memory gate reads (the hoist is not wired)")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "polic") {
		t.Fatalf("err = %v; want a policy denial", err)
	}

	// 6. The reverse direction: delete the policy + reload -> the write
	// succeeds again, still in the same process.
	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_wp05_forbid_memwrite.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies after delete: %v", err)
	}
	if err := store.Add(ctx, memoryChunk()); err != nil {
		t.Fatalf("memory write refused AFTER DeletePolicy + Reload: %v — "+
			"the reverse direction of the hoist is not wired", err)
	}
}

// ---------------------------------------------------------------------------
// Two more of the thirteen sites, so "one site happened to share" cannot
// pass. Chosen because both are exercised through a public API method
// (no subprocess / no npx dependency), and both are DISTINCT call sites
// from the memory gate: the workflows Cedar config (an inline site in
// New()) and the scheduled-chat Cedar config (a different inline site).
// ---------------------------------------------------------------------------

const wp05HoistWorkflowYAML = `
id: zz-wp05-hoist-wf
name: "WP05 hoist probe"
version: 1
steps:
  - name: run_it
    kind: shell
    cmd: "echo hello"
`

// TestCedarHoist_InSessionSaveReachesWorkflowsGate repeats the headline
// scenario at the workflows Cedar site.
//
// Mutation: revert `Cedar: a.cedarGate()` in the workflowsview.Config
// literal back to `Cedar: buildCedarGate(coreDataDir(c))` — the refusal
// below must fail.
func TestCedarHoist_InSessionSaveReachesWorkflowsGate(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	// Boot state: permitted.
	if _, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wp05HoistWorkflowYAML}); err != nil {
		t.Fatalf("workflow save refused on a default install (pre-edit): %v", err)
	}

	result, err := api.CedarPolicy().SavePolicy(ctx, "zz_wp05_forbid_wfsave.cedar", forbidPolicy(cedar.ActionWorkflowSave))
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("SavePolicy parse failed: %+v", result.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	_, err = api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wp05HoistWorkflowYAML})
	if err == nil {
		t.Fatal("workflow save succeeded AFTER an in-session SavePolicy(forbid) + Reload — the hoist is not wired at the workflows site")
	}
	if !errors.Is(err, workflowsview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}

	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_wp05_forbid_wfsave.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies after delete: %v", err)
	}
	if _, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wp05HoistWorkflowYAML}); err != nil {
		t.Fatalf("workflow save refused AFTER DeletePolicy + Reload: %v — the reverse direction is not wired", err)
	}
}

// TestCedarHoist_InSessionSaveReachesScheduledChatGate repeats the
// headline scenario at the scheduled-chat Cedar site — a third,
// independent call site from either of the two above.
//
// Mutation: revert `Cedar: a.cedarGate()` in the scheduledchatview.Config
// literal back to `Cedar: buildCedarGate(coreDataDir(c))` — the refusal
// below must fail.
func TestCedarHoist_InSessionSaveReachesScheduledChatGate(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	create := func(name string) scheduledchatview.CreateInput {
		return scheduledchatview.CreateInput{
			Name:           name,
			PromptTemplate: "summarise my day",
			Cron:           "0 7 * * *",
			Timezone:       "UTC",
			Enabled:        true,
		}
	}

	if _, err := api.ScheduledChat().Create(ctx, create("zz-wp05-hoist-pre")); err != nil {
		t.Fatalf("scheduled chat create refused on a default install (pre-edit): %v", err)
	}

	result, err := api.CedarPolicy().SavePolicy(ctx, "zz_wp05_forbid_schedcreate.cedar", forbidPolicy(cedar.ActionScheduledRunCreate))
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if !result.OK {
		t.Fatalf("SavePolicy parse failed: %+v", result.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	_, err = api.ScheduledChat().Create(ctx, create("zz-wp05-hoist-during"))
	if err == nil {
		t.Fatal("scheduled chat create succeeded AFTER an in-session SavePolicy(forbid) + Reload — the hoist is not wired at the scheduled-chat site")
	}
	if !errors.Is(err, scheduledchatview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}

	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_wp05_forbid_schedcreate.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies after delete: %v", err)
	}
	if _, err := api.ScheduledChat().Create(ctx, create("zz-wp05-hoist-post")); err != nil {
		t.Fatalf("scheduled chat create refused AFTER DeletePolicy + Reload: %v — the reverse direction is not wired", err)
	}
}

// ---------------------------------------------------------------------------
// The decision ring must actually be fed (spec §1.1 / design constraint).
// ---------------------------------------------------------------------------

// TestCedarHoist_DecisionRingIsFedByALiveGate proves e.decisions.Append
// fires on the SAME instance CedarPolicy().RecentDecisions reads —
// i.e. the ring the WP06 panel reads is fed by something, which per the
// spec's §1.1 finding it structurally could not be before this hoist
// (the cedarpolicy view held its own private engine that no gate ever
// called Evaluate on).
//
// Mutation: give the cedarpolicy view its own private engine again
// (revert the `if eng := a.cedarEngine; eng != nil` block in New() back
// to `if eng := buildCedarEngineOrNil(cedarDataDir); eng != nil`) — the
// deny below still happens (the memory gate still has ITS OWN correct
// engine), but RecentDecisions returns nothing for it, and this test
// must fail.
func TestCedarHoist_DecisionRingIsFedByALiveGate(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionMemoryWrite))
	ctx := context.Background()

	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef is nil — construction changed")
	}

	// Evaluate a REAL deny through the REAL memory-write gate — not the
	// cedarpolicy view's own API, which has no Evaluate method to call.
	if err := store.Add(ctx, memoryChunk()); err == nil {
		t.Fatal("memory write succeeded under a forbid policy — cannot prove the ring is fed by a denial that never happened")
	}

	decisions, err := api.CedarPolicy().RecentDecisions(ctx, 50)
	if err != nil {
		t.Fatalf("RecentDecisions: %v", err)
	}
	sawMemoryDeny := false
	for _, d := range decisions {
		if d.Action == cedar.ActionMemoryWrite && d.Outcome == cedar.Deny {
			sawMemoryDeny = true
		}
	}
	if !sawMemoryDeny {
		t.Fatalf("RecentDecisions does not contain the memory_write deny just evaluated: %+v — "+
			"the ring the policy panel reads is fed by a DIFFERENT engine than the one gates evaluate against", decisions)
	}
}

// ---------------------------------------------------------------------------
// Fail-open must survive the hoist: a corrupt policy file present at boot
// must not take the SHARED engine to fail-closed for every one of the
// thirteen sites it now serves.
// ---------------------------------------------------------------------------

// TestCedarHoist_CorruptPolicyAtBoot_SharedEngineStaysFailOpen repeats
// TestBuildCedarGate_PostureIsFailOpen's assertion (which calls
// buildCedarGate directly, in isolation) through the ACTUAL hoisted path:
// boot rpc.New with a broken .cedar file already on disk, and assert the
// process-shared a.cedarEngine still permits — proving the fail-open
// contract holds for the ONE instance every gate site now shares, not
// merely for buildCedarGate called standalone.
//
// Mutation: change Engine.Reload's "every source failed to parse; keep
// the prior PolicySet" branch (core/policy/cedar/engine.go) to install an
// empty PolicySet instead of returning early — with DefaultDeny=false an
// empty PolicySet is still permissive here, so this specific mutation
// would not be caught by this particular assertion; the mutation that
// DOES kill it is deleting the early return itself (i.e. letting an
// all-failed reload continue on to `e.policies.Store(ps)` with an
// all-empty `ps`), OR any change that makes a parse failure propagate to
// DefaultDeny=true. Primarily this test guards that WIRING a shared
// engine did not accidentally change WHICH engine.go code path runs.
func TestCedarHoist_CorruptPolicyAtBoot_SharedEngineStaysFailOpen(t *testing.T) {
	api := cedarWiringAPI(t, "this is not cedar {{{")
	if api.cedarEngine == nil {
		t.Fatal("cedarEngine is nil for a real DataDir — construction failed unexpectedly")
	}

	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef is nil — construction changed")
	}
	if err := store.Add(context.Background(), memoryChunk()); err != nil {
		t.Fatalf("a broken policy file on disk failed the SHARED engine CLOSED: %v — "+
			"the hoist must not change the fail-open contract every gate site relies on", err)
	}

	// ListPolicies must report the parse failure (visible, not silent) —
	// distinguishing "fails open AND tells you" from "fails open and
	// hides the typo".
	files, err := api.CedarPolicy().ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	sawBrokenFile := false
	for _, f := range files {
		if f.Name == "zz_test_policy.cedar" && !f.ParseOK {
			sawBrokenFile = true
		}
	}
	if !sawBrokenFile {
		t.Fatalf("ListPolicies does not report the broken policy file's parse failure: %+v", files)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: Reload must never deadlock or race against Evaluate on a
// live request path. Load-bearing under `go test -race`.
// ---------------------------------------------------------------------------

// TestCedarHoist_ConcurrentReloadDuringEvaluate_NoRace hammers the shared
// engine with concurrent Evaluate calls (the chat / gate hot path) while
// concurrently calling ReloadPolicies (what SavePolicy/DeletePolicy do on
// every edit). This exercises the SAME engine every gate site now shares
// — a single object now serves every one of the thirteen call sites, so
// a torn read here would corrupt every one of them at once.
//
// Mutation: this test's value is the `-race` build tag catching a data
// race, not a value assertion. Engine.Reload publishes a rebuilt
// PolicySet through `policies atomic.Pointer[cedar.PolicySet]`
// (core/policy/cedar/engine.go) specifically so Evaluate's hot-path read
// never torn-reads a write in progress. Replacing that field with a
// plain `*cedar.PolicySet` (Store/Load calls becoming direct field
// assignment/read) reproduces the exact shape of bug this test exists to
// catch: `go test -race` on this test then reports a genuine WARNING:
// DATA RACE between Engine.Evaluate's read (hooks.go's
// CheckMemoryWrite -> cedar-go's Authorize) and Engine.Reload's write
// (cedar.NewPolicySetFromBytes), verified by planting that exact
// mutation and confirming the failure (consent-surfaces-truth-01PMTR01
// WP05). reloadMu alone (serialising concurrent Reloads against each
// other) does NOT reproduce a race here — filesMu and the atomic
// Pointer are what the hot path actually depends on; reloadMu is a
// correctness/ordering guard for overlapping Reloads, not the memory
// safety boundary Evaluate depends on.
func TestCedarHoist_ConcurrentReloadDuringEvaluate_NoRace(t *testing.T) {
	api := cedarWiringAPI(t, "")
	if api.cedarEngine == nil {
		t.Fatal("cedarEngine is nil — cannot exercise concurrent reload")
	}
	gate := api.cedarGate()

	const evaluators = 8
	const reloaders = 4
	const iterations = 150

	var wg sync.WaitGroup
	ctx := context.Background()

	for i := 0; i < evaluators; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = cedar.CheckMemoryWrite(ctx, gate, "global")
			}
		}()
	}
	for i := 0; i < reloaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations/5; j++ {
				if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
					t.Errorf("ReloadPolicies: %v", err)
				}
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent Evaluate/Reload deadlocked — a Reload must never block a live request path")
	}
}
