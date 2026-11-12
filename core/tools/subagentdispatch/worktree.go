package subagentdispatch

// isolation:"worktree" (fleet-workbench-next-plan-2026-11-10 B11).
//
// A worktree-isolated sub-agent gets its own `git worktree` of the
// workspace repository at <root>/.worktrees/<agent-id>, on a new branch
// agent/<agent-id> cut from the workspace's current HEAD. The handoff
// prompt tells the worker to make every change inside that directory and
// commit it on that branch. Nothing here merges: the dispatching agent (the
// hub) or the user merges agent/<agent-id> — exactly like a human
// reviewing a colleague's branch.
//
// On completion the worktree is removed when it is clean (its commits stay
// reachable on the branch; a branch with no commits beyond the base is
// deleted too). A worktree with uncommitted changes is LEFT IN PLACE and
// its path reported, so no work is ever thrown away.
//
// Fail closed: no git binary, or a root that is not inside a git repository,
// refuses the dispatch with a clear error — it never silently degrades to a
// shared, un-isolated workspace.
//
// git runs through os/exec with an argument vector (no shell), so an agent
// id or path can never be interpreted as shell syntax; ids are generated
// here and are [a-z0-9-] only.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WorktreesDirName is the directory under the workspace root that holds
// sub-agent worktrees.
const WorktreesDirName = ".worktrees"

// gitFallbackPaths are tried when git is not on the process PATH. A NixOS
// service PATH never contains /run/current-system/sw/bin, but an app that
// declares requiredTools = ["git"] (vscode-server, the dev toolchain) puts
// git there.
var gitFallbackPaths = []string{"/run/current-system/sw/bin/git", "/usr/bin/git", "/usr/local/bin/git"}

// ErrWorktreeUnavailable wraps every reason worktree isolation cannot be
// provided.
var ErrWorktreeUnavailable = errors.New("worktree isolation unavailable")

