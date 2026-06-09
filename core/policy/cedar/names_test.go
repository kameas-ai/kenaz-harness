package cedar_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

func TestValidPolicyName_Accepted(t *testing.T) {
	t.Parallel()
	cases := []string{
		"my-policy.cedar",
		"my_policy.cedar",
		"my.policy.cedar",
		"filesystem-full-recommended.cedar",
		"mcp-no-npx.cedar",
		"default_policy.cedar",
		"default_credential_policy.cedar",
		"a.cedar",
		"ABC123.cedar",
		"Mixed-Case_Policy.cedar",
		"policy123.cedar",
	}
	for _, name := range cases {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := cedar.ValidPolicyName(name); err != nil {
				t.Fatalf("expected %q to be accepted, got: %v", name, err)
			}
		})
	}
}

func TestValidPolicyName_Rejected(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		reason string
	}{
		{"", "empty name"},
		{"../etc/passwd.cedar", "path traversal via .."},
		{"foo/bar.cedar", "embedded slash"},
		{"foo\\bar.cedar", "embedded backslash"},
		{".dotfile.cedar", "leading dot"},
		{"name.cedar ", "trailing space"},
		{"my policy.cedar", "embedded space"},
		{"my\tpolicy.cedar", "embedded tab"},
		{"policy.txt", "wrong extension"},
		{"policy", "no extension"},
		{"policy.Cedar", "uppercase extension"},
		{"/absolute/path.cedar", "absolute path"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			err := cedar.ValidPolicyName(tc.name)
			if err == nil {
				t.Fatalf("expected %q (%s) to be rejected, but got no error", tc.name, tc.reason)
			}
		})
	}
}

func TestValidPolicyName_TooLong(t *testing.T) {
	t.Parallel()
	// build a name > 255 bytes
	long := ""
	for len(long) < 256 {
		long += "a"
	}
	long += ".cedar"
	if err := cedar.ValidPolicyName(long); err == nil {
		t.Fatal("expected long name to be rejected")
	}
}

func TestIsProtectedPolicyName(t *testing.T) {
	t.Parallel()
	protected := []string{
		"default_policy.cedar",
		"default_credential_policy.cedar",
		"default_bash_policy.cedar",
		"default_filesystem_policy.cedar",
		"default_tool_policy.cedar",
		"default_workflows_policy.cedar",
	}
	for _, name := range protected {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !cedar.IsProtectedPolicyName(name) {
				t.Fatalf("expected %q to be protected", name)
			}
		})
	}

	notProtected := []string{
		"filesystem-full-recommended.cedar",
		"mcp-no-npx.cedar",
		"my-policy.cedar",
	}
	for _, name := range notProtected {
		name := name
		t.Run("not-protected-"+name, func(t *testing.T) {
			t.Parallel()
			if cedar.IsProtectedPolicyName(name) {
				t.Fatalf("expected %q to NOT be protected", name)
			}
		})
	}
}

// TestEmbeddedDefaultStillAccessible verifies that the directory embed
// (embed.go) does not break the existing engine construction which relies
// on individual //go:embed directives in engine.go. The PoliciesFS must
// contain the default policy.
func TestEmbeddedDefaultStillAccessible(t *testing.T) {
	t.Parallel()
	data, err := cedar.PoliciesFS.ReadFile("policies/default_policy.cedar")
	if err != nil {
		t.Fatalf("PoliciesFS.ReadFile default_policy.cedar: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("default_policy.cedar is empty in PoliciesFS")
	}
}

func TestPoliciesFS_ContainsFilesystemRecommended(t *testing.T) {
	t.Parallel()
	data, err := cedar.PoliciesFS.ReadFile("policies/filesystem-full-recommended.cedar")
	if err != nil {
		t.Fatalf("PoliciesFS.ReadFile filesystem-full-recommended.cedar: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("filesystem-full-recommended.cedar is empty in PoliciesFS")
	}
}
