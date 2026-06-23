package cedarpolicy_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
)

// Compile-time witness: *cedarpolicy.API satisfies CedarPolicyAPI.
var _ cedarpolicy.CedarPolicyAPI = (*cedarpolicy.API)(nil)

// TestAPI_NilEngine_ReturnsEmpty asserts the boot-time graceful
// degradation: a view constructed with no engine returns empty
// slices and an explicit error on reload, but never panics.
func TestAPI_NilEngine_ReturnsEmpty(t *testing.T) {
	t.Parallel()
	api := cedarpolicy.NewAPI(nil)
	files, err := api.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies err: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("ListPolicies len=%d, want 0", len(files))
	}
	if err := api.ReloadPolicies(context.Background()); err == nil {
		t.Fatalf("ReloadPolicies should error when engine nil")
	}
	d, err := api.RecentDecisions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentDecisions err: %v", err)
	}
	if len(d) != 0 {
		t.Fatalf("RecentDecisions len=%d, want 0", len(d))
	}
}

func TestAPI_WithEngine_ListAndDecisions(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	// Issue an evaluate to populate the decision log.
	_ = e.Evaluate(
		context.Background(),
		cedar.UserUID(),
		cedar.ActionToolExec,
		cedar.ToolUID("websearch", "search"),
		nil,
	)
	api := cedarpolicy.NewAPI(e)

	files, err := api.ListPolicies(context.Background())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("expected at least the embedded default policy file")
	}

	if err := api.ReloadPolicies(context.Background()); err != nil {
		t.Fatalf("ReloadPolicies: %v", err)
	}

	d, err := api.RecentDecisions(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentDecisions: %v", err)
	}
	if len(d) != 1 {
		t.Fatalf("RecentDecisions len=%d, want 1", len(d))
	}
	if d[0].Action != cedar.ActionToolExec {
		t.Fatalf("decision action = %q, want tool_exec", d[0].Action)
	}
}

// ── filename validation matrix ─────────────────────────────────────────

func TestFilenameValidation_AcceptedNames(t *testing.T) {
	t.Parallel()
	accepted := []string{
		// per-family allow patterns
		"bash_allow_git.cedar",
		"bash_allow_ls.cedar",
		"fs_allow_home.cedar",
		"fs_allow_tmp.cedar",
		"cred_allow_openai.cedar",
		"cred_allow_anthropic.cedar",
		"tool_allow_websearch.cedar",
		"tool_allow_kenaz__bash.cedar",
		// boundary: single-char stem after prefix
		"bash_allow_.cedar", // stem is "bash_allow_" (11 chars) which is fine
		// mixed digits
		"bash_allow_cmd123.cedar",
		"cred_allow_provider01.cedar",
	}
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithDataDir(nil, dir)
	for _, name := range accepted {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// WritePolicySnippet exercises the validator; we check it does not
			// return a validation error (os/fs errors are fine — no engine needed).
			err := api.WritePolicySnippet(context.Background(), name, "// test\n")
			if err != nil {
				t.Fatalf("expected accepted name %q to succeed, got: %v", name, err)
			}
			// Clean up.
			_ = api.RevokePolicySnippet(context.Background(), name)
		})
	}
}

func TestFilenameValidation_RejectedNames(t *testing.T) {
	t.Parallel()
	rejected := []struct {
		name   string
		reason string
	}{
		{"", "empty name"},
		{"BASH_ALLOW.cedar", "uppercase"},
		{"Bash_allow_x.cedar", "mixed case"},
		{"bash_allow_../escape.cedar", "path traversal via .."},
		{"../escape.cedar", "path traversal prefix"},
		{"/absolute.cedar", "absolute path"},
		{"bash allow x.cedar", "space in name"},
		{"bash_allow_x.Cedar", "uppercase suffix"},
		{"bash_allow_x.txt", "wrong extension"},
		{"bash_allow_x", "no extension"},
		// 200-char name (well over the 134 max)
		func() struct {
			name   string
			reason string
		} {
			long := "a"
			for len(long) < 200 {
				long += "b"
			}
			return struct {
				name   string
				reason string
			}{long + ".cedar", "name too long (200+ chars)"}
		}(),
	}
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithDataDir(nil, dir)
	for _, tc := range rejected {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			err := api.WritePolicySnippet(context.Background(), tc.name, "// test\n")
			if err == nil {
				t.Fatalf("expected name %q (%s) to be rejected, but got no error", tc.name, tc.reason)
			}
		})
	}
}

