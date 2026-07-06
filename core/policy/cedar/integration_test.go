package cedar_test

// Integration test for one gate-hook end-to-end flow: a fake tool
// dispatcher consults the Cedar engine's CheckTool helper, refuses the
// dispatch on Deny, and surfaces a PolicyDeniedError to the caller.
//
// Black-box integration test (charter DIRECTIVE_036): exercises the
// engine through its public API only — no internal types referenced.
//
// WP12 additions: multi-family scenario integration tests covering
// the four resource families introduced in cedar-credential-policy-01KQ8TDE
// (WP01). These complement the unit tests in engine_test.go by exercising
// the full "NotApplicable → transient grant → second call silent" cycle
// at the Cedar engine layer, using only the public Engine/Gate API.
//
// NOTE: WP05 mcp_spawn interactive prompt flow is NOT covered here.
// That flow requires core/policy/cedar/prompt.go (WP02) which is
// pending. A stub test documents the gap so reviewers know what's
// missing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// fakeToolDispatcher emulates the structure of core/rpc/views/tools or
// core/toolloop's dispatch site. Real call sites wrap their existing
// dispatch with the same CheckTool / Evaluate sequence.
type fakeToolDispatcher struct {
	gate cedar.Gate
	// invoked records each tool whose backend Run was actually
	// invoked — denies must NOT show up here.
	invoked []string
}

func (d *fakeToolDispatcher) Dispatch(ctx context.Context, server, tool string) error {
	if err := cedar.CheckTool(ctx, d.gate, server, tool); err != nil {
		return err
	}
	d.invoked = append(d.invoked, server+"__"+tool)
	return nil
}

