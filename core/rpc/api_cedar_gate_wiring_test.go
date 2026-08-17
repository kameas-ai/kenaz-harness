package rpc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// Cedar gate WIRING tests (audit findings A1 + A2, fixed 2026-08-16).
//
// The defect these pin was never "the gate helper is wrong" — every
// helper was correct and every call site called it. The defect was the
// ARGUMENT: five production sites passed cedar.AllowAll{} or left a
// Config's Gate field nil, so a user could write a policy, watch the
// editor validate it, save it, see it listed as loaded, and have nothing
// consult it.
//
// So these tests deliberately drive the REAL wiring path end to end:
// write a .cedar file into <DataDir>/policy/ exactly where the policy
// editor writes it, boot core.New + rpc.New over that DataDir, and
// assert the observable refusal at the surface a user touches. A test
// that merely asserted "the gate field is non-nil" would have passed
// against the broken tree.

// ---------------------------------------------------------------------------
// harness
// ---------------------------------------------------------------------------

// cedarWiringAPI boots a real Core + rpc.API over a temp DataDir that
// already contains policySrc at <DataDir>/policy/zz_test_policy.cedar —
// the same location core/rpc/views/cedarpolicy writes user policy to,
// and the same location buildCedarGate loads from. An empty policySrc
// boots with only the embedded default bundle.
func cedarWiringAPI(t *testing.T, policySrc string) *API {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	if policySrc != "" {
		polDir := filepath.Join(dataDir, cedar.PolicyDir)
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			t.Fatalf("mkdir policy dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(polDir, "zz_test_policy.cedar"), []byte(policySrc), 0o644); err != nil {
			t.Fatalf("write policy: %v", err)
		}
	}
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	assertSettingsStoreIsSandboxed(t, api)
	return api
}

// sandboxUserDir is the per-test redirect target for os.UserConfigDir.
//
// WHY THIS EXISTS. rpc.New's settings store is
// settings.NewFileStoreFromEnv() — os.UserConfigDir(), NOT the DataDir
// this harness hands core.New. Without the redirect
// TestCedarWiring_WorkflowStrictMode_IsReachableFromSettings writes
// `cedarStrictWorkflowMode: true` into the DEVELOPER'S REAL
// ~/Library/Application Support/kenaz-harness/settings.json, and its
// t.Fatal on the first assertion skips the line that flips it back. Two
// consequences, both observed:
//
//  1. `go test ./core/rpc/` leaves the installed harness in strict
//     workflow mode — every subsequent shell-bearing workflow save is
//     denied, in the real app, forever.
//  2. The leaked `true` then fails
//     TestCedarWiring_WorkflowSave_DefaultPolicyStillPermits on the NEXT
//     run of the unmodified tree — the over-blocking guard becomes
//     history-dependent, which is exactly the assertion that must not be.
//
// os.UserConfigDir reads $HOME/Library/Application Support (darwin),
// %AppData% (windows), $XDG_CONFIG_HOME or $HOME/.config (unix), so all
// three are redirected.
func sandboxUserDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("AppData", filepath.Join(dir, "AppData"))
	return dir
}

func sandboxUserConfigDir(t *testing.T) {
	t.Helper()
	dir := sandboxUserDir(t)
	got, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir after redirect: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("os.UserConfigDir() = %q, not under the sandbox %q — the redirect no longer works "+
			"and these tests would write the real user settings file", got, dir)
	}
}

// assertSettingsStoreIsSandboxed makes the redirect non-vacuous: if
// rpc.New ever stops routing settings through os.UserConfigDir (or the
// redirect silently stops applying) the tests must fail loudly rather
// than quietly resume mutating real user state.
func assertSettingsStoreIsSandboxed(t *testing.T, api *API) {
	t.Helper()
	if api.settingsImpl == nil {
		return
	}
	store := api.settingsImpl.Store()
	if store == nil {
		return
	}
	p, ok := store.(interface{ Path() string })
	if !ok {
		t.Fatalf("settings store %T exposes no Path(); cannot prove it is sandboxed", store)
	}
	home := os.Getenv("HOME")
	if home == "" || !strings.HasPrefix(p.Path(), home) {
		t.Fatalf("settings store path %q is outside the test sandbox %q — this test would write real user settings",
			p.Path(), home)
	}
}

