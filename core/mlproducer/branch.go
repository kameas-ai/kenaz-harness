package mlproducer

// branch.go — tasks.branch as a length-only placeholder
// (ml-producer-01MLPRD01 WP07 item 3; spec §12 A-15).
//
// The task body's `branch` is "x" repeated once per rune of the
// workspace's current git branch name. The name itself never leaves the
// device; its length is a weak "kind of branch" signal (main vs a long
// feature branch). Detached HEAD, a ref outside refs/heads, or no
// repository → "". The read is a plain file parse of HEAD (no git
// subprocess), refreshed at most once per branchEvery and on a workspace
// change, and consulted whenever a task upsert is encoded (task creation
// included).

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// branchEvery throttles the HEAD read.
const branchEvery = 10 * time.Second

// maxRepoWalk bounds the walk up from the workspace looking for .git.
const maxRepoWalk = 64

// ReadGitBranch returns the current branch name of the git repository
// containing dir ("" when detached, not a repository, or unreadable). It
// walks up from dir to the nearest `.git`, which is a directory (a normal
// checkout) or a file `gitdir: <path>` (a linked worktree or submodule),
// and parses that git dir's HEAD.
func ReadGitBranch(dir string) string {
	if dir == "" {
		return ""
	}
	cur := filepath.Clean(dir)
	for i := 0; i < maxRepoWalk; i++ {
		dotGit := filepath.Join(cur, ".git")
		if fi, err := os.Stat(dotGit); err == nil {
			gitDir := dotGit
			if !fi.IsDir() {
				gitDir = gitDirFromFile(dotGit)
				if gitDir == "" {
					return ""
				}
			}
			return branchFromHEAD(filepath.Join(gitDir, "HEAD"))
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
	return ""
}

// gitDirFromFile parses a `.git` file ("gitdir: <path>", relative paths
// resolved against the file's directory).
func gitDirFromFile(p string) string {
	b, err := readSmall(p)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(firstLine(b))
	rest, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return ""
	}
	gd := strings.TrimSpace(rest)
	if gd == "" {
		return ""
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(filepath.Dir(p), gd)
	}
	return filepath.Clean(gd)
}

// branchFromHEAD parses "ref: refs/heads/<name>"; anything else (a bare
// sha: detached HEAD) is "".
func branchFromHEAD(p string) string {
	b, err := readSmall(p)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(firstLine(b))
	ref, ok := strings.CutPrefix(line, "ref:")
	if !ok {
		return ""
	}
	name, ok := strings.CutPrefix(strings.TrimSpace(ref), "refs/heads/")
	if !ok {
		return ""
	}
	return name
}

// readSmall reads at most 4 KiB (HEAD and .git files are one line).
func readSmall(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// branchPlaceholder is the wire value for a branch name: "x" per rune.
func branchPlaceholder(name string) string {
	return strings.Repeat("x", utf8.RuneCountInString(name))
}

// branchCache is the recorder's throttled branch read. Guarded by the
// recorder's mu (only the worker touches it).
type branchCache struct {
	workspace string
	at        time.Time
	length    string
	valid     bool
}

// branchFor returns the placeholder for workspace's current branch,
// re-reading HEAD when the workspace changed or the cached read is older
// than branchEvery. Caller holds r.mu.
func (r *Recorder) branchFor(workspace string) string {
	now := r.cfg.Now()
	c := &r.branch
	if c.valid && c.workspace == workspace && now.Sub(c.at) < branchEvery && now.Sub(c.at) >= 0 {
		return c.length
	}
	read := r.cfg.GitBranch
	if read == nil {
		read = ReadGitBranch
	}
	name := ""
	if workspace != "" {
		name = read(workspace)
	}
	*c = branchCache{workspace: workspace, at: now, length: branchPlaceholder(name), valid: true}
	return c.length
}
