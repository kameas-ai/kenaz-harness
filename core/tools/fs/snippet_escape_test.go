package fs

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// Persistent filesystem grants interpolate the canonical path into Cedar
// text. A path is filesystem-controlled (a repo can ship a directory
// named anything), so a backslash, quote or `*` in it must neither break
// the policy file nor widen or inject a grant.
func TestFilesystemSnippets_EscapePath(t *testing.T) {
	ctx := context.Background()
	evil := `/tmp/x\" || true || "`
	for _, tc := range []struct {
		name    string
		body    string
		allowed []string
		denied  []string
	}{
		{"exact_quote_backslash", buildExactSnippet(OpRead, evil), []string{evil}, []string{"/etc/passwd"}},
		{"exact_trailing_backslash", buildExactSnippet(OpRead, `/tmp/a\`), []string{`/tmp/a\`}, []string{"/tmp/a"}},
		{"dir_quote_backslash", buildDirectorySnippet(OpRead, evil), []string{evil + "/f"}, []string{"/etc/passwd"}},
		{"dir_star", buildDirectorySnippet(OpRead, "/tmp/*"), []string{"/tmp/*/f"}, []string{"/tmp/other/f"}},
		{"exact_ordinary", buildExactSnippet(OpRead, "/Users/a/docs/x.txt"), []string{"/Users/a/docs/x.txt"}, []string{"/Users/a/docs/y.txt"}},
		{"dir_ordinary", buildDirectorySnippet(OpRead, "/Users/a/docs"), []string{"/Users/a/docs/x/y"}, []string{"/Users/a/other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eng, err := cedar.NewEngine(cedar.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := eng.SetPolicyText("grant.cedar", []byte(tc.body)); err != nil {
				t.Fatalf("generated snippet does not parse: %v\n%s", err, tc.body)
			}
			for _, p := range tc.allowed {
				if d := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionReadFilesystem, cedar.FilesystemOpUID(p), nil); d.Outcome != cedar.Allow {
					t.Errorf("%q = %v, want Allow\n%s", p, d.Outcome, tc.body)
				}
			}
			for _, p := range tc.denied {
				if d := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionReadFilesystem, cedar.FilesystemOpUID(p), nil); d.Outcome == cedar.Allow {
					t.Errorf("%q = Allow; grant widened\n%s", p, tc.body)
				}
			}
			if d := eng.Evaluate(ctx, cedar.UserUID(), cedar.ActionWriteFilesystem, cedar.FilesystemOpUID("/etc/passwd"), nil); d.Outcome == cedar.Allow {
				t.Errorf("write_filesystem = Allow; read grant injected a write\n%s", tc.body)
			}
		})
	}
}