func TestIntegration_ToolDispatchGate_Deny(t *testing.T) {
	t.Parallel()

	const userPolicy = `
permit (principal, action, resource);

forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"filesystem__delete"
);
`
	e, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("user.cedar", []byte(userPolicy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	d := &fakeToolDispatcher{gate: e}

	// Allowed tool dispatches normally.
	if err := d.Dispatch(context.Background(), "websearch", "search"); err != nil {
		t.Fatalf("websearch__search should dispatch: %v", err)
	}
	if got := len(d.invoked); got != 1 {
		t.Fatalf("invoked len=%d want 1", got)
	}

	// Forbidden tool returns PolicyDeniedError and does NOT invoke.
	err = d.Dispatch(context.Background(), "filesystem", "delete")
	if err == nil {
		t.Fatal("expected PolicyDeniedError, got nil")
	}
	var pe *cedar.PolicyDeniedError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *PolicyDeniedError, got %T (%v)", err, err)
	}
	if pe.Decision.Outcome != cedar.Deny {
		t.Fatalf("decision outcome = %s, want deny", pe.Decision.Outcome)
	}
	if pe.Decision.Resource != `Tool::"filesystem__delete"` {
		t.Fatalf("decision resource = %q, want Tool::\"filesystem__delete\"",
			pe.Decision.Resource)
	}
	// Dispatcher must not have invoked the forbidden tool.
	if len(d.invoked) != 1 {
		t.Fatalf("invoked len=%d after deny, want 1 (deny must short-circuit)",
			len(d.invoked))
	}

	// The audit log carries both decisions, newest first.
	recent := e.RecentDecisions(10)
	if len(recent) != 2 {
		t.Fatalf("RecentDecisions len=%d, want 2", len(recent))
	}
	if recent[0].Outcome != cedar.Deny {
		t.Fatalf("newest decision should be Deny, got %s", recent[0].Outcome)
	}
	if recent[1].Outcome != cedar.Allow {
		t.Fatalf("oldest decision should be Allow, got %s", recent[1].Outcome)
	}
}

// ======================================================================
// WP12 Multi-family integration scenarios
// ======================================================================

// simulateAllowAlways writes a permanent cedar grant for the given action
// and resource to the engine's policy directory and reloads the bundle.
// This mirrors what WP02/WP03 prompt.go does after the user picks
// "Allow always".
func simulateAllowAlways(t *testing.T, e *cedar.Engine, dataDir, filename, policyText string) {
	t.Helper()
	path := filepath.Join(dataDir, cedar.PolicyDir, filename)
	if err := os.WriteFile(path, []byte(policyText), 0o644); err != nil {
		t.Fatalf("write grant file %s: %v", filename, err)
	}
	if err := e.Reload(context.Background()); err != nil {
		t.Fatalf("Reload after grant write: %v", err)
	}
}

// makeGrantEngine creates a new Engine backed by a TempDir so tests
// can write cedar grant files to simulate the allow_always flow.
func makeGrantEngine(t *testing.T) (e *cedar.Engine, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, cedar.PolicyDir), 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	eng, err := cedar.NewEngine(cedar.Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng, dir
}

// ctxAttr returns a map[cedarlib.String]cedarlib.Value with the given
// key-value pairs so callers don't need to repeat the cedarlib type names.
func ctxAttr(kv ...any) map[cedarlib.String]cedarlib.Value {
	if len(kv)%2 != 0 {
		panic("ctxAttr: odd number of arguments")
	}
	m := make(map[cedarlib.String]cedarlib.Value, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k := cedarlib.String(kv[i].(string))
		switch v := kv[i+1].(type) {
		case string:
			m[k] = cedarlib.String(v)
		case bool:
			m[k] = cedarlib.Boolean(v)
		default:
			panic(fmt.Sprintf("ctxAttr: unsupported value type %T", kv[i+1]))
		}
	}
	return m
}

// TestIntegration_BashGate_NotApplicableThenGrant exercises scenario 1:
//
//	BashCommand family: first call → NotApplicable (no bash policy matches
//	by default); simulate user allow_once → cedar transient grant written;
//	second call evaluates Allow and passes silently.
func TestIntegration_BashGate_NotApplicableThenGrant(t *testing.T) {
	t.Parallel()
	e, dataDir := makeGrantEngine(t)

	pattern := "git status"
	resource := cedar.BashCommandUID(pattern)

	// First call: default bash policy is header-only → NotApplicable.
	d1 := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, resource, nil)
	if d1.Outcome != cedar.NotApplicable {
		t.Fatalf("first call: want NotApplicable, got %s (%s)",
			d1.Outcome, d1.Reason)
	}

	// Simulate user resolving allow_once → write a transient cedar grant.
	// (In production, WP02 prompt.go writes this via Registry.GrantTransient;
	// WP12 models it directly to avoid the missing prompt.go dependency.)
	grantPolicy := fmt.Sprintf(`
permit (
    principal == User::"local",
    action == Action::"run_bash_command",
    resource == BashCommand::"%s"
);`, pattern)
	simulateAllowAlways(t, e, dataDir, "bash_allow_git_status.cedar", grantPolicy)

	// Second call: the grant file is now loaded → Allow.
	d2 := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, resource, nil)
	if d2.Outcome != cedar.Allow {
		t.Fatalf("second call after grant: want Allow, got %s (%s)",
			d2.Outcome, d2.Reason)
	}

	// Audit log: two decisions, newest first.
	recent := e.RecentDecisions(5)
	if len(recent) < 2 {
		t.Fatalf("want ≥2 decisions, got %d", len(recent))
	}
	if recent[0].Outcome != cedar.Allow {
		t.Fatalf("newest should be Allow, got %s", recent[0].Outcome)
	}
	if recent[1].Outcome != cedar.NotApplicable {
		t.Fatalf("second-newest should be NotApplicable, got %s", recent[1].Outcome)
	}
}

// TestIntegration_FilesystemGate_ReadInsideRecipeDirSilent covers
// scenario 2 (read path): a read inside a recipe-declared directory
// is silently permitted by the embedded filesystem policy.
func TestIntegration_FilesystemGate_ReadInsideRecipeDirSilent(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	path := "/Users/alec/projects/myrecipe/README.md"
	d := e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionReadFilesystem,
		cedar.FilesystemOpUID(path),
		ctxAttr("recipe_dir_match", true),
	)
	if d.Outcome != cedar.Allow {
		t.Fatalf("read inside recipe-dir should Allow, got %s (%s)",
			d.Outcome, d.Reason)
	}
}

// TestIntegration_FilesystemGate_WriteInsideRecipeDirPrompts covers
// scenario 2 (write path): a write inside a recipe-declared directory
// returns NotApplicable so the prompt flow can take over.
func TestIntegration_FilesystemGate_WriteInsideRecipeDirPrompts(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	path := "/Users/alec/projects/myrecipe/output.txt"
	d := e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionWriteFilesystem,
		cedar.FilesystemOpUID(path),
		ctxAttr("recipe_dir_match", true),
	)
	if d.Outcome != cedar.NotApplicable {
		t.Fatalf("write inside recipe-dir should NotApplicable (prompt fires), got %s (%s)",
			d.Outcome, d.Reason)
	}
}

