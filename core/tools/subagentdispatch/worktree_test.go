package subagentdispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/tools/subagentdispatch"
)

func needGit(t *testing.T) string {
	t.Helper()
	g, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	return g
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

func call(t *testing.T, tool *subagentdispatch.Tool, args string) map[string]any {
	t.Helper()
	raw, err := tool.Call(ctxWithTestSession(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGitWorktrees_FailClosed(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	// Not a git repository.
	_, err := (&subagentdispatch.GitWorktrees{Root: t.TempDir()}).Create(ctx)
	if !errors.Is(err, subagentdispatch.ErrWorktreeUnavailable) || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("non-repo err = %v", err)
	}
	// git absent.
	_, err = (&subagentdispatch.GitWorktrees{Root: initRepo(t), Git: "/nonexistent/git"}).Create(ctx)
	if !errors.Is(err, subagentdispatch.ErrWorktreeUnavailable) {
		t.Fatalf("missing git err = %v", err)
	}
}

func TestGitWorktrees_CleanRemovedDirtyKept(t *testing.T) {
	needGit(t)
	ctx := context.Background()
	repo := initRepo(t)
	m := &subagentdispatch.GitWorktrees{Root: repo}

	// Clean, no commits: worktree and branch both removed.
	wt, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wt.Path, filepath.Join(repo, ".worktrees")+string(filepath.Separator)) || wt.Branch != "agent/"+wt.AgentID {
		t.Fatalf("worktree = %+v", wt)
	}
	if st := git(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf(".worktrees shows in the main checkout's status: %q", st)
	}
	out := m.Finish(ctx, wt)
	if !out.Removed || out.BranchKept || out.Dirty {
		t.Fatalf("clean outcome = %+v", out)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still there: %v", err)
	}

	// Committed work: worktree removed, branch kept for the hub to merge.
	wt2, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt2.Path, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, wt2.Path, "add", ".")
	git(t, wt2.Path, "commit", "-qm", "work")
	out = m.Finish(ctx, wt2)
	if !out.Removed || !out.BranchKept {
		t.Fatalf("committed outcome = %+v", out)
	}
	if got := git(t, repo, "branch", "--list", wt2.Branch); got == "" {
		t.Fatal("branch with commits was deleted")
	}

	// Uncommitted work: left in place.
	wt3, err := m.Create(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt3.Path, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = m.Finish(ctx, wt3)
	if out.Removed || !out.Dirty {
		t.Fatalf("dirty outcome = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(wt3.Path, "b.txt")); err != nil {
		t.Fatalf("dirty worktree content gone: %v", err)
	}
}

func TestDispatch_WorktreeIsolation(t *testing.T) {
	needGit(t)
	repo := initRepo(t)
	seam := agentgraph.NewFakeBranchSeam()
	tool := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(), Seam: seam,
		Worktrees: &subagentdispatch.GitWorktrees{Root: repo},
	})

	// Sync: worktree created, prompt points at it, removed after completion.
	out := call(t, tool, `{"profile":"implementer","prompt":"do it","isolation":"worktree","run_in_background":false}`)
	wt, _ := out["worktree"].(map[string]any)
	if out["status"] != "complete" || wt == nil {
		t.Fatalf("result = %+v", out)
	}
	path, _ := wt["path"].(string)
	if !strings.Contains(seam.Forks[0].HandoffPrompt, path) || !strings.Contains(seam.Forks[0].HandoffPrompt, "do it") {
		t.Fatalf("handoff prompt does not name the worktree: %q", seam.Forks[0].HandoffPrompt)
	}
	oc, _ := wt["outcome"].(map[string]any)
	if oc == nil || oc["removed"] != true {
		t.Fatalf("outcome = %+v", wt)
	}

	// Async: worktree survives until the run is done.
	out = call(t, tool, `{"profile":"implementer","prompt":"bg","isolation":"worktree"}`)
	wt, _ = out["worktree"].(map[string]any)
	path, _ = wt["path"].(string)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("async worktree missing: %v", err)
	}
	seam.Forks[1].OnRunDone(nil)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("async worktree not removed on completion: %v", err)
	}

	// Not a git repo: refused, nothing forked.
	tool2 := subagentdispatch.New(subagentdispatch.Options{
		DataDir: t.TempDir(), Seam: seam,
		Worktrees: &subagentdispatch.GitWorktrees{Root: t.TempDir()},
	})
	n := len(seam.Forks)
	out = call(t, tool2, `{"profile":"implementer","prompt":"x","isolation":"worktree"}`)
	if out["error"] != "worktree_unavailable" || len(seam.Forks) != n {
		t.Fatalf("non-repo result = %+v forks %d->%d", out, n, len(seam.Forks))
	}
}

func warnings(out map[string]any) []any {
	w, _ := out["warnings"].([]any)
	return w
}

func TestDispatch_OverlapWarning(t *testing.T) {
	seam := agentgraph.NewFakeBranchSeam()
	tool := subagentdispatch.New(subagentdispatch.Options{DataDir: t.TempDir(), Seam: seam})

	a := call(t, tool, `{"profile":"implementer","prompt":"a","paths":["internal/foo"]}`)
	if len(warnings(a)) != 0 {
		t.Fatalf("first writer warned: %+v", a)
	}
	// Disjoint: no warning.
	if b := call(t, tool, `{"profile":"implementer","prompt":"b","paths":["internal/bar"]}`); len(warnings(b)) != 0 {
		t.Fatalf("disjoint warned: %+v", b)
	}
	// Overlapping (nested): warning.
	c := call(t, tool, `{"profile":"implementer","prompt":"c","paths":["internal/foo/sub"]}`)
	if len(warnings(c)) != 1 {
		t.Fatalf("overlap not warned: %+v", c)
	}
	// Read-only profiles never warn, even on the whole workspace.
	if d := call(t, tool, `{"profile":"explore","prompt":"d"}`); len(warnings(d)) != 0 {
		t.Fatalf("read-only warned: %+v", d)
	}
	// Once the first writer is done, its scope no longer conflicts.
	seam.Forks[0].OnRunDone(nil)
	seam.Forks[2].OnRunDone(nil)
	if e := call(t, tool, `{"profile":"implementer","prompt":"e","paths":["internal/foo"]}`); len(warnings(e)) != 0 {
		t.Fatalf("finished writer still conflicts: %+v", e)
	}
}