// Worktree is one created sub-agent worktree.
type Worktree struct {
	AgentID    string `json:"agent_id"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseCommit string `json:"base_commit"`

	repoRoot string
}

// WorktreeOutcome reports what Finish did.
type WorktreeOutcome struct {
	// Removed is true when the worktree directory was removed.
	Removed bool `json:"removed"`
	// Dirty is true when uncommitted changes kept the worktree in place.
	Dirty bool `json:"dirty,omitempty"`
	// BranchKept is true when agent/<id> has commits to merge.
	BranchKept bool `json:"branch_kept"`
	// Error is a cleanup failure (the worktree is then left in place).
	Error string `json:"error,omitempty"`
}

// WorktreeManager creates and finishes sub-agent worktrees.
type WorktreeManager interface {
	Create(ctx context.Context) (Worktree, error)
	Finish(ctx context.Context, wt Worktree) WorktreeOutcome
}

// GitWorktrees is the os/exec-backed WorktreeManager rooted at the
// workspace directory.
type GitWorktrees struct {
	// Root is the workspace directory (/workspace in a workbench).
	Root string
	// Git overrides git binary resolution (tests). Empty = resolve.
	Git string
	// Timeout bounds each git invocation. Zero = 60s.
	Timeout time.Duration
}

func (g *GitWorktrees) gitBinary() (string, error) {
	if g.Git != "" {
		return g.Git, nil
	}
	if p, err := exec.LookPath("git"); err == nil {
		return p, nil
	}
	for _, p := range gitFallbackPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: git is not installed in this workbench (add an app that provides git, e.g. the dev toolchain, or use isolation='session')", ErrWorktreeUnavailable)
}

func (g *GitWorktrees) run(ctx context.Context, git, dir string, args ...string) (string, error) {
	to := g.Timeout
	if to <= 0 {
		to = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(cctx, git, append([]string{"-C", dir}, args...)...)
	// Never prompt (credential helpers, editors) — there is no terminal.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

func newAgentID() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sa-" + hex.EncodeToString(b[:]), nil
}

// Create makes <root>/.worktrees/<agent-id> on branch agent/<agent-id>.
func (g *GitWorktrees) Create(ctx context.Context) (Worktree, error) {
	if g == nil || g.Root == "" {
		return Worktree{}, fmt.Errorf("%w: no workspace directory", ErrWorktreeUnavailable)
	}
	git, err := g.gitBinary()
	if err != nil {
		return Worktree{}, err
	}
	if st, serr := os.Stat(g.Root); serr != nil || !st.IsDir() {
		return Worktree{}, fmt.Errorf("%w: workspace %s is not a directory", ErrWorktreeUnavailable, g.Root)
	}
	top, err := g.run(ctx, git, g.Root, "rev-parse", "--show-toplevel")
	if err != nil {
		return Worktree{}, fmt.Errorf("%w: %s is not a git repository (run `git init` there, or use isolation='session')", ErrWorktreeUnavailable, g.Root)
	}
	base, err := g.run(ctx, git, g.Root, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Worktree{}, fmt.Errorf("%w: the repository at %s has no commits yet (commit once, or use isolation='session')", ErrWorktreeUnavailable, top)
	}
	id, err := newAgentID()
	if err != nil {
		return Worktree{}, fmt.Errorf("%w: agent id: %v", ErrWorktreeUnavailable, err)
	}
	dir := filepath.Join(g.Root, WorktreesDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Worktree{}, fmt.Errorf("%w: create %s: %v", ErrWorktreeUnavailable, dir, err)
	}
	g.excludeWorktreesDir(ctx, git)
	wt := Worktree{AgentID: id, Path: filepath.Join(dir, id), Branch: "agent/" + id, BaseCommit: base, repoRoot: g.Root}
	if _, err := g.run(ctx, git, g.Root, "worktree", "add", "-b", wt.Branch, wt.Path, base); err != nil {
		return Worktree{}, fmt.Errorf("%w: %v", ErrWorktreeUnavailable, err)
	}
	return wt, nil
}

// excludeWorktreesDir keeps .worktrees/ out of the main checkout's
// `git status` via the repository-local info/exclude (never the tracked
// .gitignore). Best-effort.
func (g *GitWorktrees) excludeWorktreesDir(ctx context.Context, git string) {
	common, err := g.run(ctx, git, g.Root, "rev-parse", "--git-common-dir")
	if err != nil {
		return
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(g.Root, common)
	}
	excl := filepath.Join(common, "info", "exclude")
	line := "/" + WorktreesDirName + "/"
	if top, terr := g.run(ctx, git, g.Root, "rev-parse", "--show-prefix"); terr == nil && top != "" {
		line = "/" + strings.TrimSuffix(top, "/") + "/" + WorktreesDirName + "/"
	}
	if b, rerr := os.ReadFile(excl); rerr == nil && strings.Contains(string(b), line) {
		return
	}
	_ = os.MkdirAll(filepath.Dir(excl), 0o755)
	f, err := os.OpenFile(excl, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n# kenaz sub-agent worktrees\n" + line + "\n")
}

// Finish removes a clean worktree (and its branch when it has no new
// commits); a dirty one is left in place and reported.
func (g *GitWorktrees) Finish(ctx context.Context, wt Worktree) WorktreeOutcome {
	git, err := g.gitBinary()
	if err != nil {
		return WorktreeOutcome{Error: err.Error(), BranchKept: true}
	}
	root := wt.repoRoot
	if root == "" {
		root = g.Root
	}
	status, err := g.run(ctx, git, wt.Path, "status", "--porcelain")
	if err != nil {
		return WorktreeOutcome{Error: err.Error(), BranchKept: true}
	}
	if status != "" {
		return WorktreeOutcome{Dirty: true, BranchKept: true}
	}
	tip, _ := g.run(ctx, git, wt.Path, "rev-parse", "HEAD")
	if _, err := g.run(ctx, git, root, "worktree", "remove", wt.Path); err != nil {
		return WorktreeOutcome{Error: err.Error(), BranchKept: true}
	}
	out := WorktreeOutcome{Removed: true, BranchKept: true}
	if tip != "" && tip == wt.BaseCommit {
		if _, err := g.run(ctx, git, root, "branch", "-D", wt.Branch); err == nil {
			out.BranchKept = false
		}
	}
	return out
}

// worktreeHandoff is prepended to a worktree-isolated worker's prompt.
func worktreeHandoff(wt Worktree) string {
	return fmt.Sprintf("You are working in an isolated git worktree at %s on branch %s "+
		"(cut from %s). Make every file change inside that directory, run commands from it, "+
		"and commit your work on that branch before you finish — uncommitted changes keep the "+
		"worktree on disk for a human to inspect. Do not modify files outside it. You do not "+
		"merge: the dispatching agent or the user merges %s.\n\n---\n\n",
		wt.Path, wt.Branch, shortSHA(wt.BaseCommit), wt.Branch)
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
