package cedar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// TestNewEngine_DefaultBundleAllowsTool exercises the embedded default
// policy: a tool execution against the canonical local user must be
// permitted out of the box.
func TestNewEngine_DefaultBundleAllowsTool(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d := e.Evaluate(
		context.Background(),
		UserUID(),
		ActionToolExec,
		ToolUID("filesystem", "list"),
		nil,
	)
	if d.Outcome != Allow {
		t.Fatalf("default bundle should allow tool_exec, got %s (reason=%s)",
			d.Outcome, d.Reason)
	}
	if d.MatchedPolicy == "" {
		t.Fatalf("expected MatchedPolicy to be set on Allow")
	}
}

// TestNewEngine_NoEngine_NoDisk exercises the in-memory boot path:
// the engine boots cleanly with no embedded policy and no disk files;
// every action becomes NotApplicable (default-allow with audit).
func TestNewEngine_EmptyBundle_NotApplicable(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{
		IncludeEmbedded: false,
		LoadFromDisk:    false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d := e.Evaluate(
		context.Background(),
		UserUID(),
		ActionToolExec,
		ToolUID("filesystem", "list"),
		nil,
	)
	if d.Outcome != NotApplicable {
		t.Fatalf("empty bundle should produce NotApplicable, got %s", d.Outcome)
	}
}

// TestEngine_DefaultDeny_FailsClosed exercises the user-opt-in
// fail-closed mode: an empty bundle plus DefaultDeny=true denies
// every action.
func TestEngine_DefaultDeny_FailsClosed(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{
		IncludeEmbedded: false,
		LoadFromDisk:    false,
		DefaultDeny:     true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	d := e.Evaluate(
		context.Background(),
		UserUID(),
		ActionToolExec,
		ToolUID("filesystem", "list"),
		nil,
	)
	if d.Outcome != Deny {
		t.Fatalf("DefaultDeny should deny on empty bundle, got %s", d.Outcome)
	}
	if d.Reason == "" {
		t.Fatalf("expected non-empty Reason on Deny")
	}
}

// TestEngine_ForbidPolicyDenies installs a custom forbid policy via
// SetPolicyText and asserts a deny outcome with the matching policy
// id surfaced.
func TestEngine_ForbidPolicyDenies(t *testing.T) {
	t.Parallel()
	const src = `
permit (principal, action, resource);

forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"filesystem__delete"
);
`
	e, err := NewEngine(Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("custom.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	// Allowed tool: not the forbidden one.
	d := e.Evaluate(
		context.Background(), UserUID(), ActionToolExec,
		ToolUID("filesystem", "list"), nil,
	)
	if d.Outcome != Allow {
		t.Fatalf("expected Allow for filesystem__list, got %s (%s)",
			d.Outcome, d.Reason)
	}
	// Denied tool: the forbidden one.
	d = e.Evaluate(
		context.Background(), UserUID(), ActionToolExec,
		ToolUID("filesystem", "delete"), nil,
	)
	if d.Outcome != Deny {
		t.Fatalf("expected Deny for filesystem__delete, got %s (%s)",
			d.Outcome, d.Reason)
	}
}

// TestEngine_ReloadFromDisk writes two .cedar files under DataDir/policy
// and asserts the engine picks them up on Reload, in lexicographic
// order, alongside the embedded default.
func TestEngine_ReloadFromDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// File "01_allow.cedar" — allow netfetch.
	allow := []byte(`permit (principal, action == Action::"network_request", resource);`)
	if err := os.WriteFile(filepath.Join(policyDir, "01_allow.cedar"), allow, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// File "02_forbid.cedar" — forbid memory_write to global.
	forbid := []byte(`forbid (
		principal == User::"local",
		action == Action::"memory_write",
		resource == Memory::"global"
	);`)
	if err := os.WriteFile(filepath.Join(policyDir, "02_forbid.cedar"), forbid, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Listing should report 8 files: the 6 embedded defaults
	// (default_policy + the four WP01 family defaults + the WP11
	// workflows family default) + 2 disk files.
	files := e.ListPolicies()
	if len(files) != 8 {
		t.Fatalf("ListPolicies len=%d, want 8", len(files))
	}
	if !files[0].Embedded {
		t.Fatalf("first file should be embedded default, got %+v", files[0])
	}

	// memory_write to "global" must be denied.
	d := e.Evaluate(
		context.Background(), UserUID(), ActionMemoryWrite,
		MemoryUID("global"), nil,
	)
	if d.Outcome != Deny {
		t.Fatalf("expected Deny for memory_write/global, got %s", d.Outcome)
	}

	// memory_write to "session" still allowed by embedded permit.
	d = e.Evaluate(
		context.Background(), UserUID(), ActionMemoryWrite,
		MemoryUID("session"), nil,
	)
	if d.Outcome != Allow {
		t.Fatalf("expected Allow for memory_write/session, got %s (%s)",
			d.Outcome, d.Reason)
	}
}

// TestEngine_InvalidPolicyFile_Tolerated asserts that one broken file
// does not abort the reload; the engine keeps working with the rest.
func TestEngine_InvalidPolicyFile_Tolerated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policyDir := filepath.Join(dir, PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// One valid file:
	if err := os.WriteFile(filepath.Join(policyDir, "01_ok.cedar"),
		[]byte(`permit (principal, action, resource);`), 0o644); err != nil {
		t.Fatalf("write ok: %v", err)
	}
	// One broken file:
	if err := os.WriteFile(filepath.Join(policyDir, "02_broken.cedar"),
		[]byte(`this is not cedar`), 0o644); err != nil {
		t.Fatalf("write broken: %v", err)
	}

	e, err := NewEngine(Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	files := e.ListPolicies()
	var sawBroken bool
	for _, f := range files {
		if f.Name == "02_broken.cedar" && !f.ParseOK && f.ParseErr != "" {
			sawBroken = true
		}
	}
	if !sawBroken {
		t.Fatalf("broken file did not surface in ListPolicies: %+v", files)
	}
	// Engine must still allow tool_exec via the embedded default + ok file.
	d := e.Evaluate(
		context.Background(), UserUID(), ActionToolExec,
		ToolUID("websearch", "search"), nil,
	)
	if d.Outcome != Allow {
		t.Fatalf("expected Allow despite one broken file, got %s", d.Outcome)
	}
}

// TestEngine_DecisionLog records every Evaluate call.
func TestEngine_DecisionLog(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for i := 0; i < 3; i++ {
		_ = e.Evaluate(
			context.Background(), UserUID(), ActionToolExec,
			ToolUID("filesystem", "list"), nil,
		)
	}
	recent := e.RecentDecisions(10)
	if len(recent) != 3 {
		t.Fatalf("RecentDecisions returned %d, want 3", len(recent))
	}
	if recent[0].Action != ActionToolExec {
		t.Fatalf("RecentDecisions[0].Action = %q, want tool_exec", recent[0].Action)
	}
}

// TestEngine_PolicyDeniedError_Helpers exercises the gate helpers:
// Deny outcomes return *PolicyDeniedError; Allow / NotApplicable
// return nil.
func TestEngine_GateHooks(t *testing.T) {
	t.Parallel()
	const src = `
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"filesystem__delete"
);
`
	e, err := NewEngine(Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Append the forbid rule to the embedded base by reloading with
	// SetPolicyText that includes both the base permits and the
	// forbid. We reuse the embedded source plus an extra rule.
	combined := append([]byte{}, defaultPolicySource...)
	combined = append(combined, '\n')
	combined = append(combined, []byte(src)...)
	if err := e.SetPolicyText("combined.cedar", combined); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	if err := CheckTool(context.Background(), e, "websearch", "search"); err != nil {
		t.Fatalf("CheckTool websearch__search should allow: %v", err)
	}
	err = CheckTool(context.Background(), e, "filesystem", "delete")
	if err == nil {
		t.Fatal("CheckTool filesystem__delete should deny")
	}
	if !IsPolicyDenied(err) {
		t.Fatalf("CheckTool deny should be PolicyDeniedError, got %T", err)
	}

	// AllowAll fallback never errors.
	if err := CheckTool(context.Background(), AllowAll{}, "any", "thing"); err != nil {
		t.Fatalf("AllowAll.CheckTool returned err: %v", err)
	}
}

// TestEngine_GateNilGate is defensive: nil gate is a valid wiring
// state during boot before the engine is constructed; helpers must
// silently allow.
func TestEngine_GateNilGate(t *testing.T) {
	t.Parallel()
	if err := CheckTool(context.Background(), nil, "x", "y"); err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
	if err := CheckModel(context.Background(), nil, "p", "m"); err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
	if err := CheckMemoryWrite(context.Background(), nil, "global"); err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
	if err := CheckNetwork(context.Background(), nil, "example.com"); err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
	if err := CheckFileWrite(context.Background(), nil, "/tmp/x"); err != nil {
		t.Fatalf("nil gate should allow, got %v", err)
	}
}

// TestUIDHelpers asserts the EntityUID builders produce the canonical
// shapes the policy bundle expects.
func TestUIDHelpers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		uid  cedarlib.EntityUID
		want string
	}{
		{"tool", ToolUID("filesystem", "list"), `Tool::"filesystem__list"`},
		{"tool-builtin", ToolUID("", "websearch"), `Tool::"builtin__websearch"`},
		{"model", ModelUID("openai", "gpt-4o"), `Model::"openai:gpt-4o"`},
		{"net", NetworkUID("Example.COM."), `Network::"example.com"`},
		{"fs", FilesystemUID("/tmp/x"), `Filesystem::"/tmp/x"`},
		{"mem", MemoryUID("global"), `Memory::"global"`},
		{"user", UserUID(), `User::"local"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.uid.String(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestPolicyDeniedError_Sentinel ensures errors.As reaches the typed
// error through wrapping.
func TestPolicyDeniedError_Sentinel(t *testing.T) {
	t.Parallel()
	d := Decision{Outcome: Deny, Action: ActionToolExec, Resource: `Tool::"x"`, Reason: "x"}
	wrapped := errors.New("outer: " + (&PolicyDeniedError{Decision: d}).Error())
	if IsPolicyDenied(wrapped) {
		t.Fatalf("plain wrapping (errors.New) should NOT be PolicyDenied")
	}
	pe := &PolicyDeniedError{Decision: d}
	if !IsPolicyDenied(pe) {
		t.Fatalf("direct PolicyDeniedError should match")
	}
}

// =====================================================================
// WP01 — universal-permission family acceptance tests.
// =====================================================================
//
// The four resource families share the same engine but exercise four
// independent default-policy files. The cases below mirror the WP01
// acceptance matrix verbatim:
//
//   Credential family:
//     - mcp_spawn          → NotApplicable (no rule covers it)
//     - manual_export      → Deny (forbid in default_credential_policy)
//     - provider_call      → Allow (permit in default_credential_policy)
//
//   Bash family:
//     - any pattern        → NotApplicable (header-only default policy)
//
//   Filesystem family:
//     - read inside recipe-dir  → Allow
//     - write inside recipe-dir → NotApplicable
//     - read outside recipe-dir → NotApplicable
//     - write outside recipe-dir→ NotApplicable
//
//   Tool family:
//     - kaneaz__bash       → Allow (server_name=="kaneaz")
//     - filesystem__read   → NotApplicable (non-builtin)

// TestEvaluate_Family_Credential covers FR-002.
func TestEvaluate_Family_Credential(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name    string
		purpose string
		want    Outcome
	}{
		{"mcp_spawn-not-applicable", "mcp_spawn", NotApplicable},
		{"manual_export-deny", "manual_export", Deny},
		{"provider_call-allow", "provider_call", Allow},
		{"provider_test-allow", "provider_test", Allow},
		{"tool_dispatch-allow", "tool_dispatch", Allow},
		{"unknown-purpose-not-applicable", "future_purpose", NotApplicable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				ActionUseCredential,
				CredentialUID("openai", tc.purpose),
				map[cedarlib.String]cedarlib.Value{
					cedarlib.String(CtxKeyPurpose): cedarlib.String(tc.purpose),
				},
			)
			if d.Outcome != tc.want {
				t.Fatalf("purpose=%q: outcome=%s (reason=%s) want %s",
					tc.purpose, d.Outcome, d.Reason, tc.want)
			}
		})
	}
}

// TestEvaluate_Family_Bash covers FR-003 — every bash request is
// NotApplicable against the empty default bundle.
func TestEvaluate_Family_Bash(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name    string
		pattern string
	}{
		{"git-status", "git status"},
		{"ls", "ls"},
		{"rm-dangerous", "rm"},
		{"sudo-dangerous", "sudo"},
		{"unknown", "kubectl get"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				ActionRunBashCommand,
				BashCommandUID(tc.pattern),
				nil,
			)
			if d.Outcome != NotApplicable {
				t.Fatalf("pattern=%q: outcome=%s want NotApplicable",
					tc.pattern, d.Outcome)
			}
		})
	}
}

// TestEvaluate_Family_Filesystem covers FR-004.
func TestEvaluate_Family_Filesystem(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name        string
		action      string
		path        string
		recipeMatch bool
		want        Outcome
	}{
		{"read-inside-recipe-dir-allow", ActionReadFilesystem, "/Users/alec/recipe/notes.md", true, Allow},
		{"write-inside-recipe-dir-not-applicable", ActionWriteFilesystem, "/Users/alec/recipe/notes.md", true, NotApplicable},
		{"read-outside-recipe-dir-not-applicable", ActionReadFilesystem, "/etc/passwd", false, NotApplicable},
		{"write-outside-recipe-dir-not-applicable", ActionWriteFilesystem, "/etc/passwd", false, NotApplicable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				tc.action,
				FilesystemOpUID(tc.path),
				map[cedarlib.String]cedarlib.Value{
					cedarlib.String(CtxKeyRecipeDirMatch): cedarlib.Boolean(tc.recipeMatch),
				},
			)
			if d.Outcome != tc.want {
				t.Fatalf("%s on %q (recipeMatch=%v): outcome=%s (reason=%s) want %s",
					tc.action, tc.path, tc.recipeMatch, d.Outcome, d.Reason, tc.want)
			}
		})
	}
}

// TestEvaluate_Family_Tool covers FR-005.
func TestEvaluate_Family_Tool(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name string
		uid  string
		want Outcome
	}{
		{"builtin-bash-allow", "kaneaz__bash", Allow},
		{"builtin-websearch-allow", "kaneaz__websearch", Allow},
		{"builtin-save-artifact-allow", "kaneaz__save_artifact", Allow},
		{"mcp-filesystem-not-applicable", "filesystem__read_file", NotApplicable},
		{"mcp-postgres-not-applicable", "postgres__query", NotApplicable},
		{"no-server-not-applicable", "bare_tool", NotApplicable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				ActionUseTool,
				PermissionToolUID(tc.uid),
				nil,
			)
			if d.Outcome != tc.want {
				t.Fatalf("tool=%q: outcome=%s (reason=%s) want %s",
					tc.uid, d.Outcome, d.Reason, tc.want)
			}
		})
	}
}

// TestFamilyUIDValidation asserts the UID constructors reject malformed
// ids per the WP01 acceptance criteria. The replacement value is the
// literal "invalid" so policy `is <Type>` clauses keep type-matching
// (preventing silent bypasses) while never satisfying realistic
// permits.
func TestFamilyUIDValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want string
	}{
		// Credential
		{"credential-empty-provider", CredentialUID("", "provider_call").String(), `Credential::"invalid"`},
		{"credential-empty-purpose", CredentialUID("openai", "").String(), `Credential::"invalid"`},
		{"credential-traversal-provider", CredentialUID("..foo", "provider_call").String(), `Credential::"invalid"`},
		{"credential-control-char", CredentialUID("open\x01ai", "provider_call").String(), `Credential::"invalid"`},
		{"credential-nul", CredentialUID("openai", "purpose\x00bad").String(), `Credential::"invalid"`},
		{"credential-ok", CredentialUID("openai", "provider_call").String(), `Credential::"openai::provider_call"`},

		// BashCommand
		{"bash-empty", BashCommandUID("").String(), `BashCommand::"invalid"`},
		{"bash-traversal", BashCommandUID("../bash").String(), `BashCommand::"invalid"`},
		{"bash-control", BashCommandUID("git\nstatus").String(), `BashCommand::"invalid"`},
		{"bash-nul", BashCommandUID("git\x00status").String(), `BashCommand::"invalid"`},
		{"bash-ok", BashCommandUID("git status").String(), `BashCommand::"git status"`},

		// FilesystemOp
		{"fs-empty", FilesystemOpUID("").String(), `FilesystemOp::"invalid"`},
		{"fs-traversal-leading", FilesystemOpUID("../etc/passwd").String(), `FilesystemOp::"invalid"`},
		{"fs-control", FilesystemOpUID("/tmp/\x07a").String(), `FilesystemOp::"invalid"`},
		{"fs-nul", FilesystemOpUID("/tmp/a\x00b").String(), `FilesystemOp::"invalid"`},
		{"fs-ok", FilesystemOpUID("/Users/alec/notes.md").String(), `FilesystemOp::"/Users/alec/notes.md"`},

		// Tool (universal shape)
		{"tool-empty", PermissionToolUID("").String(), `Tool::"invalid"`},
		{"tool-traversal", PermissionToolUID("..bad").String(), `Tool::"invalid"`},
		{"tool-control", PermissionToolUID("kaneaz__\tbash").String(), `Tool::"invalid"`},
		{"tool-nul", PermissionToolUID("kaneaz__bash\x00").String(), `Tool::"invalid"`},
		{"tool-ok", PermissionToolUID("kaneaz__bash").String(), `Tool::"kaneaz__bash"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q want %q", tc.got, tc.want)
			}
		})
	}
}

// TestPopulateFamilyContext_NonFamilyAction is a regression guard: the
// agent-kernel-graph callers (tool_exec, file_read, model_select,
// memory_write) MUST keep their existing nil-context behaviour. The
// helper short-circuits on non-family actions so these stay unaffected.
func TestPopulateFamilyContext_NonFamilyAction(t *testing.T) {
	t.Parallel()
	in := map[cedarlib.String]cedarlib.Value(nil)
	out := populateFamilyContext(ActionToolExec, ToolUID("filesystem", "list"), in)
	if out != nil {
		t.Fatalf("non-family action should leave context untouched, got %v", out)
	}
}

// =====================================================================
// WP10 — MCPRecipe resource family tests.
// =====================================================================

// TestMCPRecipeUID_Shape asserts the EntityUID constructor produces the
// canonical MCPRecipe::"<id>" shape and validates malformed ids.
func TestMCPRecipeUID_Shape(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
		want string
	}{
		{"ok-simple", "github", `MCPRecipe::"github"`},
		{"ok-hyphen", "brave-search", `MCPRecipe::"brave-search"`},
		{"empty-id", "", `MCPRecipe::"invalid"`},
		{"leading-dotdot", "../evil", `MCPRecipe::"invalid"`},
		{"control-char", "my\x01recipe", `MCPRecipe::"invalid"`},
		{"nul-byte", "recipe\x00bad", `MCPRecipe::"invalid"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := MCPRecipeUID(tc.id).String()
			if got != tc.want {
				t.Fatalf("MCPRecipeUID(%q) = %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}

// TestEvaluate_MCPRecipe_DefaultPermit verifies that the default embedded
// policy set produces NotApplicable (= default-allow) for add_recipe and
// spawn_recipe because no MCPRecipe-specific rules are in the embedded bundle.
func TestEvaluate_MCPRecipe_DefaultPermit(t *testing.T) {
	t.Parallel()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	cases := []struct {
		name      string
		action    string
		recipeID  string
		command   string
		transport string
		want      Outcome
	}{
		{"add-stdio-npx-default", ActionAddRecipe, "my-recipe", "npx", "stdio", NotApplicable},
		{"spawn-stdio-node-default", ActionSpawnRecipe, "my-recipe", "node", "stdio", NotApplicable},
		{"add-http-recipe-default", ActionAddRecipe, "brave-search", "", "http", NotApplicable},
		{"spawn-sse-recipe-default", ActionSpawnRecipe, "slack", "", "sse", NotApplicable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				tc.action,
				MCPRecipeUID(tc.recipeID),
				map[cedarlib.String]cedarlib.Value{
					cedarlib.String(CtxKeyRecipeID):        cedarlib.String(tc.recipeID),
					cedarlib.String(CtxKeyRecipeCommand):   cedarlib.String(tc.command),
					cedarlib.String(CtxKeyRecipeTransport): cedarlib.String(tc.transport),
				},
			)
			if d.Outcome != tc.want {
				t.Fatalf("%s on %q (cmd=%q, transport=%q): outcome=%s want %s",
					tc.action, tc.recipeID, tc.command, tc.transport, d.Outcome, tc.want)
			}
		})
	}
}

// TestEvaluate_MCPRecipe_NoNpxPolicy installs the mcp-no-npx policy text
// inline and asserts:
//   - add_recipe / spawn_recipe on an npx recipe → Deny
//   - add_recipe / spawn_recipe on a non-npx recipe → Allow (from base permit)
//   - add_recipe / spawn_recipe with empty command → Allow (not npx)
func TestEvaluate_MCPRecipe_NoNpxPolicy(t *testing.T) {
	t.Parallel()

	// We compose a policy that (a) permits everything by default
	// and (b) forbids npx-spawned recipes — mirroring what mcp-no-npx.cedar
	// does when combined with a user's base permit.
	const noNpxPolicy = `
permit (principal, action, resource);

forbid (
    principal == User::"local",
    action in [Action::"add_recipe", Action::"spawn_recipe"],
    resource is MCPRecipe
) when {
    context.recipe_command == "npx"
};
`
	e, err := NewEngine(Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("no-npx.cedar", []byte(noNpxPolicy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	cases := []struct {
		name      string
		action    string
		recipeID  string
		command   string
		transport string
		want      Outcome
	}{
		// npx → Deny for both add and spawn.
		{"add-npx-deny", ActionAddRecipe, "my-mcp", "npx", "stdio", Deny},
		{"spawn-npx-deny", ActionSpawnRecipe, "my-mcp", "npx", "stdio", Deny},
		// Non-npx → Allow (base permit).
		{"add-node-allow", ActionAddRecipe, "my-mcp", "node", "stdio", Allow},
		{"spawn-uvx-allow", ActionSpawnRecipe, "my-mcp", "uvx", "stdio", Allow},
		// HTTP recipe with empty command → Allow (not npx).
		{"add-http-allow", ActionAddRecipe, "brave-search", "", "http", Allow},
		// spawn_recipe on http recipe → Allow.
		{"spawn-http-allow", ActionSpawnRecipe, "brave-search", "", "http", Allow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(
				context.Background(),
				UserUID(),
				tc.action,
				MCPRecipeUID(tc.recipeID),
				map[cedarlib.String]cedarlib.Value{
					cedarlib.String(CtxKeyRecipeID):        cedarlib.String(tc.recipeID),
					cedarlib.String(CtxKeyRecipeCommand):   cedarlib.String(tc.command),
					cedarlib.String(CtxKeyRecipeTransport): cedarlib.String(tc.transport),
				},
			)
			if d.Outcome != tc.want {
				t.Fatalf("%s on %q (cmd=%q): outcome=%s (reason=%s) want %s",
					tc.action, tc.recipeID, tc.command, d.Outcome, d.Reason, tc.want)
			}
		})
	}
}

// TestCheckRecipeAdd_NilGatePermits exercises the nil-gate default-allow path.
func TestCheckRecipeAdd_NilGatePermits(t *testing.T) {
	t.Parallel()
	if err := CheckRecipeAdd(context.Background(), nil, "my-recipe", "npx", "stdio"); err != nil {
		t.Fatalf("nil gate CheckRecipeAdd should permit: %v", err)
	}
	if err := CheckRecipeSpawn(context.Background(), nil, "my-recipe", "npx", "stdio"); err != nil {
		t.Fatalf("nil gate CheckRecipeSpawn should permit: %v", err)
	}
}

// TestCheckRecipeAdd_AllowAllPermits exercises the AllowAll gate.
func TestCheckRecipeAdd_AllowAllPermits(t *testing.T) {
	t.Parallel()
	g := AllowAll{}
	if err := CheckRecipeAdd(context.Background(), g, "my-recipe", "npx", "stdio"); err != nil {
		t.Fatalf("AllowAll CheckRecipeAdd should permit: %v", err)
	}
	if err := CheckRecipeSpawn(context.Background(), g, "my-recipe", "npx", "stdio"); err != nil {
		t.Fatalf("AllowAll CheckRecipeSpawn should permit: %v", err)
	}
}