func forbidPolicy(action string) string {
	return "forbid (\n    principal == User::\"local\",\n    action == Action::\"" + action + "\",\n    resource\n);\n"
}

// ---------------------------------------------------------------------------
// Site 1 — memory writes (api.go: gs.SetGate(&memoryGateAdapter{…}))
// ---------------------------------------------------------------------------

func memoryChunk() corememory.Chunk {
	return corememory.Chunk{
		ID:        "zz-cedar-wiring",
		ScopeKind: "global",
		ScopeID:   "global",
		Content:   "the user prefers tabs",
		Embedding: []float32{0.1, 0.2, 0.3},
		CreatedAt: time.Now().UTC(),
	}
}

func TestCedarWiring_MemoryWrite_DenyPolicyIsEnforced(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionMemoryWrite))

	// memStoreRef is the SAME store instance New() installed the gate
	// on, so this exercises the production wiring rather than a
	// re-opened copy.
	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef is nil — construction changed; this test no longer covers the wiring")
	}

	err := store.Add(context.Background(), memoryChunk())
	if err == nil {
		t.Fatal("memory write succeeded under a `forbid memory_write` policy — " +
			"the gate is wired to something that cannot deny")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "denied") &&
		!strings.Contains(strings.ToLower(err.Error()), "polic") {
		t.Fatalf("err = %v; want a policy denial", err)
	}
}

func TestCedarWiring_MemoryWrite_DefaultPolicyStillPermits(t *testing.T) {
	api := cedarWiringAPI(t, "")
	store := api.memStoreRef
	if store == nil {
		t.Fatal("memStoreRef is nil — construction changed")
	}
	if err := store.Add(context.Background(), memoryChunk()); err != nil {
		t.Fatalf("memory write refused under the shipped default bundle: %v — "+
			"wiring a real engine must not start denying what users rely on", err)
	}
}

// ---------------------------------------------------------------------------
// Site 4 — workflows (workflowsview.Config.Cedar)
// ---------------------------------------------------------------------------

const cedarWiringWorkflowYAML = `
id: zz-cedar-wiring-wf
name: "Cedar wiring probe"
version: 1
steps:
  - name: run_it
    kind: shell
    cmd: "echo hello"
`

func TestCedarWiring_WorkflowSave_DenyPolicyIsEnforced(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionWorkflowSave))
	ctx := context.Background()

	_, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: cedarWiringWorkflowYAML})
	if err == nil {
		t.Fatal("workflow save succeeded under a `forbid workflow.save` policy — " +
			"Config.Cedar is nil or cannot deny")
	}
	if !errors.Is(err, workflowsview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}

	// The refusal must be observable in PERSISTED state, not just in the
	// return value. This drives the real sqlite-backed workflow store
	// that core.New opened — a memory-store fixture would not have
	// exercised the encode/decode path.
	//
	// Match on NAME, not on the YAML's `id:`. Save mints its own
	// `wf-<hash>` id, so an id-based assertion here would be vacuous
	// (it was, in the first cut of this test).
	assertWorkflowNotPersisted(t, api, "Cedar wiring probe")
}

// assertWorkflowNotPersisted fails when a workflow with the given name
// is present in the catalog. The builtin catalog is non-empty, so
// "nothing was saved" cannot be spelled as len(list) == 0.
func assertWorkflowNotPersisted(t *testing.T, api *API, name string) {
	t.Helper()
	list, err := api.Workflows().List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	saw := 0
	for _, w := range list {
		if w.Name == name {
			saw++
		}
	}
	if saw != 0 {
		t.Fatalf("denied workflow %q was persisted anyway (%d rows)", name, saw)
	}
	if len(list) == 0 {
		t.Fatal("catalog is empty — this assertion cannot distinguish 'not saved' from 'nothing loaded'")
	}
}