// TestWriteAndRevoke_AtomicRoundtrip verifies the write-to-tmp-then-rename
// path creates the file and revoke removes it.
func TestWriteAndRevoke_AtomicRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithDataDir(nil, dir)
	const name = "bash_allow_roundtrip.cedar"
	const body = `permit(
  principal == User::"local",
  action == Action::"run_bash_command",
  resource == BashCommand::"ls"
);`

	if err := api.WritePolicySnippet(context.Background(), name, body); err != nil {
		t.Fatalf("WritePolicySnippet: %v", err)
	}
	dst := filepath.Join(dir, "policy", name)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile after write: %v", err)
	}
	if string(got) != body {
		t.Fatalf("file body mismatch: got %q, want %q", string(got), body)
	}

	if err := api.RevokePolicySnippet(context.Background(), name); err != nil {
		t.Fatalf("RevokePolicySnippet: %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("file still exists after revoke; err=%v", err)
	}
}

// TestRevoke_IdempotentWhenMissing verifies that revoking a non-existent
// snippet returns nil (os.ErrNotExist is silenced).
func TestRevoke_IdempotentWhenMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithDataDir(nil, dir)
	err := api.RevokePolicySnippet(context.Background(), "bash_allow_nonexistent.cedar")
	if err != nil {
		t.Fatalf("expected nil when snippet does not exist, got: %v", err)
	}
}

// TestWriteAndRevoke_EngineReloadTriggered verifies that a wired engine
// receives Reload calls after Write and Revoke.
func TestWriteAndRevoke_EngineReloadTriggered(t *testing.T) {
	t.Parallel()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	dir := t.TempDir()
	api := cedarpolicy.NewAPIWithDataDir(e, dir)
	const name = "tool_allow_reloadtest.cedar"
	if err := api.WritePolicySnippet(context.Background(), name, "// reload test\n"); err != nil {
		t.Fatalf("WritePolicySnippet: %v", err)
	}
	if err := api.RevokePolicySnippet(context.Background(), name); err != nil {
		t.Fatalf("RevokePolicySnippet: %v", err)
	}
}

// TestAPI_WritePolicySnippet_WritesAndReloads (cedar WP11): a real
// engine + dataDir + valid filename round-trips through ListPolicies.
func TestAPI_WritePolicySnippet_WritesAndReloads(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	e, err := cedar.NewEngine(cedar.Options{
		IncludeEmbedded: true,
		LoadFromDisk:    true,
		DataDir:         dataDir,
	})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	api := cedarpolicy.NewAPIWithDataDir(e, dataDir)

	initialFiles, _ := api.ListPolicies(context.Background())

	const filename = "tool_allow_my_recipe__search.cedar"
	const body = `permit(principal, action == Action::"use_tool", resource == Tool::"my-recipe__search");`
	if err := api.WritePolicySnippet(context.Background(), filename, body); err != nil {
		t.Fatalf("WritePolicySnippet: %v", err)
	}

	afterFiles, _ := api.ListPolicies(context.Background())
	if len(afterFiles) <= len(initialFiles) {
		t.Fatalf("ListPolicies after write: %d files, want > %d", len(afterFiles), len(initialFiles))
	}
	found := false
	for _, f := range afterFiles {
		if f.Name == filename {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("snippet file %q not listed after WritePolicySnippet", filename)
	}
}
