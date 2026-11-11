package fsbuiltins_test

// Fleet e2e 2026-11-07 regressions E2E-2 (write/read/list/glob share one
// filesystem view; a write that did not land is a failure) and E2E-4
// (glob prompts for its literal prefix, not "/").

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
)

// recordingPrompter allows once and records every prompted path.
type recordingPrompter struct {
	mu    sync.Mutex
	paths []string
}

func (p *recordingPrompter) Prompt(_ context.Context, s corefs.PromptSurface) (corefs.PromptResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paths = append(p.paths, string(s.Op)+" "+s.CanonicalPath)
	return corefs.PromptAllowOnce, nil
}

func (p *recordingPrompter) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.paths...)
}

func gatedOpts(t *testing.T) (fsbuiltins.Options, *recordingPrompter) {
	t.Helper()
	engine, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	p := &recordingPrompter{}
	return fsbuiltins.Options{
		Gate:    corefs.NewGate(corefs.GateOptions{Engine: engine, Prompter: p}),
		ReadSet: fsbuiltins.NewReadSet(),
	}, p
}

// E2E-4: glob(pattern=<root>/*) prompts for read <root>, not for the
// working directory (which is "/" for the served harness).
func TestE2E4_GlobPromptsForLiteralPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "kenaz", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"top.txt", "kenaz/a.txt", "kenaz/deep/b.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o, p := gatedOpts(t)
	tool := fsbuiltins.NewGlobTool(o)

	m := call(t, tool, map[string]any{"pattern": root + "/*"})
	if isError(m) {
		t.Fatalf("glob error: %v", m["error"])
	}
	if got := p.snapshot(); len(got) != 1 || got[0] != "read "+root {
		t.Fatalf("prompts = %v, want exactly [read %s]", got, root)
	}
	matches, _ := m["matches"].([]any)
	if len(matches) != 1 || matches[0] != filepath.Join(root, "top.txt") {
		t.Fatalf("matches = %v, want only the top-level file", matches)
	}

	// Literal leading segments narrow further: <root>/kenaz/**/*.txt
	// asks for <root>/kenaz.
	m = call(t, tool, map[string]any{"pattern": root + "/kenaz/**/*.txt"})
	if isError(m) {
		t.Fatalf("glob error: %v", m["error"])
	}
	if got := p.snapshot(); got[len(got)-1] != "read "+filepath.Join(root, "kenaz") {
		t.Fatalf("last prompt = %q, want read %s/kenaz", got[len(got)-1], root)
	}
	if matches, _ := m["matches"].([]any); len(matches) != 2 {
		t.Fatalf("matches = %v, want a.txt and deep/b.txt", matches)
	}

	// A relative pattern with base_dir still narrows by its literal part.
	_ = call(t, tool, map[string]any{"pattern": "kenaz/*.txt", "base_dir": root})
	if got := p.snapshot(); got[len(got)-1] != "read "+filepath.Join(root, "kenaz") {
		t.Fatalf("last prompt = %q, want read %s/kenaz", got[len(got)-1], root)
	}
}

// E2E-2: with a temp HOME, a write through "~" lands where read_file,
// list_dir and glob — and a shell's "~" — look for it.
func TestE2E2_WriteReadListGlobShareOneView(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := opts(fsbuiltins.NewReadSet())

	w := call(t, fsbuiltins.NewWriteFileTool(o), map[string]any{"path": "~/e2e-status-page/index.html", "content": "<h1>ok</h1>"})
	if isError(w) {
		t.Fatalf("write error: %v", w["error"])
	}
	want := filepath.Join(home, "e2e-status-page", "index.html")
	if w["path"] != want {
		t.Fatalf("write landed at %v, want %s (HOME)", w["path"], want)
	}
	if _, err := os.Stat(filepath.Join(mustGetwd(t), "~")); err == nil {
		t.Fatalf("write created a literal ./~ directory")
	}

	r := call(t, fsbuiltins.NewReadFileTool(o), map[string]any{"path": want})
	if isError(r) || !strings.Contains(r["content"].(string), "ok") {
		t.Fatalf("read back = %v", r)
	}
	l := call(t, fsbuiltins.NewListDirTool(o), map[string]any{"path": "~/e2e-status-page"})
	if isError(l) {
		t.Fatalf("list_dir error: %v", l["error"])
	}
	if entries, _ := l["entries"].([]any); len(entries) != 1 {
		t.Fatalf("list_dir entries = %v, want the written file", entries)
	}
	g := call(t, fsbuiltins.NewGlobTool(o), map[string]any{"pattern": "~/e2e-status-page/*"})
	if matches, _ := g["matches"].([]any); len(matches) != 1 || matches[0] != want {
		t.Fatalf("glob matches = %v, want [%s]", g["matches"], want)
	}
}

// E2E-2: a write that cannot land must fail — and the failure must reach
// the chat as a failed tool call, not as "OK".
func TestE2E2_WriteThatCannotLandFails(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "home")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"path": filepath.Join(blocker, "kenaz", "index.html"), "content": "x"})
	out, err := fsbuiltins.NewWriteFileTool(opts(nil)).Call(context.Background(), raw)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !toolloop.ResultFlagsError(out) {
		t.Fatalf("write into an impossible path did not flag is_error: %s", out)
	}
	ls := call(t, fsbuiltins.NewListDirTool(opts(nil)), map[string]any{"path": filepath.Join(blocker, "kenaz")})
	if !isError(ls) {
		t.Fatalf("list_dir found a directory the failed write claims to have created")
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return wd
}