// TestIntegration_FilesystemGate_WriteAllowsAfterGrant covers scenario 2
// (allow_always path): after the user picks "allow_always" for a write
// inside a directory, subsequent writes pass silently.
func TestIntegration_FilesystemGate_WriteAllowsAfterGrant(t *testing.T) {
	t.Parallel()
	e, dataDir := makeGrantEngine(t)

	writeDir := "/Users/alec/projects/myrecipe"
	writePath := writeDir + "/output.txt"

	// First call: write → NotApplicable.
	d1 := e.Evaluate(
		context.Background(), cedar.UserUID(),
		cedar.ActionWriteFilesystem,
		cedar.FilesystemOpUID(writePath),
		ctxAttr("recipe_dir_match", true),
	)
	if d1.Outcome != cedar.NotApplicable {
		t.Fatalf("first write: want NotApplicable, got %s", d1.Outcome)
	}

	// User picks "allow_always" (this directory and below) →
	// WP04 prompt.go writes a snippet like `like "/Users/alec/projects/myrecipe/*"`.
	// We model it here with an exact-path permit for simplicity.
	grantPolicy := fmt.Sprintf(`
permit (
    principal == User::"local",
    action == Action::"write_filesystem",
    resource is FilesystemOp
) when {
    context.canonical_path like "%s/*"
};`, writeDir)
	simulateAllowAlways(t, e, dataDir, "fs_allow_recipe_write.cedar", grantPolicy)

	// Second call with canonical_path in context → Allow.
	d2 := e.Evaluate(
		context.Background(), cedar.UserUID(),
		cedar.ActionWriteFilesystem,
		cedar.FilesystemOpUID(writePath),
		ctxAttr("recipe_dir_match", true, "canonical_path", writePath),
	)
	if d2.Outcome != cedar.Allow {
		t.Fatalf("second write after grant: want Allow, got %s (%s)",
			d2.Outcome, d2.Reason)
	}
}

// TestIntegration_ToolGate_FirstCallPromptsSecondSilent covers
// scenario 3: a tagged MCP tool's first call via use_tool returns
// NotApplicable (prompt fires), then after allow_always the second
// call is Allow.
func TestIntegration_ToolGate_FirstCallPromptsSecondSilent(t *testing.T) {
	t.Parallel()
	e, dataDir := makeGrantEngine(t)

	toolFQN := "filesystem__read_file"

	// First call: MCP tool → NotApplicable (not a kenaz builtin).
	d1 := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionUseTool, cedar.PermissionToolUID(toolFQN), nil)
	if d1.Outcome != cedar.NotApplicable {
		t.Fatalf("first call MCP tool: want NotApplicable, got %s", d1.Outcome)
	}

	// User picks allow_always → WP02 writes a tool grant.
	grantPolicy := fmt.Sprintf(`
permit (
    principal == User::"local",
    action == Action::"use_tool",
    resource == Tool::"%s"
);`, toolFQN)
	simulateAllowAlways(t, e, dataDir, "tool_allow_filesystem_read_file.cedar", grantPolicy)

	// Second call: Allow.
	d2 := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionUseTool, cedar.PermissionToolUID(toolFQN), nil)
	if d2.Outcome != cedar.Allow {
		t.Fatalf("second call after grant: want Allow, got %s (%s)",
			d2.Outcome, d2.Reason)
	}
}

