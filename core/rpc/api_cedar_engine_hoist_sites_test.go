package rpc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// WP05 hoist — SITE COVERAGE + POSTURE tests
// (consent-surfaces-truth-01PMTR01, added during adversarial review).
//
// api_cedar_engine_hoist_test.go pins the headline plus three sites
// (memory, workflows, scheduled chat). Reverting the SESSION-EXPORT site
// to its own buildCedarGate left that whole file — and every other test
// in ./core/... — green, so the shipped suite is blind to nine of the
// thirteen sites. These add the two further sites that can be driven
// in-process, the over-blocking matrix through the SHARED engine (rather
// than through a standalone buildCedarGate, which is what
// TestCedarWiring_DefaultInstall_PermitsEveryGatedAction exercises), the
// nil-safety of the single accessor all thirteen sites now funnel
// through, and a harder concurrency storm than the shipped one.
//
// ---------------------------------------------------------------------------
// The headline acceptance again, narrated, so a failure shows the whole
// sequence rather than only the assertion that broke.
// ---------------------------------------------------------------------------

func TestCedarHoist_HeadlineAcceptance_Narrated(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef nil")
	}

	t.Logf("STEP 1 boot: cedarEngine=%p (nil=%v)", api.cedarEngine, api.cedarEngine == nil)

	err := store.Add(ctx, memoryChunk())
	t.Logf("STEP 2 memory write on default install -> err=%v", err)
	if err != nil {
		t.Fatalf("default install refused a memory write: %v", err)
	}

	res, err := api.CedarPolicy().SavePolicy(ctx, "zz_hoistsite_forbid.cedar", forbidPolicy(cedar.ActionMemoryWrite))
	t.Logf("STEP 3 SavePolicy(forbid memory_write) -> ok=%v errs=%+v err=%v", res.OK, res.Errors, err)
	if err != nil || !res.OK {
		t.Fatalf("SavePolicy: ok=%v err=%v", res.OK, err)
	}

	rerr := api.CedarPolicy().ReloadPolicies(ctx)
	t.Logf("STEP 4 ReloadPolicies -> %v", rerr)
	if rerr != nil {
		t.Fatalf("ReloadPolicies: %v", rerr)
	}

	files, lerr := api.CedarPolicy().ListPolicies(ctx)
	if lerr != nil {
		t.Fatalf("ListPolicies: %v", lerr)
	}
	loaded := false
	for _, f := range files {
		if f.Name == "zz_hoistsite_forbid.cedar" {
			loaded = f.ParseOK
			t.Logf("STEP 5 ListPolicies: name=%q parse_ok=%v bytes=%d embedded=%v", f.Name, f.ParseOK, f.Bytes, f.Embedded)
		}
	}
	if !loaded {
		t.Fatalf("policy not reported loaded: %+v", files)
	}

	err = store.Add(ctx, memoryChunk())
	t.Logf("STEP 6 SAME memory write after in-session save+reload -> err=%v", err)
	if err == nil {
		t.Fatal("REGRESSION: write still permitted after forbid + reload")
	}

	decs, _ := api.CedarPolicy().RecentDecisions(ctx, 5)
	for i, d := range decs {
		t.Logf("STEP 6b RecentDecisions[%d]: outcome=%v action=%q resource=%q matched=%q reason=%q",
			i, d.Outcome, d.Action, d.Resource, d.MatchedPolicy, d.Reason)
	}

	if derr := api.CedarPolicy().DeletePolicy(ctx, "zz_hoistsite_forbid.cedar"); derr != nil {
		t.Fatalf("DeletePolicy: %v", derr)
	}
	if rerr := api.CedarPolicy().ReloadPolicies(ctx); rerr != nil {
		t.Fatalf("reload after delete: %v", rerr)
	}
	err = store.Add(ctx, memoryChunk())
	t.Logf("STEP 7 memory write after DeletePolicy+Reload -> err=%v", err)
	if err != nil {
		t.Fatalf("REGRESSION: write refused after policy deleted: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Two further sites of the thirteen: session.export
// (sessions.WithExportOpts) and audit.bulk_purge (audit.WithGate).
//
// Mutation (verified): revert `sessions.WithExportOpts(a.sessionsAPI,
// a.cedarGate(), nil, nil)` to `buildCedarGate(c.DataDir())` — the
// export test below is the ONLY test in the repo that fails.
// ---------------------------------------------------------------------------

func TestCedarHoist_InSessionSaveReachesSessionExportGate(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	// Default install: the gate permits, so Export gets past step 1 and
	// fails on "load session" for a session that does not exist.
	_, err := api.Sessions().Export(ctx, "zz-no-such-session", "markdown")
	t.Logf("pre-edit Export err=%v", err)
	if err == nil {
		t.Fatal("expected a load failure for a nonexistent session")
	}
	if strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("session export DENIED on a default install: %v", err)
	}
	if !strings.Contains(err.Error(), "load session") {
		t.Fatalf("unexpected pre-edit error shape: %v", err)
	}

	res, serr := api.CedarPolicy().SavePolicy(ctx, "zz_hoistsite_forbid_export.cedar", forbidPolicy(cedar.ActionExportSession))
	if serr != nil || !res.OK {
		t.Fatalf("SavePolicy: ok=%v err=%v errs=%+v", res.OK, serr, res.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	_, err = api.Sessions().Export(ctx, "zz-no-such-session", "markdown")
	t.Logf("post-edit Export err=%v", err)
	if err == nil || !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("session export NOT refused after in-session forbid+reload: %v — site 3 of 13 does not share the engine", err)
	}

	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_hoistsite_forbid_export.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	_, err = api.Sessions().Export(ctx, "zz-no-such-session", "markdown")
	t.Logf("post-delete Export err=%v", err)
	if err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("still denied after delete+reload: %v", err)
	}
}

// audit.bulk_purge is DEFAULT-FORBID by design (CheckAuditBulkPurge treats
// NotApplicable as Deny), so the reach probe here runs the other way: an
// in-session PERMIT must flip a denied purge to allowed. That is a
// stronger proof of reach at the auditGate site than a forbid would be,
// because the pre-state is already a denial.
func TestCedarHoist_InSessionPermitReachesAuditBulkPurgeGate(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	var pde *cedar.PolicyDeniedError
	err := api.Audit().BulkPurge(ctx, []string{"zz-nope"})
	t.Logf("pre-edit BulkPurge err=%v", err)
	if !errors.As(err, &pde) {
		t.Fatalf("audit bulk_purge is documented default-forbid; pre-state should be a policy denial, got %v", err)
	}

	permit := "permit (\n    principal == User::\"local\",\n    action == Action::\"" +
		cedar.ActionAuditBulkPurge + "\",\n    resource\n);\n"
	res, serr := api.CedarPolicy().SavePolicy(ctx, "zz_hoistsite_permit_purge.cedar", permit)
	if serr != nil || !res.OK {
		t.Fatalf("SavePolicy: ok=%v err=%v errs=%+v", res.OK, serr, res.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	err = api.Audit().BulkPurge(ctx, []string{"zz-nope"})
	t.Logf("post-edit BulkPurge err=%v", err)
	if errors.As(err, &pde) {
		t.Fatalf("audit bulk_purge STILL policy-denied after an in-session permit+reload: %v — the auditGate site does not share the engine", err)
	}
}

// ---------------------------------------------------------------------------
// OVER-BLOCKING. The hoist's failure mode is not only "stops enforcing";
// it is equally "starts denying what worked yesterday". Boot a DEFAULT
// install (no user policy) and assert every gated action any of the
// thirteen sites can ask about is still permitted through the ONE shared
// engine, plus a behavioural permit at every surface drivable in-process.
//
// This is deliberately NOT the same assertion as
// TestCedarWiring_DefaultInstall_PermitsEveryGatedAction, which calls
// buildCedarGate(dir) standalone: that engine is not the object any gate
// site holds post-hoist, so it cannot see a posture change introduced by
// the sharing itself.
// ---------------------------------------------------------------------------

func TestCedarHoist_DefaultInstall_SharedEngineOverBlocksNothing(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	g := api.cedarGate()
	if _, degraded := g.(cedar.AllowAll); degraded {
		t.Fatal("shared gate degraded to AllowAll for a real DataDir — every assertion below would be vacuous")
	}
	if g != cedar.Gate(api.cedarEngine) {
		t.Fatal("cedarGate() does not return the shared engine")
	}

	// --- engine-level: every action any of the 13 sites can ask about ---
	if err := cedar.CheckMemoryWrite(ctx, g, "global"); err != nil {
		t.Errorf("[site: memory write] memory_write denied: %v", err)
	}
	if err := cedar.CheckExportSession(ctx, g, "s1", "markdown"); err != nil {
		t.Errorf("[site: session export] session.export denied: %v", err)
	}
	// audit.bulk_purge is deliberately default-forbid (CheckAuditBulkPurge
	// maps NotApplicable -> Deny). Assert the pre-hoist posture is
	// preserved rather than asserting a permit.
	if err := cedar.CheckAuditBulkPurge(ctx, g); err == nil {
		t.Errorf("[site: audit BulkPurge] audit.bulk_purge is now PERMITTED by default — posture changed")
	}
	if err := cedar.CheckContextBootstrapRun(ctx, g); err != nil {
		t.Errorf("[site: context bootstrap] context.bootstrap.run denied: %v", err)
	}
	if err := cedar.CheckModel(ctx, g, "anthropic", "claude-3"); err != nil {
		t.Errorf("[site: LLM guard] model_select denied: %v", err)
	}
	if err := cedar.CheckLLMFallback(ctx, g, "chain-1"); err != nil {
		t.Errorf("[site: LLM guard] llm.fallback denied: %v", err)
	}
	if err := cedar.CheckRecipeSpawn(ctx, g, "filesystem", "npx", "stdio"); err != nil {
		t.Errorf("[site: tools/mcp bootstrap] spawn_recipe denied: %v", err)
	}
	if err := cedar.CheckRecipeAdd(ctx, g, "filesystem", "npx", "stdio"); err != nil {
		t.Errorf("[site: tools] add_recipe denied: %v", err)
	}
	if err := cedar.CheckTool(ctx, g, "", "websearch"); err != nil {
		t.Errorf("[site: tools] tool_exec denied: %v", err)
	}
	if err := cedar.CheckNetwork(ctx, g, "en.wikipedia.org"); err != nil {
		t.Errorf("[site: tools] network_request denied: %v", err)
	}
	if err := cedar.CheckFileRead(ctx, g, "/tmp/x"); err != nil {
		t.Errorf("[site: bash/fs gate] file_read denied: %v", err)
	}
	if err := cedar.CheckFileWrite(ctx, g, "/tmp/x"); err != nil {
		t.Errorf("[site: bash/fs gate] file_write denied: %v", err)
	}
	if err := cedar.CheckStateRead(ctx, g, "memory"); err != nil {
		t.Errorf("[site: agentgraph policy] state_read denied: %v", err)
	}
	if err := cedar.CheckStateWrite(ctx, g, "memory"); err != nil {
		t.Errorf("[site: agentgraph policy] state_write denied: %v", err)
	}
	if err := cedar.CheckCredentialAccess(ctx, g, "ref-1", "llm", false); err != nil {
		t.Errorf("[site: llm stack] use_credential denied: %v", err)
	}
	for _, mode := range []string{"permissive", ""} {
		if d, err := cedar.GateWorkflowSave(ctx, g, "wf", mode, "model_turn,shell"); err != nil || d.Outcome == cedar.Deny {
			t.Errorf("[site: workflows] workflow.save mode=%q outcome=%v err=%v", mode, d.Outcome, err)
		}
		if d, err := cedar.GateWorkflowRun(ctx, g, "wf", mode, "model_turn,shell"); err != nil || d.Outcome == cedar.Deny {
			t.Errorf("[site: workflows] workflow.run mode=%q outcome=%v err=%v", mode, d.Outcome, err)
		}
	}
	if d, err := cedar.GateWorkflowDelete(ctx, g, "wf"); err != nil || d.Outcome == cedar.Deny {
		t.Errorf("[site: workflows] workflow.delete outcome=%v err=%v", d.Outcome, err)
	}
	for name, call := range map[string]func() (cedar.Decision, error){
		"scheduled_run.create":  func() (cedar.Decision, error) { return cedar.GateScheduledChatCreate(ctx, g, "sc") },
		"scheduled_run.delete":  func() (cedar.Decision, error) { return cedar.GateScheduledChatDelete(ctx, g, "sc") },
		"scheduled_run.execute": func() (cedar.Decision, error) { return cedar.GateScheduledChatExecute(ctx, g, "sc") },
	} {
		d, err := call()
		if err != nil || d.Outcome == cedar.Deny {
			t.Errorf("[site: scheduled chat] %s outcome=%v err=%v", name, d.Outcome, err)
		}
	}
	// ACP send/receive (the acpview.NewEngineAdapter site) — evaluated
	// straight through the shared engine, same as the adapter does.
	for _, action := range []string{cedar.ActionACPSend, cedar.ActionACPReceive} {
		d := api.cedarEngine.Evaluate(ctx, cedar.UserUID(), action, cedar.ToolUID("", "acp"), nil)
		if d.Outcome == cedar.Deny {
			t.Errorf("[site: ACP] %s outcome=%v reason=%q", action, d.Outcome, d.Reason)
		}
	}
	if err := cedar.CheckTool(ctx, g, "", "run_bash_command"); err != nil {
		t.Errorf("[site: bash] run_bash_command tool_exec denied: %v", err)
	}

	// --- behavioural: real surfaces still work on a default install ---
	if err := api.memStoreRef.Add(ctx, memoryChunk()); err != nil {
		t.Errorf("[behaviour] memory write denied on default install: %v", err)
	}
	if _, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: wp05HoistWorkflowYAML}); err != nil {
		t.Errorf("[behaviour] workflow save denied on default install: %v", err)
	}
	if _, err := api.ScheduledChat().Create(ctx, scheduledchatview.CreateInput{
		Name: "zz-review", PromptTemplate: "hi", Cron: "0 7 * * *", Timezone: "UTC", Enabled: true,
	}); err != nil {
		t.Errorf("[behaviour] scheduled chat create denied on default install: %v", err)
	}
	if _, err := api.Sessions().Export(ctx, "zz-none", "markdown"); err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Errorf("[behaviour] session export denied on default install: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FAIL-OPEN through the SHARED engine + nil-safety of the single accessor
// every one of the thirteen sites now funnels through. a.cedarGate() is a
// new single point of failure: if it ever returned nil, or a fail-closed
// stand-in, all thirteen change posture at once.
// ---------------------------------------------------------------------------

func TestCedarHoist_CedarGateAccessorIsNilSafe(t *testing.T) {
	// nil API receiver.
	var nilAPI *API
	if _, ok := nilAPI.cedarGate().(cedar.AllowAll); !ok {
		t.Fatalf("(*API)(nil).cedarGate() = %T; want cedar.AllowAll", nilAPI.cedarGate())
	}
	// non-nil API, nil engine.
	empty := &API{}
	if _, ok := empty.cedarGate().(cedar.AllowAll); !ok {
		t.Fatalf("API{}.cedarGate() = %T; want cedar.AllowAll", empty.cedarGate())
	}
	// AllowAll must actually permit, not merely be non-nil.
	if err := cedar.CheckMemoryWrite(context.Background(), empty.cedarGate(), "global"); err != nil {
		t.Fatalf("AllowAll fallback denied a memory write: %v", err)
	}
}

func TestCedarHoist_EmptyDataDir_NoEngine_EverythingPermits(t *testing.T) {
	sandboxUserConfigDir(t)
	api := New(nil) // nil core => empty DataDir
	if api.cedarEngine != nil {
		t.Fatalf("cedarEngine = %p for a nil Core; want nil", api.cedarEngine)
	}
	if _, ok := api.cedarGate().(cedar.AllowAll); !ok {
		t.Fatalf("cedarGate() = %T for a nil Core; want cedar.AllowAll", api.cedarGate())
	}
	ctx := context.Background()
	for name, err := range map[string]error{
		"memory_write":   cedar.CheckMemoryWrite(ctx, api.cedarGate(), "global"),
		"session.export": cedar.CheckExportSession(ctx, api.cedarGate(), "s", "md"),
		"model_select":   cedar.CheckModel(ctx, api.cedarGate(), "p", "m"),
		"spawn_recipe":   cedar.CheckRecipeSpawn(ctx, api.cedarGate(), "r", "npx", "stdio"),
		"file_write":     cedar.CheckFileWrite(ctx, api.cedarGate(), "/tmp/x"),
		"bootstrap.run":  cedar.CheckContextBootstrapRun(ctx, api.cedarGate()),
	} {
		if err != nil {
			t.Errorf("empty DataDir denied %s: %v — fail-open broken", name, err)
		}
	}
}

func TestCedarHoist_CorruptPolicyMidSession_StaysFailOpen(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	ctx := context.Background()

	// Install a working forbid, reload: the write is refused.
	res, err := api.CedarPolicy().SavePolicy(ctx, "zz_hoistsite_prior.cedar", forbidPolicy(cedar.ActionMemoryWrite))
	if err != nil || !res.OK {
		t.Fatalf("SavePolicy: %v ok=%v", err, res.OK)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if err := api.memStoreRef.Add(ctx, memoryChunk()); err == nil {
		t.Fatal("write permitted under the forbid policy")
	}

	// Now corrupt the on-disk source and reload. The embedded bundle
	// still parses, so anyOK is true and the corrupt file's rules simply
	// vanish -> permitted again, but the app is not bricked.
	writeRawPolicy(t, dataDir, "zz_hoistsite_prior.cedar", "this is not cedar {{{")
	rerr := api.CedarPolicy().ReloadPolicies(ctx)
	t.Logf("reload with a corrupt on-disk policy -> %v", rerr)

	// Fail-open: the corrupt file must not brick the engine.
	if err := api.memStoreRef.Add(ctx, memoryChunk()); err != nil {
		t.Fatalf("a corrupt policy file failed the SHARED engine closed: %v", err)
	}
	files, _ := api.CedarPolicy().ListPolicies(ctx)
	sawBroken := false
	for _, f := range files {
		if f.Name == "zz_hoistsite_prior.cedar" && !f.ParseOK {
			sawBroken = true
			t.Logf("ListPolicies reports the parse failure: %s -> %s", f.Name, f.ParseErr)
		}
	}
	if !sawBroken {
		t.Fatalf("the corrupt file's parse failure is not visible in ListPolicies: %+v", files)
	}
}

// ---------------------------------------------------------------------------
// CONCURRENCY, harder than the shipped test: evaluations across five gate
// surfaces, six concurrent Reloads, in-flight request paths, concurrent
// RecentDecisions readers, and SavePolicy/DeletePolicy churn (each of
// which reloads internally) all at once.
//
// Mutation (verified): replace Engine.policies' atomic.Pointer with a
// plain field — `go test -race` reports 562 DATA RACEs between
// Engine.Reload's write and Engine.Evaluate's read via CheckMemoryWrite,
// and both this test and the shipped one fail.
// ---------------------------------------------------------------------------

func TestCedarHoist_ConcurrentReloadSaveDeleteAndRingRead_NoRace(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	g := api.cedarGate()
	if api.cedarEngine == nil {
		t.Fatal("no engine")
	}

	const iters = 400
	var wg sync.WaitGroup

	// 16 evaluators across several distinct gate surfaces (the "many
	// concurrent evaluations" leg).
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iters; j++ {
				switch (i + j) % 5 {
				case 0:
					_ = cedar.CheckMemoryWrite(ctx, g, "global")
				case 1:
					_, _ = cedar.GateWorkflowSave(ctx, g, "wf", "permissive", "shell")
				case 2:
					_ = cedar.CheckExportSession(ctx, g, "s", "md")
				case 3:
					_ = cedar.CheckRecipeSpawn(ctx, g, "r", "npx", "stdio")
				case 4:
					_ = cedar.CheckAuditBulkPurge(ctx, g)
				}
			}
		}(i)
	}

	// 6 concurrent reloaders (two-or-more concurrent Reload leg).
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters/4; j++ {
				if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
					t.Errorf("ReloadPolicies: %v", err)
				}
			}
		}()
	}

	// 4 in-flight "tool dispatch" style paths that go through a real
	// surface while reloads land underneath them.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters/8; j++ {
				_, _ = api.Sessions().Export(ctx, "zz-none", "markdown")
				_ = api.Audit().BulkPurge(ctx, []string{"zz"})
			}
		}()
	}

	// 4 readers of the decision ring while everything above churns.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iters/2; j++ {
				if _, err := api.CedarPolicy().RecentDecisions(ctx, 50); err != nil {
					t.Errorf("RecentDecisions: %v", err)
				}
			}
		}()
	}

	// 2 policy mutators: SavePolicy/DeletePolicy (each internally reloads)
	// racing everything else.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "zz_hoistsite_churn_" + string(rune('a'+i)) + ".cedar"
			for j := 0; j < iters/20; j++ {
				if _, err := api.CedarPolicy().SavePolicy(ctx, name, forbidPolicy(cedar.ActionMemoryWrite)); err != nil {
					t.Errorf("SavePolicy: %v", err)
				}
				if err := api.CedarPolicy().DeletePolicy(ctx, name); err != nil {
					t.Errorf("DeletePolicy: %v", err)
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("DEADLOCK: a reload blocked a live request path")
	}

	// The engine must still be usable and still fail-open afterwards.
	if err := cedar.CheckMemoryWrite(ctx, g, "global"); err != nil {
		t.Fatalf("engine poisoned after the concurrency storm: %v", err)
	}
}

