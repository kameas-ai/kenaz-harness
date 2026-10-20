package fs

import (
	"context"
	"os"
	"path/filepath"
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

// Linux file names are arbitrary bytes. A path cedar-go cannot parse
// back (invalid UTF-8, U+FFFD) used to be written straight into an
// allow-always snippet, and the unparseable file made the engine fail
// closed. Allow always on such a path now behaves as Allow once.
func TestGate_AllowAlways_UnparseablePathNotPersisted(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		resp PromptResponse
		path string
	}{
		{PromptAllowExact, "/tmp/x\xff.txt"},
		{PromptAllowExact, "/tmp/y\uFFFD.txt"},
		{PromptAllowDirectory, "/tmp/d\xff/f.txt"},
		{PromptAllowDirectory, "/tmp/e\uFFFD/f.txt"},
	} {
		dataDir := t.TempDir()
		policyDir := filepath.Join(dataDir, cedar.PolicyDir)
		g, _ := newTestGate(tc.resp, cedar.NotApplicable, policyDir, false)
		d, err := g.Evaluate(ctx, OpRead, tc.path)
		if err != nil || d.Outcome != cedar.Allow {
			t.Fatalf("%q: err=%v outcome=%s, want Allow (once)", tc.path, err, d.Outcome)
		}
		if ents, _ := os.ReadDir(policyDir); len(ents) != 0 {
			t.Errorf("%q: persisted %d snippet(s); want none", tc.path, len(ents))
		}
		if !g.hasTransientGrant(OpRead, tc.path) {
			t.Errorf("%q: no transient grant; the in-flight approval was lost", tc.path)
		}
		eng, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := eng.UserPolicyLoadError(); err != nil {
			t.Errorf("%q: engine unhealthy: %v", tc.path, err)
		}
	}
}

// Defence in depth: whatever a builder produces, the writer refuses a
// body that does not parse to exactly one policy.
func TestGate_WritePolicySnippet_RejectsInvalidBody(t *testing.T) {
	dir := t.TempDir()
	g, _ := newTestGate(PromptAllowExact, cedar.NotApplicable, dir, false)
	for _, body := range []string{
		buildExactSnippet(OpRead, "/tmp/x\uFFFD"),
		`permit(principal, action, resource == FilesystemOp::"x`,
		buildExactSnippet(OpRead, "/a") + buildExactSnippet(OpRead, "/b"),
	} {
		if err := g.writePolicySnippet("bad.cedar", body); err == nil {
			t.Errorf("writePolicySnippet accepted invalid body:\n%s", body)
		}
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("wrote %d files; want none", len(ents))
	}
}
