package fsbuiltins_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
)

// stateHome is a home directory that contains the harness state dir
// (<home>/.kenaz/harness/prod, with a secret in it) next to an ordinary
// project, for the recursive tools to walk from the top.
func stateHome(t *testing.T) (s *swapRig, home string) {
	t.Helper()
	s = newSwapRig(t)
	home = filepath.Dir(filepath.Dir(filepath.Dir(s.r.dataDir)))
	if err := os.WriteFile(filepath.Join(s.r.dataDir, "secret.txt"), []byte(secretText), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "proj", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "proj", "deep", "a.txt"), []byte("plain needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	return s, home
}

func wantStateSkipped(t *testing.T, m map[string]any) {
	t.Helper()
	b, _ := json.Marshal(m)
	if isError(m) {
		t.Fatalf("error: %v", m["error"])
	}
	if strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "secret.txt") || strings.Contains(string(b), cedar.PolicyDir+"/") {
		t.Fatalf("result reaches into harness state: %s", b)
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "harness state directory skipped") {
		t.Fatalf("note = %q, want the state skip reported", note)
	}
	if !strings.Contains(string(b), "a.txt") {
		t.Fatalf("the ordinary subdirectory was not walked: %s", b)
	}
}

// grep, glob and recursive list_dir from a root that CONTAINS the state
// dir never read below it, say so, and still walk ordinary directories.
func TestRecursiveTools_SkipHarnessStateBelowRoot(t *testing.T) {
	t.Run("grep", func(t *testing.T) {
		s, home := stateHome(t)
		s.allowOnce()
		wantStateSkipped(t, call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": home}))
	})
	t.Run("glob", func(t *testing.T) {
		s, home := stateHome(t)
		s.allowOnce()
		wantStateSkipped(t, call(t, fsbuiltins.NewGlobTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": filepath.Join(home, "**", "*.txt")}))
	})
	t.Run("list_dir", func(t *testing.T) {
		s, home := stateHome(t)
		s.allowOnce()
		m := call(t, fsbuiltins.NewListDirTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"path": home, "recursive": true})
		wantStateSkipped(t, m)
		ents, _ := m["entries"].([]any)
		for _, e := range ents {
			p, _ := e.(map[string]any)["path"].(string)
			if strings.HasPrefix(p, filepath.Join(".kenaz", "harness")+string(filepath.Separator)) {
				t.Fatalf("listed state entry %s", p)
			}
		}
	})
	t.Run("ordinary root has no note", func(t *testing.T) {
		s, home := stateHome(t)
		s.allowOnce()
		m := call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": filepath.Join(home, "proj")})
		if isError(m) || m["note"] != nil || matchCount(m) != 1 {
			t.Fatalf("grep of an ordinary tree = %v", m)
		}
	})
}

// A link in the tree to a file in harness state is not read on the
// root's judgement even though state sits inside the tree: it goes to the
// gate (here denied) and nothing is read.
func TestGrep_InTreeLinkIntoStateIsJudged(t *testing.T) {
	s, home := stateHome(t)
	if err := os.Symlink(filepath.Join(s.r.dataDir, "secret.txt"), filepath.Join(home, "proj", "l.txt")); err != nil {
		t.Fatal(err)
	}
	s.allowOnce()
	s.r.expect(s.r.deny)
	m := call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": home})
	wantStateSkipped(t, m)
	if n := len(s.r.prompts()); n != 2 {
		t.Fatalf("prompts = %d, want 2 (root + the link into state)", n)
	}
}