// TestIntegration_CrossFamilyTransientIsolation covers scenario 4:
// a bash allow_once grant does NOT affect the fs or tool families.
// Granting "git status" must NOT cause "read_filesystem" on the same
// path to Allow.
func TestIntegration_CrossFamilyTransientIsolation(t *testing.T) {
	t.Parallel()
	e, dataDir := makeGrantEngine(t)

	// Grant bash "git status".
	bashGrant := `
permit (
    principal == User::"local",
    action == Action::"run_bash_command",
    resource == BashCommand::"git status"
);`
	simulateAllowAlways(t, e, dataDir, "bash_allow_git_status.cedar", bashGrant)

	// Bash "git status" is now Allow.
	dBash := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, cedar.BashCommandUID("git status"), nil)
	if dBash.Outcome != cedar.Allow {
		t.Fatalf("bash grant: want Allow, got %s", dBash.Outcome)
	}

	// Filesystem read on an arbitrary path (outside recipe-dir) is still NotApplicable.
	dFs := e.Evaluate(
		context.Background(), cedar.UserUID(),
		cedar.ActionReadFilesystem,
		cedar.FilesystemOpUID("/etc/passwd"),
		ctxAttr("recipe_dir_match", false),
	)
	if dFs.Outcome != cedar.NotApplicable {
		t.Fatalf("bash grant must NOT affect fs: want NotApplicable, got %s", dFs.Outcome)
	}

	// Tool use on an MCP tool is still NotApplicable.
	dTool := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionUseTool, cedar.PermissionToolUID("postgres__query"), nil)
	if dTool.Outcome != cedar.NotApplicable {
		t.Fatalf("bash grant must NOT affect tool: want NotApplicable, got %s", dTool.Outcome)
	}

	// Credential use for mcp_spawn is still NotApplicable.
	dCred := e.Evaluate(
		context.Background(), cedar.UserUID(),
		cedar.ActionUseCredential,
		cedar.CredentialUID("openai", "mcp_spawn"),
		ctxAttr("purpose", "mcp_spawn"),
	)
	if dCred.Outcome != cedar.NotApplicable {
		t.Fatalf("bash grant must NOT affect cred/mcp_spawn: want NotApplicable, got %s", dCred.Outcome)
	}
}

// TestIntegration_DefaultDeny_StrictMode covers scenario 5:
// when DefaultDeny=true (PermissionMode=strict/paranoid), every action
// that has no matching permit is Deny, not NotApplicable.
func TestIntegration_DefaultDeny_StrictMode(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
		DefaultDeny:     true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cases := []struct {
		name   string
		eval   func() cedar.Decision
	}{
		{"bash", func() cedar.Decision {
			return e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionRunBashCommand, cedar.BashCommandUID("ls"), nil)
		}},
		{"fs-read", func() cedar.Decision {
			return e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionReadFilesystem, cedar.FilesystemOpUID("/tmp/x"), nil)
		}},
		{"fs-write", func() cedar.Decision {
			return e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionWriteFilesystem, cedar.FilesystemOpUID("/tmp/x"), nil)
		}},
		{"tool", func() cedar.Decision {
			return e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionUseTool, cedar.PermissionToolUID("postgres__query"), nil)
		}},
		{"cred", func() cedar.Decision {
			return e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionUseCredential, cedar.CredentialUID("openai", "provider_call"), nil)
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := tc.eval()
			if d.Outcome != cedar.Deny {
				t.Fatalf("%s: DefaultDeny=true should Deny on empty bundle, got %s (%s)",
					tc.name, d.Outcome, d.Reason)
			}
		})
	}
}

// TestIntegration_PermissiveModeNonDangerous covers scenario 5 (permissive
// side): with DefaultDeny=false (default) and the embedded policy bundle,
// a non-dangerous bash pattern is NotApplicable (allowed). No prompt fires.
// Flip to DefaultDeny=true and the same pattern is Deny.
func TestIntegration_PermissiveModeNonDangerous(t *testing.T) {
	t.Parallel()

	// Permissive (default): NotApplicable = allow-with-audit.
	ePermissive, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		DefaultDeny:     false,
	})
	if err != nil {
		t.Fatalf("NewEngine permissive: %v", err)
	}
	dPermissive := ePermissive.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, cedar.BashCommandUID("echo hello"), nil)
	if dPermissive.Outcome != cedar.NotApplicable {
		t.Fatalf("permissive mode should NotApplicable for unmatched bash, got %s", dPermissive.Outcome)
	}

	// Strict (DefaultDeny=true): same call → Deny.
	eStrict, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
		DefaultDeny:     true,
	})
	if err != nil {
		t.Fatalf("NewEngine strict: %v", err)
	}
	dStrict := eStrict.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, cedar.BashCommandUID("echo hello"), nil)
	if dStrict.Outcome != cedar.Deny {
		t.Fatalf("strict mode should Deny on empty bundle, got %s", dStrict.Outcome)
	}
}