func TestCedarWiring_WorkflowRun_DenyPolicyIsEnforced(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionWorkflowRun))
	ctx := context.Background()

	// Save first (save is permitted by the embedded bundle), then run.
	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: cedarWiringWorkflowYAML})
	if serr != nil {
		t.Fatalf("Save (should be permitted): %v", serr)
	}
	_, err := api.Workflows().Run(ctx, saved.ID, nil)
	if err == nil {
		t.Fatal("workflow run succeeded under a `forbid workflow.run` policy")
	}
	if !errors.Is(err, workflowsview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}
}

func TestCedarWiring_WorkflowSave_DefaultPolicyStillPermits(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	// The shipped Workflow bundle is EMBEDDED in every engine and its
	// permits are conditioned on context.mode == "permissive". Wiring
	// the gate therefore had a real over-blocking risk: if the mode
	// producer sent anything else, every save would fall through to
	// NotApplicable. Pin that a default install still saves — including
	// a shell-bearing workflow, which is exactly what the bundle's
	// strict arm forbids.
	if _, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: cedarWiringWorkflowYAML}); err != nil {
		t.Fatalf("shell-bearing workflow refused on a default install: %v — "+
			"the default posture is permissive; only cedarStrictWorkflowMode changes that", err)
	}
}

// The CedarModeFn producer: with the strict dial on, the shipped
// bundle's forbid rule fires. Before this dial existed nothing in the
// repo set the mode outside tests, so that arm could never be reached.
func TestCedarWiring_WorkflowStrictMode_IsReachableFromSettings(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	store := api.settingsImpl.Store()
	if store == nil {
		t.Fatal("settings store is nil — construction changed")
	}
	if err := store.SaveCedarStrictWorkflowMode(true); err != nil {
		t.Fatalf("SaveCedarStrictWorkflowMode: %v", err)
	}

	_, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: cedarWiringWorkflowYAML})
	if err == nil {
		t.Fatal("shell-bearing workflow saved with cedarStrictWorkflowMode=true — " +
			"the mode producer is not reaching the gate")
	}
	if !errors.Is(err, workflowsview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}

	// And the dial is live: turning it back off must permit the same
	// save without re-constructing the API.
	if err := store.SaveCedarStrictWorkflowMode(false); err != nil {
		t.Fatalf("SaveCedarStrictWorkflowMode(false): %v", err)
	}
	if _, err := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: cedarWiringWorkflowYAML}); err != nil {
		t.Fatalf("save after flipping the dial off: %v — the mode is being cached", err)
	}
}

// ---------------------------------------------------------------------------
// Site 5 — scheduled chat (scheduledchatview.Config.Cedar)
// ---------------------------------------------------------------------------

func scheduledChatInput() scheduledchatview.CreateInput {
	return scheduledchatview.CreateInput{
		Name:           "zz cedar wiring",
		PromptTemplate: "summarise my day",
		Cron:           "0 7 * * *",
		Timezone:       "UTC",
		Enabled:        true,
	}
}

func TestCedarWiring_ScheduledChatCreate_DenyPolicyIsEnforced(t *testing.T) {
	api := cedarWiringAPI(t, forbidPolicy(cedar.ActionScheduledRunCreate))
	ctx := context.Background()

	_, err := api.ScheduledChat().Create(ctx, scheduledChatInput())
	if err == nil {
		t.Fatal("scheduled chat create succeeded under a forbid policy — Config.Cedar is nil")
	}
	if !errors.Is(err, scheduledchatview.ErrCedarDenied) {
		t.Fatalf("err = %v; want a wrap of ErrCedarDenied", err)
	}

	// Persisted state must agree — this is the sqlite-backed chat store.
	list, lerr := api.ScheduledChat().List(ctx)
	if lerr != nil {
		t.Fatalf("List: %v", lerr)
	}
	if len(list) != 0 {
		t.Fatalf("denied scheduled run was persisted anyway (%d rows)", len(list))
	}
}

