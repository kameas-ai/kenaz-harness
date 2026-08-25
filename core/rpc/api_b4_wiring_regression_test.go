package rpc

// B4 (unwired sweep, release/v0.72.0): behavioural regression protection
// for two of the three production wiring fields PR #308 shipped with zero
// real coverage.
//
// PR #308's own regression suites drove these fields by CONSTRUCTING the
// dependency themselves, never through core/rpc/api.go's real wiring:
//   - core/rpc/views/permissions/impl_test.go's newRealEngineAPI calls
//     permissions.New(Config{Engine: eng}) directly.
//   - Nothing drove contextsyncview.Impl.Gate at all.
// Both are the exact "test-side edit standing in for a production revert"
// shape CLAUDE.md's unwired-sweep doctrine warns is a false equivalence —
// deleting the one-line assignment at api.go left every prior test green.
//
// These tests drive the REAL rpc.New(c) construction path (hoistSiteAPI,
// shared with the WP05 hoist-site tests in
// api_cedar_engine_hoist_sites_test.go) and assert an OBSERVABLE
// behavioural effect, not merely that a field is non-nil — a static
// "field is assigned something" check (see check-cedar-gate-arguments.sh
// clause 5) cannot tell a correctly-wired collaborator from one wired to
// the wrong object; these can.
//
// The third field (chat.Config.SecretLookup, wired inside buildChatRunner)
// was initially judged (see check-secret-lookup-wiring.sh's header, and
// this file's own history) too costly to cover behaviourally: driving a
// real tool-call round trip through buildChatRunner appeared to need a
// scripted tool-calling LLM registry that exists only inside the
// unexported chat package. TestB4_SecretLookupWiring_ChatRunnerResolves
// RealSecret below closes that gap without reconstructing chat package
// internals: it calls newLLMStack directly (the real production
// function, reachable because this file lives in package rpc), so
// buildChatRunner's real chat.Config{} literal is what gets exercised,
// and it scripts the model side by registering the REAL
// core/llm/anthropic adapter against an httptest fixture (the same seam
// cost_reducer_wiring_test.go's TestNewLLMStack_CostReducer_DerivesReal
// Cost already uses for the Cost-reducer field) rather than inventing a
// synthetic corellm.ProviderAdapter — capabilities.Catalog.Describe
// keys off ProviderProfile.Kind, and an unrecognised Kind gets a
// streaming-only baseline that fails the CapToolCalling check before a
// fake adapter's Stream method would ever run. The tool side is the
// REAL core/tools/bash.Tool newLLMStack's own registerBuiltinTools call
// wires in — not a stand-in — so this proves @secret: resolution
// through the actual refs.ResolverFromContext guard bash.go checks in
// production, at the actual call site the static
// check-secret-lookup-wiring.sh gate cannot see past (it only sees that
// SOME non-nil value was assigned, never whether that value still does
// anything by the time driveRun runs — see Finding 4, 2026-08-25).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/anthropic"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// TestB4_PermissionsEngineWiring_RevokeGrantReachesSharedEngine is the
// api.go-level counterpart to permissions/impl_test.go's
// TestRevokeGrant_ChangesEvaluateOutcome_FiveFamilies. That test proves
// permissions.API.RevokeGrant behaves correctly once handed a real
// engine; this one proves rpc.New actually HANDS it one.
//
// Falsification (verified by hand while writing this test): delete
// `Engine: permissionsEngine,` from api.go's permissionsview.Config{}
// literal (or revert to the pre-M1 `Engine: a.cedarEngine,` form with no
// nil guard — either way the Config's Engine ends up unset for this
// test's purposes) and this test fails: RevokeGrant deletes the .cedar
// grant file but the engine never reloads, so the SAME shared engine
// a.cedarGate() vends everywhere else keeps evaluating the deleted grant
// as Allow.
func TestB4_PermissionsEngineWiring_RevokeGrantReachesSharedEngine(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	ctx := context.Background()

	const pattern = "echo zz-b4-permissions-wiring"
	grantID := "bash_allow_zz_b4_permissions_wiring.cedar"
	body := "permit(\n  principal,\n  action == Action::\"run_bash_command\",\n  resource == BashCommand::\"" + pattern + "\"\n);\n"
	writeRawPolicy(t, dataDir, grantID, body)
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("initial ReloadPolicies: %v", err)
	}

	resource := cedar.BashCommandUID(pattern)
	before := api.cedarEngine.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, resource, nil)
	if before.Outcome != cedar.Allow {
		t.Fatalf("setup broken: pre-revoke Evaluate = %v, want Allow: %+v", before.Outcome, before)
	}

	if err := api.Permissions().RevokeGrant(ctx, grantID); err != nil {
		t.Fatalf("Permissions().RevokeGrant: %v", err)
	}

	after := api.cedarEngine.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, resource, nil)
	if after.Outcome == cedar.Allow {
		t.Fatalf("REGRESSION: revoked grant still permits the same call through the shared engine — "+
			"api.go's permissionsview.Config.Engine is not reaching a.cedarEngine (B4): %+v", after)
	}
}

