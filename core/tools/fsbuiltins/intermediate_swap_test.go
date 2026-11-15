package fsbuiltins_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
)

// swapRig is a work tree and a "state" directory inside the harness data
// dir, each with sub/f.txt, plus the gate and tools over them. The gate's
// prompt is the deterministic seam between the check and the I/O: the
// path was resolved and judged before the prompt opened, and the I/O runs
// after it is answered.
type swapRig struct {
	r           *retargetRig
	gate        *corefs.Gate
	proj, state string
}

const (
	publicText = "PUBLIC needle"
	secretText = "SECRET needle"
)

func newSwapRig(t *testing.T) *swapRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("swapping a directory for a symlink needs unix symlink semantics")
	}
	r := newRetargetRig(t)
	s := &swapRig{r: r, proj: filepath.Join(t.TempDir(), "proj"), state: filepath.Join(r.dataDir, "state")}
	for dir, text := range map[string]string{s.proj: publicText, s.state: secretText} {
		if err := os.MkdirAll(filepath.Join(dir, "sub", "deeper"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.gate = corefs.NewGate(corefs.GateOptions{Engine: r.engine, Prompter: &corefs.CedarPrompter{Registry: r.registry}, StateDirs: corefs.HarnessStateDirs(r.dataDir)})
	return s
}

// swapThenAllow answers the next prompt by replacing dir (a directory the
// checked path runs through) with a symlink to target, then allowing once.
func (s *swapRig) swapThenAllow(t *testing.T, dir, target string) {
	s.r.expect(func(p cedar.PendingRequest) {
		if err := os.Rename(dir, dir+".moved"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(target, dir); err != nil {
			t.Error(err)
		}
		_ = s.r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce)
	})
}

func (s *swapRig) allowOnce() {
	s.r.expect(func(p cedar.PendingRequest) { _ = s.r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce) })
}

// stateUntouched fails unless state/sub holds exactly what it started with.
func (s *swapRig) stateUntouched(t *testing.T) {
	t.Helper()
	for _, d := range []string{s.state, filepath.Join(s.state, "sub")} {
		ents, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		want := "sub"
		if filepath.Base(d) == "sub" {
			want = "deeper,f.txt"
		}
		if got := strings.Join(names, ","); got != want {
			t.Errorf("%s holds %q after the refused I/O, want %q", d, got, want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(s.state, "sub", "f.txt")); string(b) != secretText {
		t.Errorf("state file modified: %q", b)
	}
}

func wantPathChanged(t *testing.T, m map[string]any) {
	t.Helper()
	if !isError(m) {
		t.Fatalf("I/O through a directory swapped for a link after the check succeeded: %v", m)
	}
	e, _ := m["error"].(string)
	if !strings.Contains(e, fsbuiltins.ErrPathChanged.Error()) {
		t.Fatalf("error = %q, want %q", e, fsbuiltins.ErrPathChanged.Error())
	}
	if strings.Contains(e, "SECRET") {
		t.Fatalf("error leaks the target's content: %q", e)
	}
}

// Every builtin refuses I/O when an intermediate directory of the checked
// path is swapped for a link to a state directory after the check, and
// nothing is read from or written into the target.
func TestIntermediateDirSwappedAfterCheckIsRefused(t *testing.T) {
	type tc struct {
		name string
		// run calls the tool; swap is the directory to replace.
		run  func(t *testing.T, s *swapRig) map[string]any
		swap func(s *swapRig) string
	}
	proj := func(s *swapRig) string { return s.proj }
	sub := func(s *swapRig) string { return filepath.Join(s.proj, "sub") }
	read := func(t *testing.T, s *swapRig, path string) map[string]any {
		return call(t, fsbuiltins.NewReadFileTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"path": path})
	}
	cases := []tc{
		{"read_file", func(t *testing.T, s *swapRig) map[string]any {
			return read(t, s, filepath.Join(s.proj, "sub", "f.txt"))
		}, proj},
		{"read_file/parent", func(t *testing.T, s *swapRig) map[string]any {
			return read(t, s, filepath.Join(s.proj, "sub", "f.txt"))
		}, sub},
		{"write_file", func(t *testing.T, s *swapRig) map[string]any {
			w := fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: s.gate})
			return call(t, w, map[string]any{"path": filepath.Join(s.proj, "sub", "evil.cedar"), "content": "x"})
		}, proj},
		{"write_file/overwrite", func(t *testing.T, s *swapRig) map[string]any {
			w := fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: s.gate})
			return call(t, w, map[string]any{"path": filepath.Join(s.proj, "sub", "f.txt"), "content": "x"})
		}, proj},
		{"write_file/missing-dirs", func(t *testing.T, s *swapRig) map[string]any {
			w := fsbuiltins.NewWriteFileTool(fsbuiltins.Options{Gate: s.gate})
			return call(t, w, map[string]any{"path": filepath.Join(s.proj, "sub", "new", "evil.cedar"), "content": "x"})
		}, proj},
		{"edit_file", func(t *testing.T, s *swapRig) map[string]any {
			target := filepath.Join(s.proj, "sub", "f.txt")
			rs := fsbuiltins.NewReadSet()
			rs.Add("", target)
			e := fsbuiltins.NewEditFileTool(fsbuiltins.Options{Gate: s.gate, ReadSet: rs})
			return call(t, e, map[string]any{"path": target, "old_str": "needle", "new_str": "pwned"})
		}, proj},
		{"list_dir", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewListDirTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"path": filepath.Join(s.proj, "sub")})
		}, proj},
		{"list_dir/root", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewListDirTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"path": filepath.Join(s.proj, "sub")})
		}, sub},
		{"list_dir/recursive", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewListDirTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"path": filepath.Join(s.proj, "sub"), "recursive": true})
		}, proj},
		{"glob", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewGlobTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": filepath.Join(s.proj, "sub", "*.txt")})
		}, proj},
		{"grep", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": filepath.Join(s.proj, "sub")})
		}, proj},
		{"grep/file", func(t *testing.T, s *swapRig) map[string]any {
			return call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": filepath.Join(s.proj, "sub", "f.txt")})
		}, proj},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSwapRig(t)
			s.swapThenAllow(t, c.swap(s), s.state)
			m := c.run(t, s)
			wantPathChanged(t, m)
			s.stateUntouched(t)
			if n := len(s.r.prompts()); n != 1 {
				t.Fatalf("prompts = %d, want 1", n)
			}
		})
	}
}