func TestCedarWiring_ScheduledChatCreate_DefaultPolicyStillPermits(t *testing.T) {
	api := cedarWiringAPI(t, "")
	ctx := context.Background()

	if _, err := api.ScheduledChat().Create(ctx, scheduledChatInput()); err != nil {
		t.Fatalf("scheduled chat create refused on a default install: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Site 3 — websearch (constructWebSearch's PolicyGate + backend gates)
// ---------------------------------------------------------------------------

// constructWebSearch is the sole wiring site for the websearch tool.
// Passing it a real engine must reach BOTH the Fetcher and the query
// backends; passing nil must stay ungated for the test chassis.
func TestCedarWiring_WebSearch_EngineReachesTheTool(t *testing.T) {
	eng, err := cedar.NewEngine(cedar.Options{})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	if err := eng.SetPolicyText("deny.cedar", []byte(forbidPolicy(cedar.ActionNetworkRequest))); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	tool := constructWebSearch(eng)
	if tool == nil {
		t.Fatal("constructWebSearch returned nil")
	}

	// Every backend is denied before it can issue a request, so the
	// aggregator reports total failure and Call surfaces an error. The
	// point is that the refusal happens at all: with AllowAll (the
	// shipped behaviour before 2026-08-16) this call would have made
	// live requests to duckduckgo.com and en.wikipedia.org.
	_, cerr := tool.Call(context.Background(), []byte(`{"query":"kenaz harness"}`))
	if cerr == nil {
		t.Fatal("web search succeeded under a `forbid network_request` policy")
	}
	if !strings.Contains(strings.ToLower(cerr.Error()), "denied") &&
		!strings.Contains(strings.ToLower(cerr.Error()), "polic") {
		t.Fatalf("err = %v; want a policy denial", cerr)
	}
}

// ---------------------------------------------------------------------------
// Site 2 — model selection (llmregistry.Options.Policy)
// ---------------------------------------------------------------------------

// newLLMStack is the only door: llmregistry.Options.Policy is set at
// construction and the registry exposes no policy setter. So the test
// drives newLLMStack itself over a DataDir carrying a
// `forbid model_select` policy, loads a profile, and asserts Stream
// refuses at pipeline step 3 (PolicyGuard) — before the credential
// resolver, so no secret is touched and no network call is made.
func TestCedarWiring_ModelSelect_DenyPolicyIsEnforced(t *testing.T) {
	dataDir := t.TempDir()
	polDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(polDir, 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(polDir, "zz_deny_model.cedar"),
		[]byte(forbidPolicy(cedar.ActionModelSelect)), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	reg := llmRegistryOverDataDir(t, dataDir)
	if err := reg.LoadProfiles([]corellm.ProviderProfile{zzProbeProfile()}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	_, err := reg.Stream(context.Background(), corellm.GenerationRequest{ProfileID: "zz-cedar-probe"})
	if err == nil {
		t.Fatal("Stream succeeded under a `forbid model_select` policy — " +
			"the guard is wired to something that cannot deny")
	}
	var denied *corellm.ErrPolicyDenied
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v (%T); want *llm.ErrPolicyDenied", err, err)
	}
}

func TestCedarWiring_ModelSelect_DefaultPolicyStillPermits(t *testing.T) {
	reg := llmRegistryOverDataDir(t, t.TempDir())
	if err := reg.LoadProfiles([]corellm.ProviderProfile{zzProbeProfile()}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}

	// The shipped bundle permits model_select, so the pipeline must get
	// PAST step 3. It will fail later (no credential in the env, no
	// adapter reachable) — what must NOT happen is a policy denial.
	_, err := reg.Stream(context.Background(), corellm.GenerationRequest{ProfileID: "zz-cedar-probe"})
	var denied *corellm.ErrPolicyDenied
	if errors.As(err, &denied) {
		t.Fatalf("the shipped default bundle denied model selection: %v — "+
			"wiring a real engine must not start blocking every model", err)
	}
}

func zzProbeProfile() corellm.ProviderProfile {
	return corellm.ProviderProfile{
		ID:    "zz-cedar-probe",
		Kind:  "anthropic",
		Model: "claude-sonnet-4-20250514",
		Cred:  corellm.CredentialReference{Kind: "env", Locator: "ZZ_CEDAR_PROBE_KEY_UNSET"},
	}
}

// llmRegistryOverDataDir boots the production LLM stack over dataDir and
// returns the registry it built. Every dependency other than the Core is
// nil-tolerant; the point of the call is that the Cedar guard is
// constructed inside newLLMStack exactly as it is at boot.
func llmRegistryOverDataDir(t *testing.T, dataDir string) corellm.Registry {
	t.Helper()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	stack := newLLMStack(c, NewStreamBroker(NewMultiEmitter()), newPersonalStore(c),
		nil, nil, func() bool { return false }, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil)
	if stack.reg == nil {
		t.Fatal("newLLMStack produced no registry — construction changed")
	}
	return stack.reg
}

// ---------------------------------------------------------------------------
// Site 6 — recipe spawn (tools.Config.Gate)
// ---------------------------------------------------------------------------

// The sixth site is the one no behavioural test covered: deleting
// `Gate: buildCedarGate(dataDir)` from newToolsAPI left the whole
// ./core/rpc/... tree green, so only check-cedar-gate-arguments.sh
// clause 3 stood between the field and silent removal.
//
// This drives the flow the shipped template documents in its own header
// ("Copy this file to <DataDir>/policy/"): install mcp-no-npx.cedar,
// then ask the tools surface to install the one shipped npx-based
// recipe, and require the refusal.
func TestCedarWiring_RecipeSpawn_ShippedNoNpxTemplateIsEnforced(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx is not installed; InstallRecipe's prereq check fires before the Cedar gate")
	}
	src, err := cedar.PoliciesFS.ReadFile("policies/mcp-no-npx.cedar")
	if err != nil {
		t.Fatalf("read shipped template: %v", err)
	}
	api := cedarWiringAPI(t, string(src))

	_, err = api.Tools().InstallRecipe(context.Background(), "filesystem", nil, nil)
	if err == nil {
		t.Fatal("the npx-based `filesystem` recipe installed under mcp-no-npx.cedar — tools.Config.Gate is nil or cannot deny")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cedar") {
		t.Fatalf("err = %v; want the Cedar gate refusal, not an unrelated failure", err)
	}
}

// ---------------------------------------------------------------------------
// Over-blocking matrix: a default install permits every gated action
// ---------------------------------------------------------------------------

// The surface-level "DefaultPolicyStillPermits" tests above cover four
// of the newly-wired actions. This pins the whole set at the gate the
// production builder returns, including the two with no surface test
// (network_request, recipe spawn) and the delete/execute verbs.
//
// Every entry here worked before the gates were wired — precisely
// because nothing was wired. If any starts denying, this change has
// taken a capability away from users on a default install.
func TestCedarWiring_DefaultInstall_PermitsEveryGatedAction(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	g := buildCedarGate(dir)
	if _, degraded := g.(cedar.AllowAll); degraded {
		t.Fatal("buildCedarGate degraded to AllowAll for a real DataDir — every assertion below would be vacuous")
	}
	ctx := context.Background()

	if err := cedar.CheckMemoryWrite(ctx, g, "global"); err != nil {
		t.Errorf("memory_write denied on a default install: %v", err)
	}
	for _, host := range []string{"html.duckduckgo.com", "en.wikipedia.org"} {
		if err := cedar.CheckNetwork(ctx, g, host); err != nil {
			t.Errorf("network_request(%s) denied on a default install: %v", host, err)
		}
	}
	if err := cedar.CheckRecipeSpawn(ctx, g, "filesystem", "npx", "stdio"); err != nil {
		t.Errorf("recipe spawn(npx) denied on a default install: %v — mcp-no-npx.cedar must stay opt-in", err)
	}
	// "" is the mode a Config with neither CedarMode nor CedarModeFn
	// produces; the helpers must coerce it to the permissive arm.
	for _, mode := range []string{"permissive", ""} {
		if d, err := cedar.GateWorkflowSave(ctx, g, "wf-1", mode, "model_turn,shell"); err != nil || d.Outcome == cedar.Deny {
			t.Errorf("workflow.save(shell, mode=%q): outcome=%v err=%v; want permit", mode, d.Outcome, err)
		}
		if d, err := cedar.GateWorkflowRun(ctx, g, "wf-1", mode, "model_turn,shell"); err != nil || d.Outcome == cedar.Deny {
			t.Errorf("workflow.run(shell, mode=%q): outcome=%v err=%v; want permit", mode, d.Outcome, err)
		}
	}
	if d, err := cedar.GateWorkflowDelete(ctx, g, "wf-1"); err != nil || d.Outcome == cedar.Deny {
		t.Errorf("workflow.delete: outcome=%v err=%v; want permit", d.Outcome, err)
	}
	for name, call := range map[string]func() (cedar.Decision, error){
		"scheduled_run.create":  func() (cedar.Decision, error) { return cedar.GateScheduledChatCreate(ctx, g, "sc-1") },
		"scheduled_run.delete":  func() (cedar.Decision, error) { return cedar.GateScheduledChatDelete(ctx, g, "sc-1") },
		"scheduled_run.execute": func() (cedar.Decision, error) { return cedar.GateScheduledChatExecute(ctx, g, "sc-1") },
	} {
		d, err := call()
		if err != nil || d.Outcome == cedar.Deny {
			t.Errorf("%s: outcome=%v err=%v; want permit", name, d.Outcome, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Posture: fail-open on construction failure, and DefaultDeny stays off
// ---------------------------------------------------------------------------

// buildCedarGate's contract on every failure path is FAIL-OPEN, so a
// typo in a user's policy file cannot brick the app. This pins that
// posture so a future change has to be deliberate about flipping it.
func TestBuildCedarGate_PostureIsFailOpen(t *testing.T) {
	t.Parallel()

	t.Run("empty datadir yields AllowAll", func(t *testing.T) {
		t.Parallel()
		if _, ok := buildCedarGate("").(cedar.AllowAll); !ok {
			t.Fatalf("buildCedarGate(\"\") = %T; want cedar.AllowAll", buildCedarGate(""))
		}
	})

	t.Run("unparseable policy file does not fail closed", func(t *testing.T) {
		t.Parallel()
		dataDir := t.TempDir()
		polDir := filepath.Join(dataDir, cedar.PolicyDir)
		if err := os.MkdirAll(polDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(polDir, "broken.cedar"), []byte("this is not cedar {{{"), 0o644); err != nil {
			t.Fatal(err)
		}
		gate := buildCedarGate(dataDir)
		if gate == nil {
			t.Fatal("buildCedarGate returned nil for a broken policy dir")
		}
		// A memory write must still be permitted: the embedded bundle
		// stays active and the broken file is reported, not enforced.
		if err := cedar.CheckMemoryWrite(context.Background(), gate, "global"); err != nil {
			t.Fatalf("a typo in a user policy file failed the gate CLOSED: %v", err)
		}
	})

	t.Run("nil gate permits", func(t *testing.T) {
		t.Parallel()
		// The A2 half restated: a Config that omits its Gate field
		// leaves nil, and nil is an unconditional permit. This is why
		// omission is a defect and not a safe default.
		if err := cedar.CheckMemoryWrite(context.Background(), nil, "global"); err != nil {
			t.Fatalf("nil gate denied: %v", err)
		}
		d, err := cedar.GateWorkflowSave(context.Background(), nil, "wf", "", "shell")
		if err != nil || d.Outcome != cedar.Allow {
			t.Fatalf("nil gate: outcome=%v err=%v; want Allow/nil", d.Outcome, err)
		}
	})
}