// TestB4_ContextSyncGateWiring_ReachesSharedEngine is the api.go-level
// wiring proof for contextsyncview.Impl.Gate. No prior test in the repo
// drove this field at all.
//
// CheckContextSyncSessionPurge (core/policy/cedar/hooks.go) treats a
// literal nil Gate as an IMMEDIATE ALLOW ("g == nil { return nil }") —
// unlike most gate-hook helpers, which degrade a nil-through-AllowAll to
// a documented default-allow-but-still-evaluated posture. So the mutation
// this test is falsified by is not "starts denying everything" but the
// opposite and more dangerous failure: reverting `Gate: a.cedarGate(),`
// to an omitted field makes SessionSync_DeleteRemote bypass policy
// evaluation entirely — an operator-authored forbid rule against
// context_sync.session.purge would silently stop applying.
//
// Falsification (verified by hand): delete `Gate: a.cedarGate(),` from
// api.go's contextsyncview.Impl{} literal — the forbid-policy step below
// then fails to deny, because im.Gate is a true nil interface and
// CheckContextSyncSessionPurge never evaluates anything.
func TestB4_ContextSyncGateWiring_ReachesSharedEngine(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()
	cs := api.ContextSync()
	if cs == nil {
		t.Fatal("api.ContextSync() is nil — contextSyncAPI was not constructed")
	}

	// Default install: the shipped default_context_sync_policy.cedar
	// permit clears the gate, so the call proceeds past policy and fails
	// on the fleet layer instead (no fleet client configured in this test
	// chassis) — never "denied by policy".
	err := cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	t.Logf("pre-forbid SessionSync_DeleteRemote err=%v", err)
	if err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("context sync purge DENIED on a default install — the shipped default permit is not reaching the gate: %v", err)
	}

	res, serr := api.CedarPolicy().SavePolicy(ctx, "zz_b4_forbid_context_sync_purge.cedar", forbidPolicy(cedar.ActionContextSyncSessionPurge))
	if serr != nil || !res.OK {
		t.Fatalf("SavePolicy: ok=%v err=%v errs=%+v", res.OK, serr, res.Errors)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	err = cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	t.Logf("post-forbid SessionSync_DeleteRemote err=%v", err)
	if err == nil || !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("REGRESSION: context sync purge NOT refused after an in-session forbid+reload — "+
			"api.go's contextsyncview.Impl.Gate is not reaching the shared engine (B4): %v", err)
	}

	if err := api.CedarPolicy().DeletePolicy(ctx, "zz_b4_forbid_context_sync_purge.cedar"); err != nil {
		t.Fatalf("DeletePolicy: %v", err)
	}
	if err := api.CedarPolicy().ReloadPolicies(ctx); err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	err = cs.SessionSync_DeleteRemote(ctx, "zz-b4-no-such-session")
	if err != nil && strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("still denied after delete+reload: %v", err)
	}
}

