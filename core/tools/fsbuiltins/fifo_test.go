//go:build unix

package fsbuiltins_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
)

// within fails the test if f has not returned after d (a blocked open).
func within(t *testing.T, d time.Duration, f func() map[string]any) map[string]any {
	t.Helper()
	done := make(chan map[string]any, 1)
	go func() { done <- f() }()
	select {
	case m := <-done:
		return m
	case <-time.After(d):
		t.Fatal("tool blocked on a FIFO")
		return nil
	}
}

// read_file on a FIFO is refused promptly rather than blocking.
func TestReadFile_FIFORefusedPromptly(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	read := fsbuiltins.NewReadFileTool(opts(nil))
	m := within(t, 5*time.Second, func() map[string]any { return call(t, read, map[string]any{"path": p}) })
	if !isError(m) {
		t.Fatalf("read_file of a FIFO = %v, want a refusal", m)
	}
}

// A regular file swapped for a FIFO after grep listed the tree (the
// prompt for an in-tree link out of the tree is the seam) is skipped
// promptly, and so is a link whose target became a FIFO.
func TestGrep_FIFOSwappedInDoesNotBlock(t *testing.T) {
	s := newSwapRig(t)
	root := filepath.Join(s.proj, "sub")
	outDir := t.TempDir()
	out := filepath.Join(outDir, "out.txt")
	if err := os.WriteFile(out, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, "a-link.txt")); err != nil {
		t.Fatal(err)
	}
	z := filepath.Join(root, "z.txt")
	if err := os.WriteFile(z, []byte("needle"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.allowOnce() // grep's root
	s.r.expect(func(p cedar.PendingRequest) {
		for _, f := range []string{z, out} {
			if err := os.Remove(f); err != nil {
				t.Error(err)
			}
			if err := syscall.Mkfifo(f, 0o600); err != nil {
				t.Error(err)
			}
		}
		_ = s.r.registry.Resolve(p.RequestID, cedar.DecisionAllowOnce)
	})
	grep := fsbuiltins.NewGrepTool(fsbuiltins.Options{Gate: s.gate})
	m := within(t, 5*time.Second, func() map[string]any {
		return call(t, grep, map[string]any{"pattern": "needle", "path": root})
	})
	if isError(m) {
		t.Fatal(m["error"])
	}
	if got := matchCount(m); got != 1 { // f.txt only
		t.Fatalf("matches = %d, want 1", got)
	}
}
