package cedar_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestEmbeddedCorpus_NoForbidErrorsOnDesktopRequests is the pre-condition
// for the erroring-forbid fail-closed fix (provider-credentials-client-
// 01PCCLI01 §9.1). Before the fix an erroring forbid was silently
// skipped; after it, it denies. So any shipped forbid that errors on a
// request the desktop actually sends was fail-OPEN before and would
// start denying now. This test loads every shipped policy — the engine's
// embedded defaults, every installable template in policies/*.cedar, the
// harness-self snippets and the graph-authoring snippets — and drives a
// representative request for every action family through the same
// helpers / context shapes the real call sites use, asserting that no
// forbid errors.
func TestEmbeddedCorpus_NoForbidErrorsOnDesktopRequests(t *testing.T) {
	e := corpusEngine(t)
	ctx := context.Background()
	s := func(v string) cedarlib.String { return cedarlib.String(v) }
	str := func(v string) cedarlib.Value { return cedarlib.String(v) }

	// Wrapper helpers in hooks.go (they build their own contexts).
	_ = cedar.CheckTool(ctx, e, "kenaz", "read_file")
	_ = cedar.CheckUseTool(ctx, e, "github", "create_issue")
	_ = cedar.CheckUseTool(ctx, e, "harness-self", "harness_write_settings")
	_ = cedar.CheckModel(ctx, e, "anthropic", "claude-x")
	_ = cedar.CheckMemoryWrite(ctx, e, "project")
	_ = cedar.CheckNetwork(ctx, e, "api.example.com")
	_ = cedar.CheckFileWrite(ctx, e, "/Users/u/proj/a.go")
	_ = cedar.CheckFileRead(ctx, e, "/Users/u/proj/a.go")
	_ = cedar.CheckStateRead(ctx, e, "bash_output")
	_ = cedar.CheckStateWrite(ctx, e, "scratch")
	_ = cedar.CheckRecipeAdd(ctx, e, "rec", "npx", "stdio")
	_ = cedar.CheckRecipeSpawn(ctx, e, "rec", "uvx", "stdio")
	for _, purpose := range []string{"provider_call", "provider_test", "tool_dispatch", "manual_export", "mcp_spawn"} {
		_ = cedar.CheckCredentialAccess(ctx, e, "anthropic", purpose, false)
	}
	_ = cedar.GateMCPSpawn(ctx, e, nil, "rec", "", nil)
	for _, mode := range []string{"", "permissive", "strict"} {
		_, _ = cedar.GateWorkflowRun(ctx, e, "wf", mode, "shell,llm")
		_, _ = cedar.GateWorkflowSave(ctx, e, "wf", mode, "shell,llm")
		_, _ = cedar.GateWorkflowNetworkFetch(ctx, e, "wf", mode)
	}
	_, _ = cedar.GateWorkflowDelete(ctx, e, "wf")
	for _, enabled := range []bool{true, false} {
		_, _ = cedar.GateGraphAuthor(ctx, e, "g", enabled, "chat", "llm,write_file", 3)
	}
	for _, prov := range []string{"model_authored", "user", ""} {
		_, _ = cedar.GateGraphRun(ctx, e, "g", prov, "chat", "user")
	}
	_, _ = cedar.GateScheduledChatCreate(ctx, e, "r", "model")
	_, _ = cedar.GateScheduledChatDelete(ctx, e, "r")
	_, _ = cedar.GateScheduledChatExecute(ctx, e, "r", "model", true)
	_, _ = cedar.GateScheduledChatExecute(ctx, e, "r", "user", false)
	_, _ = cedar.GateSubagentAbort(ctx, e, "b")
	_, _ = cedar.GateSubagentSteer(ctx, e, "b", 12)
	_, _ = cedar.GateSubagentPause(ctx, e, "b")
	_, _ = cedar.GateSubagentResume(ctx, e, "b")
	_ = cedar.CheckExportSession(ctx, e, "s", "markdown")
	_ = cedar.CheckApprovalResolve(ctx, e, "run", "node", true)
	_ = cedar.CheckAuditBulkPurge(ctx, e)
	_ = cedar.CheckContextSyncSessionPurge(ctx, e, "s")
	_ = cedar.CheckContextSyncProjectPurge(ctx, e, "p")
	_ = cedar.CheckLLMFallback(ctx, e, "chain")
	_ = cedar.CheckContextBootstrapRun(ctx, e)
	_ = cedar.CheckBundleInstall(ctx, e, "https://example.com/b.json")
	for _, kind := range []string{"", "trusted", "untrusted"} {
		for _, budget := range []int64{0, 5} {
			_ = cedar.EvaluateSecretReferenceResolve(ctx, e, "env:X", cedar.ResolveContext{
				ToolName: "web_fetch", DestinationHost: "api.example.com", Budget: budget, AgentKind: kind,
			})
		}
	}

	// Call sites that build their context outside hooks.go.
	// core/tools/bash/bash.go cedarGate.
	e.Evaluate(ctx, cedar.UserUID(), cedar.ActionRunBashCommand, cedar.BashCommandUID("git status"),
		map[cedarlib.String]cedarlib.Value{
			s(cedar.CtxKeyPattern):       str("git status"),
			s(cedar.CtxKeyWorkingDir):    str("/Users/u/proj"),
			s(cedar.CtxKeyDangerousTier): cedarlib.Boolean(false),
			s("argv_count"):              cedarlib.Long(2),
		})
	// core/tools/fs/gate.go Evaluate.
	for _, action := range []string{cedar.ActionReadFilesystem, cedar.ActionWriteFilesystem} {
		for _, p := range []string{"/Users/u/proj/a.go", "/Users/u/.ssh/id_rsa", "/etc/hosts"} {
			e.Evaluate(ctx, cedar.UserUID(), action, cedar.FilesystemOpUID(p),
				map[cedarlib.String]cedarlib.Value{
					s(cedar.CtxKeyCanonicalPath):  str(p),
					s(cedar.CtxKeyRecipeDirMatch): cedarlib.Boolean(false),
					s(cedar.CtxKeyDangerousTier):  cedarlib.Boolean(false),
				})
		}
	}
	// core/rpc/harness_session_kind_resolver.go: session_kind only.
	for _, tool := range []string{"harness-self__harness_write_settings", "harness-self__harness_read_x", "github__force_push", "stripe__create_payment"} {
		for _, kind := range []string{"chat", "onboarding", ""} {
			e.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseTool, cedar.PermissionToolUID(tool),
				map[cedarlib.String]cedarlib.Value{s(cedar.CtxKeySessionKind): str(kind)})
		}
	}
	// core/rpc/views/agentgraph/chat/kernel_tool_adapter.go: nil context.
	e.Evaluate(ctx, cedar.UserUID(), cedar.ActionUseTool, cedar.ToolUID("github", "delete_repo"), nil)
	// core/rpc/views/acp/acp.go ACP_Dispatch (EngineAdapter.Check shape).
	for _, tier := range []string{"verified", "pending", "revoked"} {
		e.Evaluate(ctx, cedar.UserUID(), cedar.ActionACPSend, cedar.ACPEnvelopeUID("peer"),
			map[cedarlib.String]cedarlib.Value{
				s("peer_id"): str("peer"), s("peer_trust_tier"): str(tier),
				s("transport"): str("http"), s("direction"): str("send"),
			})
	}
	// core/policy/cedar/llmguard.go: model_select, nil context.
	e.Evaluate(ctx, cedar.UserUID(), cedar.ActionModelSelect, cedar.ModelUID("anthropic", "m"), nil)
	// Tool-family fine-grained actions evaluated with no context.
	for _, a := range []string{
		cedar.ActionToolReadGlob, cedar.ActionToolReadGrep, cedar.ActionToolTodoWrite,
		cedar.ActionToolTodoRead, cedar.ActionElicitAsk, cedar.ActionElicitDeferred,
		cedar.ActionPassive, cedar.ActionToolSkillInvoke, cedar.ActionToolSubagentDispatch,
		cedar.ActionToolSubagentMerge, cedar.ActionToolListSecrets, cedar.ActionToolTasksMonitor,
		cedar.ActionToolTasksCancel, cedar.ActionArtifactUpdate, cedar.ActionToolExec,
	} {
		e.Evaluate(ctx, cedar.UserUID(), a, cedar.ToolUID("kenaz", "x"), nil)
	}

	var forbidErrs []cedar.PolicyEvalError
	for _, pe := range e.PolicyEvalErrors() {
		t.Logf("eval error: %+v", pe)
		if pe.Effect == "forbid" {
			forbidErrs = append(forbidErrs, pe)
		}
	}
	if len(forbidErrs) > 0 {
		t.Fatalf("shipped forbid policies error on desktop requests (would now deny): %+v", forbidErrs)
	}
}