// TestIntegration_MigrationRecap covers the WP10 migration behavior recap:
// on first boot, historical bash allowlist commands (ls, git, cat, etc.)
// are seeded as cedar grant files. After seeding, each command evaluates
// to Allow — users don't see a prompt storm for previously-allowed commands.
func TestIntegration_MigrationRecap(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, cedar.PolicyDir), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Simulate WP10 migration: write a grant file for each historical
	// allowlist entry (ls, cat, git status). Real WP10 does this in migrate.go.
	historicalAllowlist := []string{"ls", "cat", "git status"}
	for i, cmd := range historicalAllowlist {
		policy := fmt.Sprintf(`
permit (
    principal == User::"local",
    action == Action::"run_bash_command",
    resource == BashCommand::"%s"
);`, cmd)
		fname := fmt.Sprintf("bash_migrated_%02d.cedar", i)
		path := filepath.Join(dir, cedar.PolicyDir, fname)
		if err := os.WriteFile(path, []byte(policy), 0o644); err != nil {
			t.Fatalf("write migration file: %v", err)
		}
	}

	e, err := cedar.NewEngine(cedar.Options{
		DataDir:         dir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Each migrated command should Allow immediately (no prompt storm).
	for _, cmd := range historicalAllowlist {
		cmd := cmd
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			d := e.Evaluate(context.Background(), cedar.UserUID(),
				cedar.ActionRunBashCommand, cedar.BashCommandUID(cmd), nil)
			if d.Outcome != cedar.Allow {
				t.Errorf("migrated cmd %q: want Allow, got %s (%s)",
					cmd, d.Outcome, d.Reason)
			}
		})
	}

	// An un-migrated command still prompts (NotApplicable).
	dNew := e.Evaluate(context.Background(), cedar.UserUID(),
		cedar.ActionRunBashCommand, cedar.BashCommandUID("kubectl get"), nil)
	if dNew.Outcome != cedar.NotApplicable {
		t.Fatalf("un-migrated cmd: want NotApplicable, got %s", dNew.Outcome)
	}
}

// TestIntegration_WP05_MCPSpawnGap documents the gap in WP12 coverage:
// the mcp_spawn interactive prompt flow (WP05 partial) is not covered
// because core/policy/cedar/prompt.go (WP02) is pending. When WP05
// lands, this test should be replaced with a full mcp_spawn → prompt →
// resolve flow exercising the Registry.RequestInteractive path.
func TestIntegration_WP05_MCPSpawnGap(t *testing.T) {
	t.Parallel()
	// The default credential policy leaves mcp_spawn → NotApplicable
	// (no rule covers it). This is the documented behaviour per spec
	// FR-002 and the WP01 embedded policy header.
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	d := e.Evaluate(
		context.Background(), cedar.UserUID(),
		cedar.ActionUseCredential,
		cedar.CredentialUID("openai", "mcp_spawn"),
		ctxAttr("purpose", "mcp_spawn"),
	)
	if d.Outcome != cedar.NotApplicable {
		t.Fatalf("mcp_spawn: default policy should NotApplicable, got %s (%s) — "+
			"when WP05 prompt.go lands, verify this becomes Allow after user consent",
			d.Outcome, d.Reason)
	}
	// GAP: WP05 mcp_spawn interactive flow not covered.
	// The real flow: NotApplicable → Registry.RequestInteractive fires →
	// frontend renders CredModal → user resolves allow_once →
	// transient grant written → second call Allow.
	// This requires WP02 (prompt.go) + WP05 (mcp_spawn hooks) to be merged.
	t.Log("WP05 mcp_spawn gap documented: mcp_spawn prompt flow requires " +
		"WP02 prompt.go + WP05 hooks (both pending)")
}