// TestB4_SecretLookupWiring_ChatRunnerResolvesRealSecret is the
// api.go-level behavioural proof for chat.Config.SecretLookup, wired
// inside buildChatRunner (core/rpc/api.go's newLLMStack). It drives
// newLLMStack directly — the real production function this test file's
// package can reach — so a real *chat.ChatRunner comes back wired
// against the real registerBuiltinTools-installed core/tools/bash.Tool,
// exactly as rpc.New(c) wires it at boot.
//
// The command's Cedar gate pattern is pre-granted via a raw policy file
// (mirroring TestB4_PermissionsEngineWiring_RevokeGrantReachesShared
// Engine above) because default_bash_policy.cedar is deliberately
// NotApplicable for every pattern — the universal-prompt flow owns that
// case in the real app, and this test isn't exercising it.
//
// The assertion never looks for the plaintext secret itself: bash.go
// sanitizes its own stdout/stderr through the SAME
// refs.SanitizerFromContext driveRun installs alongside the resolver,
// so a correctly-wired run would redact it out of the persisted
// tool_result anyway. Instead the scripted command performs the
// comparison INSIDE the shell (`[ "@secret:<locator>" = "<sentinel>" ]`)
// and echoes one of two sentinel-free markers — ZZ_B4_MATCH_OK only
// appears in the transcript if the substitution actually happened
// before the shell evaluated the test.
//
// Falsification (verified by hand while writing this test, mirroring
// Finding 4's own planted mutation): appending `secretLookup = nil`
// immediately after newLLMStack's `if exposureIdx != nil { secretLookup
// = exposureIdx }` guard (api.go ~:5197) leaves `SecretLookup:
// secretLookup,` textually present at the buildChatRunner call site —
// check-secret-lookup-wiring.sh stays clean — but this test fails with
// ZZ_B4_MATCH_FAIL: the shell sees the literal, un-substituted
// "@secret:user:zz-b4-secretlookup" token.
func TestB4_SecretLookupWiring_ChatRunnerResolvesRealSecret(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	const locator = "user:zz-b4-secretlookup"
	const sentinel = "ZZ_B4_SECRETLOOKUP_SENTINEL_0x7c1"
	const cmd = `[ "@secret:` + locator + `" = "` + sentinel + `" ] && echo ZZ_B4_MATCH_OK || echo ZZ_B4_MATCH_FAIL`

	// Pre-grant the exact BashCommand pattern this command derives to
	// (DerivePattern/FirstSegmentArgv — the same functions bash.go's own
	// cedarGate calls), so the run proceeds synchronously instead of
	// falling to the (here-unconfigured) universal confirm-each prompt.
	pattern := corebash.DerivePattern(corebash.FirstSegmentArgv(cmd))
	polDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(polDir, 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	grantBody := "permit(\n  principal,\n  action == Action::\"run_bash_command\",\n  resource == BashCommand::\"" + pattern + "\"\n);\n"
	if err := os.WriteFile(filepath.Join(polDir, "zz_b4_secretlookup_allow.cedar"), []byte(grantBody), 0o644); err != nil {
		t.Fatalf("write grant policy: %v", err)
	}

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	cedarEngine := buildCedarEngineOrNil(dataDir)
	if cedarEngine == nil {
		t.Fatal("buildCedarEngineOrNil returned nil over a real DataDir — cannot prove the gate/grant interaction")
	}

	exposureIdx := secrets.NewExposureIndex()
	pt := []byte(sentinel)
	exposureIdx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)

	bashStore := corebash.NewStore()
	// newGraphManagerWithDeps (not the bare graphview.NewManager()) —
	// buildChatRunner's envDefaults closure (api.go's newLLMStack)
	// unconditionally does `env.Hooks = coreag.NewHookManager(env.Memory,
	// ...)`, and HookManager.Fire calls mem.Write with no nil-guard. A
	// graphMgr with no EnvDeps.Memory (what the bare constructor leaves)
	// panics the very first "ask" node hook fire — openMemoryStore(c) is
	// the real production memory-store constructor (a real chromem-go
	// store over this test's temp DataDir), matching how newLLMStack's
	// real caller in New() never hands it a memStore-less graphMgr either.
	memStore := openMemoryStore(c)
	if memStore == nil {
		t.Fatal("openMemoryStore returned nil over a real DataDir")
	}
	graphMgr, _, _ := newGraphManagerWithDeps(c, nil, nil, memStore, nil, bashStore, nil, cedarEngine, nil, nil)
	if graphMgr == nil {
		t.Fatal("newGraphManagerWithDeps returned a nil manager")
	}
	broker := NewStreamBroker(NewMultiEmitter())
	store := newPersonalStore(c)

	// The scripted transport: request 1 returns a tool_use call to the
	// real kenaz__bash tool with the command above; request 2 (issued
	// after the tool result is folded back in) returns a plain "done"
	// text turn so the kernel run terminates. This is the REAL
	// core/llm/anthropic adapter (see cost_reducer_wiring_test.go's
	// TestNewLLMStack_CostReducer_DerivesRealCost for the same pattern
	// against a different B4-adjacent field) pointed at an httptest
	// fixture via ProviderProfile.Endpoint / anthropic.WithEndpoint —
	// not a hand-rolled corellm.ProviderAdapter, which would carry an
	// unrecognised Kind and fail capabilities.Gate's CapToolCalling
	// check before ever reaching Stream.
	var reqN int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&reqN, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		var frames []string
		if n == 1 {
			argsJSON, _ := json.Marshal(struct {
				Command string `json:"command"`
			}{Command: cmd})
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_1","role":"assistant","model":"zz-b4-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_1","name":"` + corebash.Name + `","input":{}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + strconv.Quote(string(argsJSON)) + `}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		} else {
			frames = []string{
				`{"type":"message_start","message":{"id":"msg_2","role":"assistant","model":"zz-b4-model","usage":{"input_tokens":1,"output_tokens":1}}}`,
				`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`,
				`{"type":"content_block_stop","index":0}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":1,"output_tokens":1}}`,
				`{"type":"message_stop"}`,
			}
		}
		for _, f := range frames {
			fmt.Fprintf(w, "data: %s\n\n", f)
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	stack := newLLMStack(c, broker, store, nil, nil, func() bool { return false },
		nil, nil, nil, bashStore, nil, graphMgr, nil, nil, nil, nil,
		exposureIdx, nil, nil, nil, confirmAuditEmitter{}, cedarEngine, nil)

	if stack.chatRunner == nil {
		t.Fatal("newLLMStack produced no chatRunner — cannot drive StartStream")
	}
	if stack.reg == nil {
		t.Fatal("newLLMStack produced no registry")
	}
	stack.reg.RegisterAdapter(anthropic.New(anthropic.WithEndpoint(srv.URL)))

	t.Setenv("ZZ_B4_SECRETLOOKUP_KEY", "unused-test-key")
	prof := corellm.ProviderProfile{
		ID:    "zz-b4-secretlookup-probe",
		Kind:  anthropic.Kind,
		Model: "default",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ZZ_B4_SECRETLOOKUP_KEY"},
	}
	if err := stack.reg.LoadProfiles([]corellm.ProviderProfile{prof}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	ctx := context.Background()
	sessRec, err := c.SessionManager().Create(ctx, "zz-b4-secretlookup")
	if err != nil {
		t.Fatalf("SessionManager().Create: %v", err)
	}
	sessionID := sessRec.ID
	if _, err := stack.chatRunner.StartStream(ctx, prof.ID, sessionID, "", "run it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}

	var content string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		msgs, herr := stack.historyAdapter.ListMessages(ctx, sessionID)
		if herr == nil {
			for _, m := range msgs {
				if strings.Contains(m.Content, "ZZ_B4_MATCH_OK") || strings.Contains(m.Content, "ZZ_B4_MATCH_FAIL") {
					content = m.Content
					break
				}
				for _, cb := range m.ContentBlocks {
					if strings.Contains(string(cb.ToolData), "ZZ_B4_MATCH_OK") || strings.Contains(string(cb.ToolData), "ZZ_B4_MATCH_FAIL") {
						content = string(cb.ToolData)
					}
					if cb.ToolResult != nil && (strings.Contains(string(cb.ToolResult.Content), "ZZ_B4_MATCH_OK") || strings.Contains(string(cb.ToolResult.Content), "ZZ_B4_MATCH_FAIL")) {
						content = string(cb.ToolResult.Content)
					}
				}
			}
		}
		if content != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if content == "" {
		t.Fatalf("no ZZ_B4_MATCH_{OK,FAIL} tool result observed in session %q within 8s — bash tool never ran, "+
			"the Cedar grant did not match, or history was never persisted", sessionID)
	}
	if strings.Contains(content, "ZZ_B4_MATCH_FAIL") {
		t.Fatalf("REGRESSION: bash saw the literal, un-substituted @secret: token — "+
			"newLLMStack's secretLookup is not reaching buildChatRunner's chat.Config.SecretLookup (B4): %q", content)
	}
	if !strings.Contains(content, "ZZ_B4_MATCH_OK") {
		t.Fatalf("tool result contained neither marker as expected: %q", content)
	}
}

// TestB4_BackgroundSanitizerWiring_ResolvedSecretRedactedInTaskLog is the
// api.go-level (well, builtins_wiring.go-level) wiring proof for
// core/rpc/builtins_wiring.go:172's `Sanitizer: sanitizer,` line — the
// production wiring the review-round-3 background-output fix (PR #308)
// shipped alongside zero regression protection of its own.
//
// PR #308's own suite, core/tools/bash/secret_background_output_leak_test.go,
// builds its own bgSpawn closure with its OWN Clone() call — it proves the
// tasks/ring/Sanitizer plumbing works in isolation, but never touches the
// production closure at builtins_wiring.go:143-174. And
// core/rpc/background_task_wiring_test.go — the one file in this package
// that already drives real registerBuiltinTools + a real *coretasks.Registry
// — has zero occurrences of "secret"/"sanitiz"/"redact" anywhere in it. The
// reviewer mutated all five production wirings background.go's fix touches;
// four (permissionsEngine, contextsync Gate, SecretLookup, DCRStore) were
// caught RED by pre-existing tests with the right failure message; mutating
// `Sanitizer: sanitizer` to nil left the ENTIRE ./core/rpc/... suite green.
//
// This test closes that gap: it drives the REAL registerBuiltinTools with a
// REAL *coretasks.Registry constructed with a REAL temp LogDir (not
// coretasks.Options{} in-memory-only, which is what
// newProductionShapedBashRegistry in background_task_wiring_test.go uses —
// this test needs the on-disk log file that Sanitizer wiring actually
// protects), spawns a background kenaz__bash command carrying a @secret:
// reference through the real refs.Resolver + refs.Sanitizer context the
// chat runner installs (refs.WithResolver / refs.WithTurnSanitizer — same
// setup core/tools/bash/secret_background_leak_test.go's
// setupSecretResolverCtx uses, reproduced here because that helper is
// unexported to package bash_test), and reads the ACTUAL bytes written to
// disk at <logDir>/<taskID>.log.
//
// Falsification (verified by hand while writing this test): deleting
// `Sanitizer: sanitizer,` from builtins_wiring.go's RegisterOpts literal
// (or changing the assignment to `Sanitizer: nil`) leaves the on-disk task
// log carrying the resolved secret plaintext — this test fails with the
// sentinel visible in the file content. See the mission report for the
// pasted RED output.
func TestB4_BackgroundSanitizerWiring_ResolvedSecretRedactedInTaskLog(t *testing.T) {
	logDir := t.TempDir()
	taskReg := coretasks.NewRegistry(coretasks.Options{LogDir: logDir})

	registry := toolloop.NewBuiltinRegistry()
	registerBuiltinTools(
		nil, // core
		registry,
		nil, // bashStore
		nil, // artifactsMgr
		nil, // store
		nil, // cedarEngine
		nil, // promptRegistry
		nil, // elicitAPI
		nil, // slashDispatch
		nil, // exposureIdx
		nil, // budget
		nil, // posture
		taskReg,
	)
	bashTool, ok := registry.Lookup(corebash.Name)
	if !ok {
		t.Fatal("kenaz__bash not registered")
	}

	const locator = "user:zz-b4-bgsanitizer"
	const sentinel = "ZZ_B4_BGSANITIZER_SENTINEL_9f2c"

	idx := secrets.NewExposureIndex()
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, []byte(sentinel))
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "sess-zz-b4-bgsanitizer",
		Agent:     "chat",
	})
	san := refs.NewSanitizer()
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)

	// No CedarEngine is wired above, so bash falls back to the legacy
	// allowlist gate; "echo" is in corebash.DefaultAllowlist.
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo @secret:" + locator,
		"run_in_background": true,
		"description":       "zz-b4 sanitizer wiring probe",
	})
	result, err := bashTool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TaskID == "" {
		t.Fatalf("no task_id in background-mode result: %s", result)
	}

	logPath := filepath.Join(logDir, out.TaskID+".log")
	deadline := time.Now().Add(3 * time.Second)
	var content []byte
	for time.Now().Before(deadline) {
		b, rerr := os.ReadFile(logPath)
		if rerr == nil && len(b) > 0 {
			content = b
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if len(content) == 0 {
		t.Fatalf("task log %s never gained content within 3s — background command may not have run", logPath)
	}

	if strings.Contains(string(content), sentinel) {
		t.Fatalf("REGRESSION: on-disk task log %s carries resolved secret plaintext — "+
			"builtins_wiring.go's `Sanitizer: sanitizer` is not reaching the real production wiring (B4): %s",
			logPath, content)
	}
}
