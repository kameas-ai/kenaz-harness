package contexts_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/contexts"
)

func newLib(t *testing.T) (*contexts.Library, string) {
	t.Helper()
	root := t.TempDir()
	lib, err := contexts.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return lib, root
}

func TestOpenCreatesRoot(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	root := filepath.Join(tmp, "contexts")
	lib, err := contexts.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if lib.Root() == "" {
		t.Fatal("Root empty after Open")
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("root not created: %v", err)
	}
}

func TestOpenRejectsRelativeRoot(t *testing.T) {
	t.Parallel()
	if _, err := contexts.Open("relative/path"); err == nil {
		t.Fatal("expected error on relative root")
	}
}

func TestSaveGetRoundTrip(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.Save("notes/welcome.md", "# hello\n"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := lib.Get("notes/welcome.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "# hello\n" {
		t.Fatalf("Get content mismatch: %q", got)
	}
}

func TestSaveRejectsBadExtensions(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	cases := []string{"foo.exe", "foo.json", "foo", "foo.md.exe"}
	for _, name := range cases {
		err := lib.Save(name, "content")
		if !errors.Is(err, contexts.ErrUnsupportedExt) {
			t.Errorf("Save(%q) = %v; want ErrUnsupportedExt", name, err)
		}
	}
}

func TestSaveRejectsOversize(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	huge := strings.Repeat("x", contexts.MaxFileBytes+1)
	if err := lib.Save("big.md", huge); !errors.Is(err, contexts.ErrFileTooLarge) {
		t.Fatalf("Save oversized = %v; want ErrFileTooLarge", err)
	}
	// Boundary check: exactly MaxFileBytes accepted.
	exact := strings.Repeat("y", contexts.MaxFileBytes)
	if err := lib.Save("ok.md", exact); err != nil {
		t.Fatalf("Save at limit: %v", err)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	bad := []string{
		"../escape.md",
		"../../escape.md",
		"foo/../../escape.md",
		"/etc/passwd",
		"foo/../bar/../../baz.md",
	}
	for _, p := range bad {
		if err := lib.Save(p, "x"); !errors.Is(err, contexts.ErrInvalidPath) {
			t.Errorf("Save(%q) = %v; want ErrInvalidPath", p, err)
		}
		if _, err := lib.Get(p); err == nil {
			t.Errorf("Get(%q) succeeded; expected error", p)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows; covered by darwin/linux runs")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Symlink points OUT of the library root.
	link := filepath.Join(root, "leak")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	lib, err := contexts.Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Reading through the symlinked subtree must not cross the boundary.
	_, err = lib.Get("leak/secret.md")
	if !errors.Is(err, contexts.ErrSymlinkEscape) {
		t.Fatalf("Get through symlink = %v; want ErrSymlinkEscape", err)
	}
	// And neither must Save.
	if err := lib.Save("leak/leaked.md", "x"); !errors.Is(err, contexts.ErrSymlinkEscape) {
		t.Fatalf("Save through symlink = %v; want ErrSymlinkEscape", err)
	}
}

func TestCreateFolder(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	if err := lib.CreateFolder("a/b/c"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "a", "b", "c"))
	if err != nil || !info.IsDir() {
		t.Fatalf("folder not created: %v info=%v", err, info)
	}
	// Idempotent.
	if err := lib.CreateFolder("a/b/c"); err != nil {
		t.Fatalf("CreateFolder idempotent failed: %v", err)
	}
}

func TestRename(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.Save("old.md", "x"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := lib.Rename("old.md", "new.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := lib.Get("old.md"); !errors.Is(err, contexts.ErrNotFound) {
		t.Fatalf("old.md still readable: %v", err)
	}
	if got, err := lib.Get("new.md"); err != nil || got != "x" {
		t.Fatalf("new.md not readable: got=%q err=%v", got, err)
	}
}

func TestRenameRejectsInvalidExt(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.Save("a.md", "x"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := lib.Rename("a.md", "a.exe"); !errors.Is(err, contexts.ErrUnsupportedExt) {
		t.Fatalf("Rename to bad ext = %v; want ErrUnsupportedExt", err)
	}
}

func TestDeleteMovesToTrash(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	if err := lib.Save("doomed.md", "ttyl"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := lib.Delete("doomed.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "doomed.md")); !os.IsNotExist(err) {
		t.Fatal("file still exists at original path")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".trash"))
	if err != nil {
		t.Fatalf("read trash: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("trash entry count = %d; want 1", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), "-doomed.md") {
		t.Fatalf("trash basename %q lacks suffix", entries[0].Name())
	}
}

func TestSweepTrashRetention(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	trashRoot := filepath.Join(root, ".trash")
	if err := os.MkdirAll(trashRoot, 0o700); err != nil {
		t.Fatalf("mkdir trash: %v", err)
	}
	old := filepath.Join(trashRoot, "old.md")
	fresh := filepath.Join(trashRoot, "fresh.md")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed old: %v", err)
	}
	if err := os.WriteFile(fresh, []byte("y"), 0o600); err != nil {
		t.Fatalf("seed fresh: %v", err)
	}
	// Backdate "old" beyond the 30-day TTL.
	past := time.Now().Add(-31 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := lib.SweepTrash(); err != nil {
		t.Fatalf("SweepTrash: %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old not swept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh swept prematurely: %v", err)
	}
}

func TestTreeHidesDotfiles(t *testing.T) {
	t.Parallel()
	lib, root := newLib(t)
	if err := lib.Save("visible.md", "v"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// The library is allowed to drop dotfiles in directly; the tree
	// view filters them so the user's library list stays clean.
	if err := os.WriteFile(filepath.Join(root, ".secret"), []byte("nope"), 0o600); err != nil {
		t.Fatalf("seed dotfile: %v", err)
	}
	tree, err := lib.Tree()
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	for _, c := range tree.Children {
		if strings.HasPrefix(c.Name, ".") {
			t.Errorf("dotfile leaked into tree: %s", c.Name)
		}
	}
}

func TestRecentlyAppliedLRU(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		if err := lib.Save(p, "x"); err != nil {
			t.Fatalf("Save %s: %v", p, err)
		}
	}
	if err := lib.MarkApplied("a.md"); err != nil {
		t.Fatalf("MarkApplied a: %v", err)
	}
	if err := lib.MarkApplied("b.md"); err != nil {
		t.Fatalf("MarkApplied b: %v", err)
	}
	if err := lib.MarkApplied("a.md"); err != nil {
		t.Fatalf("MarkApplied a (dedup): %v", err)
	}
	got := lib.RecentlyApplied(10)
	want := []string{"a.md", "b.md"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RecentlyApplied = %v; want %v", got, want)
	}
}

func TestRecentlyAppliedRejectsMissingPath(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	if err := lib.MarkApplied("missing.md"); !errors.Is(err, contexts.ErrNotFound) {
		t.Fatalf("MarkApplied missing = %v; want ErrNotFound", err)
	}
}

func TestWatcherFiresOnSave(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	w := lib.Subscribe()
	defer w.Close()
	go func() {
		_ = lib.Save("ping.md", "x")
	}()
	select {
	case <-w.Events():
	case <-time.After(time.Second):
		t.Fatal("watcher did not fire on Save")
	}
}

// TestTreePerformance exercises NFR-001 — tree fetch ≤ 50 ms p99 on a
// 100-file / 50-folder fixture. We compute a relaxed p99 (max of 5
// trials) so the assertion is robust to scheduler jitter on CI.
func TestTreePerformance(t *testing.T) {
	t.Parallel()
	lib, _ := newLib(t)
	for i := 0; i < 50; i++ {
		folder := fmt.Sprintf("folder-%02d", i)
		if err := lib.CreateFolder(folder); err != nil {
			t.Fatalf("CreateFolder: %v", err)
		}
	}
	for i := 0; i < 100; i++ {
		path := fmt.Sprintf("folder-%02d/file-%03d.md", i%50, i)
		if err := lib.Save(path, "content"); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	const trials = 5
	durations := make([]time.Duration, 0, trials)
	for i := 0; i < trials; i++ {
		start := time.Now()
		if _, err := lib.Tree(); err != nil {
			t.Fatalf("Tree: %v", err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	worst := durations[len(durations)-1]
	if worst > 50*time.Millisecond {
		t.Fatalf("Tree p99 = %v; budget 50 ms (samples: %v)", worst, durations)
	}
}