// TestCedarHoist_FailedReloadLeavesEngineUsable drives a reload that fails
// (every on-disk source unparseable AND embedded disabled is impossible
// through the API, so drive the failure the API can produce: a corrupt
// file) and asserts the engine still answers afterwards.
func TestCedarHoist_FailedReloadLeavesEngineUsable(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	ctx := context.Background()
	writeRawPolicy(t, dataDir, "zz_broken.cedar", "{{{ not cedar")
	for i := 0; i < 5; i++ {
		_ = api.CedarPolicy().ReloadPolicies(ctx)
	}
	if err := cedar.CheckMemoryWrite(ctx, api.cedarGate(), "global"); err != nil {
		t.Fatalf("engine poisoned by repeated failing reloads: %v", err)
	}
	if _, err := api.CedarPolicy().ListPolicies(ctx); err != nil {
		t.Fatalf("ListPolicies after failing reloads: %v", err)
	}
	if _, err := api.CedarPolicy().RecentDecisions(ctx, 10); err != nil {
		t.Fatalf("RecentDecisions after failing reloads: %v", err)
	}
}

// hoistSiteAPI boots a real Core + API over a fresh temp DataDir and returns
// both, so a probe can write straight into <DataDir>/policy/.
func hoistSiteAPI(t *testing.T) (*API, string) {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	return api, dataDir
}

func writeRawPolicy(t *testing.T, dataDir, name, src string) {
	t.Helper()
	polDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(polDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(polDir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