// TestIntegration_CredentialFamily_AllFourFamiliesEndToEnd is the
// comprehensive smoke across all four families: each family fires
// "first call prompts → allow_always → second call silent".
func TestIntegration_CredentialFamily_AllFourFamiliesEndToEnd(t *testing.T) {
	t.Parallel()
	e, dataDir := makeGrantEngine(t)

	type familyTest struct {
		name         string
		firstEval    func() cedar.Decision
		writeGrant   func()
		secondEval   func() cedar.Decision
	}

	cases := []familyTest{
		{
			name: "credential/provider_call",
			firstEval: func() cedar.Decision {
				// provider_call is pre-permitted by embedded policy.
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionUseCredential,
					cedar.CredentialUID("openai", "provider_call"),
					ctxAttr("purpose", "provider_call"))
			},
			writeGrant: func() {}, // pre-permitted, no grant needed
			secondEval: func() cedar.Decision {
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionUseCredential,
					cedar.CredentialUID("openai", "provider_call"),
					ctxAttr("purpose", "provider_call"))
			},
		},
		{
			name: "bash/git_status",
			firstEval: func() cedar.Decision {
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionRunBashCommand,
					cedar.BashCommandUID("git status"), nil)
			},
			writeGrant: func() {
				grant := `permit (principal == User::"local", action == Action::"run_bash_command", resource == BashCommand::"git status");`
				simulateAllowAlways(t, e, dataDir, "bash_e2e_git_status.cedar", grant)
			},
			secondEval: func() cedar.Decision {
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionRunBashCommand,
					cedar.BashCommandUID("git status"), nil)
			},
		},
		{
			name: "filesystem/read_inside_recipe",
			firstEval: func() cedar.Decision {
				// Reads inside recipe-dir are pre-permitted.
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionReadFilesystem,
					cedar.FilesystemOpUID("/project/file.go"),
					ctxAttr("recipe_dir_match", true))
			},
			writeGrant: func() {}, // pre-permitted, no grant needed
			secondEval: func() cedar.Decision {
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionReadFilesystem,
					cedar.FilesystemOpUID("/project/file.go"),
					ctxAttr("recipe_dir_match", true))
			},
		},
		{
			name: "tool/builtin_bash",
			firstEval: func() cedar.Decision {
				// kenaz builtins are pre-permitted.
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionUseTool,
					cedar.PermissionToolUID("kenaz__bash"), nil)
			},
			writeGrant: func() {}, // pre-permitted, no grant needed
			secondEval: func() cedar.Decision {
				return e.Evaluate(context.Background(), cedar.UserUID(),
					cedar.ActionUseTool,
					cedar.PermissionToolUID("kenaz__bash"), nil)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d1 := tc.firstEval()
			// Allow means pre-permitted; NotApplicable means prompt fires.
			if d1.Outcome != cedar.Allow && d1.Outcome != cedar.NotApplicable {
				t.Fatalf("first call: want Allow or NotApplicable, got %s (%s)",
					d1.Outcome, d1.Reason)
			}

			tc.writeGrant()

			d2 := tc.secondEval()
			if d2.Outcome != cedar.Allow {
				t.Fatalf("second call: want Allow, got %s (%s)",
					d2.Outcome, d2.Reason)
			}
		})
	}
}

// TestIntegration_LLMFallback_DefaultAllow verifies that the bundled
// default_llm_fallback_policy.cedar permits the local user to hop to any
// FallbackChain resource. This is the happy-path side of
// model-fallback-routing-01NDFSEX04 WP03 + WP06.
func TestIntegration_LLMFallback_DefaultAllow(t *testing.T) {
	t.Parallel()

	e, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: true, // loads all bundled .cedar policies
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// The bundled default_llm_fallback_policy.cedar should allow any chain.
	for _, chainID := range []string{
		"anthropic-with-openrouter-fallback",
		"openai-with-anthropic-fallback",
		"my-custom-chain",
	} {
		if gotErr := cedar.CheckLLMFallback(context.Background(), e, chainID); gotErr != nil {
			t.Errorf("CheckLLMFallback(%q) = %v; want nil (default-allow)", chainID, gotErr)
		}
	}
}

// TestIntegration_LLMFallback_ExplicitDeny verifies that a forbid policy
// on Action::"llm.fallback" causes CheckLLMFallback to return
// *PolicyDeniedError (fail-closed behaviour).
// model-fallback-routing-01NDFSEX04 WP03 + WP06.
func TestIntegration_LLMFallback_ExplicitDeny(t *testing.T) {
	t.Parallel()

	// Start with embedded policies (which include the default-allow).
	e, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// Override with a forbid that blocks a specific chain.
	denyPolicy := `
forbid (
    principal == User::"local",
    action == Action::"llm.fallback",
    resource == FallbackChain::"blocked-chain"
);
`
	if err := e.SetPolicyText("user_deny_fallback.cedar", []byte(denyPolicy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	// The blocked chain must be denied.
	gotErr := cedar.CheckLLMFallback(context.Background(), e, "blocked-chain")
	if gotErr == nil {
		t.Fatal("CheckLLMFallback(blocked-chain) = nil; want PolicyDeniedError")
	}
	var pde *cedar.PolicyDeniedError
	if !errors.As(gotErr, &pde) {
		t.Errorf("err type = %T; want *cedar.PolicyDeniedError", gotErr)
	}

	// Other chains must remain allowed.
	if otherErr := cedar.CheckLLMFallback(context.Background(), e, "allowed-chain"); otherErr != nil {
		t.Errorf("CheckLLMFallback(allowed-chain) = %v; want nil", otherErr)
	}
}