// corpusEngine builds an engine carrying every shipped policy: the
// embedded defaults, every policies/*.cedar template (installed the way
// InstallTemplate lands them — as a user file in <DataDir>/policy/),
// and both snippet sets the boot path installs via LoadHarnessSnippets.
func corpusEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	dataDir := t.TempDir()
	probe, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("probe engine: %v", err)
	}
	embedded := map[string]bool{}
	for _, f := range probe.ListPolicies() {
		embedded[f.Name] = true
	}
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	templates, err := fs.Glob(cedar.PoliciesFS, "policies/*.cedar")
	if err != nil || len(templates) == 0 {
		t.Fatalf("glob templates: %v (%d)", err, len(templates))
	}
	for _, tp := range templates {
		name := filepath.Base(tp)
		if embedded[name] {
			continue
		}
		body, err := cedar.PoliciesFS.ReadFile(tp)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(policyDir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for label, load := range map[string]func() (map[string][]byte, error){
		"harness-self":    harnessmcp.CedarSnippets,
		"graph-authoring": graphview.CedarSnippets,
	} {
		snips, err := load()
		if err != nil || len(snips) == 0 {
			t.Fatalf("%s snippets: %v (%d)", label, err, len(snips))
		}
		if err := e.LoadHarnessSnippets(snips); err != nil {
			t.Fatalf("%s LoadHarnessSnippets: %v", label, err)
		}
	}
	for _, f := range e.ListPolicies() {
		if !f.ParseOK {
			t.Fatalf("shipped policy %s failed to parse: %s", f.Name, f.ParseErr)
		}
	}
	return e
}

// TestEmbeddedCorpus_DetectsPlantedErroringForbid proves the corpus
// check is not vacuous: a forbid that reads a missing attribute, loaded
// alongside the shipped corpus, is recorded as an erroring forbid and
// denies.
func TestEmbeddedCorpus_DetectsPlantedErroringForbid(t *testing.T) {
	e := corpusEngine(t)
	planted := map[string][]byte{"planted_forbid.cedar": []byte(
		`forbid(principal, action == Action::"file_read", resource) when { context.missing_attr == "mobile" };`)}
	if err := e.LoadHarnessSnippets(planted); err != nil {
		t.Fatal(err)
	}
	if err := cedar.CheckFileRead(context.Background(), e, "/Users/u/proj/a.go"); !cedar.IsPolicyDenied(err) {
		t.Fatalf("CheckFileRead err = %v, want a policy denial", err)
	}
	var found bool
	for _, pe := range e.PolicyEvalErrors() {
		if pe.Effect == "forbid" && pe.File == "planted_forbid.cedar" {
			found = true
		}
	}
	if !found {
		t.Fatalf("planted erroring forbid not recorded: %+v", e.PolicyEvalErrors())
	}
}