// A directory inside the tree swapped for a link while grep walks it is
// refused, not entered. The grep's own prompt for an in-tree link (the
// link's target is judged as a read of its own) is the seam: by then the
// walk has listed the tree, and the swapped subdirectory sorts after it.
func TestGrep_SubdirSwappedMidWalkIsRefused(t *testing.T) {
	s := newSwapRig(t)
	root := filepath.Join(s.proj, "sub")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "a-link.txt")); err != nil {
		t.Fatal(err)
	}
	s.allowOnce() // grep's own root
	s.swapThenAllow(t, filepath.Join(root, "deeper"), filepath.Join(s.state, "sub"))
	m := call(t, fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate}), map[string]any{"pattern": "needle", "path": root})
	wantPathChanged(t, m)
	s.stateUntouched(t)
}

// With no swap, every builtin works on the same tree through the real gate.
func TestNoSwap_EveryToolWorks(t *testing.T) {
	s := newSwapRig(t)
	rs := fsbuiltins.NewReadSet()
	o := fsbuiltins.Options{Gate: s.gate, ReadSet: rs}
	f := filepath.Join(s.proj, "sub", "f.txt")
	steps := []struct {
		name string
		tool toolloop.BuiltinTool
		args map[string]any
		want string
	}{
		{"read_file", fsbuiltins.NewReadFileTool(o), map[string]any{"path": f}, publicText},
		{"edit_file", fsbuiltins.NewEditFileTool(o), map[string]any{"path": f, "old_str": "PUBLIC", "new_str": "EDITED"}, ""},
		{"write_file", fsbuiltins.NewWriteFileTool(o), map[string]any{"path": filepath.Join(s.proj, "sub", "new", "w.txt"), "content": "written needle"}, ""},
		{"list_dir", fsbuiltins.NewListDirTool(o), map[string]any{"path": filepath.Join(s.proj, "sub")}, "f.txt"},
		{"list_dir/recursive", fsbuiltins.NewListDirTool(o), map[string]any{"path": s.proj, "recursive": true}, "w.txt"},
		{"glob", fsbuiltins.NewGlobTool(o), map[string]any{"pattern": filepath.Join(s.proj, "**", "*.txt")}, "w.txt"},
		{"grep", fsbuiltins.NewGrepTool(o), map[string]any{"pattern": "needle", "path": s.proj}, "written needle"},
	}
	for _, st := range steps {
		s.allowOnce()
		m := call(t, st.tool, st.args)
		if isError(m) {
			t.Fatalf("%s: %v", st.name, m["error"])
		}
		b, _ := jsonOf(m)
		if !strings.Contains(b, st.want) {
			t.Fatalf("%s returned %s, want it to contain %q", st.name, b, st.want)
		}
	}
	if b, _ := os.ReadFile(f); string(b) != "EDITED needle" {
		t.Fatalf("edit result = %q", b)
	}
	s.stateUntouched(t)
}

func jsonOf(m map[string]any) (string, error) {
	b, err := json.Marshal(m)
	return string(b), err
}
