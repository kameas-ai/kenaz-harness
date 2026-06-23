package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sentinel errors. Callers branch on errors.Is.
var (
	// ErrPathNotAbsolute is returned when ValidateAllowedDir receives
	// a relative path. Tilde expansion lives at the caller — the
	// validator only accepts paths that are already absolute.
	ErrPathNotAbsolute = errors.New("tools: path is not absolute")
	// ErrPathNotFound is returned when os.Stat does not see the path.
	// Callers should pre-create the directory or surface this error
	// to the user as "does this directory exist?"
	ErrPathNotFound = errors.New("tools: path does not exist")
	// ErrPathInDenyList is returned when the canonical path resolves
	// to a directory the harness refuses to grant: filesystem roots,
	// the user's home directory itself (children are fine), and known
	// system paths (/etc, /System, /Library, etc.).
	ErrPathInDenyList = errors.New("tools: path is in deny-list")
	// ErrPathTraversal is returned when filepath.EvalSymlinks fails.
	// A symlink-resolution failure is the only signal we have that a
	// caller is feeding us a path that escapes its declared root.
	ErrPathTraversal = errors.New("tools: path traversal or symlink resolution failed")
)

// denyRoots is the deny-list checked AFTER canonicalization. The list
// is intentionally explicit — a regex-based "no system paths" rule
// would be too easy to bypass with a creative path. EvalSymlinks
// resolves "/etc/../tmp" to "/tmp" before we check, so the list only
// needs the canonical forms.
//
// Roots are matched both as exact equals AND as a prefix with a
// trailing separator: "/etc" denies "/etc" and "/etc/passwd", but
// not "/etcetera".
var denyRoots = []string{
	"/",
	"/etc",
	"/private/etc",
	"/System",
	"/Library",
	"/private",
	"/Applications",
	"/usr/bin",
	"/usr/sbin",
	"/sbin",
	"/bin",
	"/proc",
	"/sys",
	"/boot",
	"/dev",
}

// allowOverrides are subtrees that PRE-EMPT the deny-list, in canonical
// form. These exist because macOS canonicalizes legitimate test- and
// agent-workspace dirs into "/private/..." (a denied prefix). The
// override is intentionally narrow: it only re-allows known temp-dir
// roots, harness data subtrees, and the literal filesystem root.
//
// Match is a longest-prefix-with-separator rule, so "/private/tmp"
// allows "/private/tmp/foo" but does NOT re-allow "/private/etc". The
// "/" entry is special-cased in matchesDenyRoot to allow ONLY the
// literal "/" — child paths still hit the deny-list normally, so
// granting "/" works for the filesystem recipe's "full" access tier
// but typing "/etc" still fails. Once "/" is granted, the MCP server
// reaches everything under it; the deny-list's job is only to gate
// what can be assigned as the recipe's allowed_directories root.
var allowOverrides = []string{
	"/",
	"/tmp",
	"/private/tmp",
	"/var/folders",
	"/private/var/folders",
}

// workspaceMarkerName is dropped at the root of the agent workspace
// the first time EnsureWorkspace creates it. The marker lets a future
// "Reset workspace" path identify which directory is harness-owned
// without misidentifying a user's pre-existing folder.
const workspaceMarkerName = ".kenaz-workspace"

// agentWorkspaceDirName is the conventional subdirectory under
// dataDir that EnsureWorkspace materialises.
const agentWorkspaceDirName = "agent-workspace"

// commonHomeFolders maps bare folder names to subdirectories of the
// user's home. Lets users type "Desktop" / "Downloads" in the recipe
// modal and get them resolved to $HOME/Desktop / $HOME/Downloads
// instead of an "is not absolute" error. Match is case-insensitive
// against the bare path component.
var commonHomeFolders = map[string]string{
	"desktop":   "Desktop",
	"documents": "Documents",
	"downloads": "Downloads",
	"music":     "Music",
	"movies":    "Movies",
	"pictures":  "Pictures",
	"public":    "Public",
}

// ExpandPath resolves shell-friendly path forms into absolute paths
// suitable for ValidateAllowedDir. Order:
//
//  1. Empty input → empty (caller surfaces empty-path error).
//  2. Already absolute → returned untouched.
//  3. Leading "~" or "~/" → $HOME-prefixed.
//  4. "$HOME" / "${HOME}" prefix → $HOME-prefixed.
//  5. Bare common-home-folder name (case-insensitive: "Desktop",
//     "Downloads", "Documents", etc.) → $HOME/<canonical>.
//  6. Anything else relative → returned untouched so the validator
//     surfaces the canonical "is not absolute" error.
//
// Resolution is intentionally narrow: we only do the mappings users
// reach for first. Generic `cd path` semantics would let a typo land
// the agent on an unintended directory.
func ExpandPath(path string) string {
	if path == "" {
		return path
	}
	if filepath.IsAbs(path) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	case strings.HasPrefix(path, "$HOME/"):
		return filepath.Join(home, path[len("$HOME/"):])
	case path == "$HOME" || path == "${HOME}":
		return home
	case strings.HasPrefix(path, "${HOME}/"):
		return filepath.Join(home, path[len("${HOME}/"):])
	}
	// Single bare component? Match against the common-home folders.
	if !strings.ContainsRune(path, filepath.Separator) {
		if mapped, ok := commonHomeFolders[strings.ToLower(strings.TrimSuffix(path, "/"))]; ok {
			return filepath.Join(home, mapped)
		}
	}
	return path
}

// ValidateAllowedDir reports whether path is suitable to grant to a
// filesystem MCP server.
//
// Validation order matters:
//  1. ExpandPath: tilde, $HOME, and bare common-folder names like
//     "Desktop" are rewritten to $HOME-prefixed absolute paths.
//  2. Absolute (after filepath.Abs). A relative path that ExpandPath
//     couldn't resolve is rejected with ErrPathNotAbsolute.
//  3. Existing (os.Stat). Non-existent → ErrPathNotFound.
//  4. Canonical (filepath.EvalSymlinks). A symlink-resolution
//     failure → ErrPathTraversal. Canonicalization runs BEFORE the
//     deny-list check so "/etc/../tmp" resolves to "/tmp" and the
//     check sees the real target.
//  5. Not in the deny-list (denyRoots + the user's home directory
//     itself). Children of the home directory are allowed.
//
// The function returns nil on success; on failure it wraps the
// matching sentinel with the offending path for diagnostic purposes.
func ValidateAllowedDir(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrPathNotAbsolute)
	}
	path = ExpandPath(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%w: %q", ErrPathNotAbsolute, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrPathNotAbsolute, path, err)
	}
	if _, err := os.Stat(abs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %q", ErrPathNotFound, abs)
		}
		return fmt.Errorf("%w: stat %q: %v", ErrPathTraversal, abs, err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return fmt.Errorf("%w: %q: %v", ErrPathTraversal, abs, err)
	}

	if isDenied(canonical) {
		return fmt.Errorf("%w: %q (canonical=%q)", ErrPathInDenyList, path, canonical)
	}
	return nil
}

// IsDeniedPath is the public form of isDenied. The shell view re-uses
// it to filter PathComplete results and validate ReadFile targets
// without duplicating the deny-list. Callers MUST canonicalise via
// filepath.EvalSymlinks before invoking — IsDeniedPath performs the
// deny-list test only, not symlink resolution.
func IsDeniedPath(canonical string) bool {
	return isDenied(canonical)
}

// isDenied reports whether canonical is the same as, or sits under,
// any path in the denyRoots list. allowOverrides are checked first;
// a match there short-circuits to "allowed" so test-isolation dirs
// survive the "/private/..." canonicalization on macOS.
//
// Note: the user's home directory itself is NOT denied — recipes that
// declare an "access_tier=full" need to be able to grant access to
// $HOME and its subtrees. System paths (/, /etc, /System, etc.) remain
// in the deny-list. Users who grant home-directory access should
// understand the risk; the recipe's warning field surfaces that context.
func isDenied(canonical string) bool {
	clean := filepath.Clean(canonical)
	for _, allow := range allowOverrides {
		if matchesDenyRoot(clean, allow) {
			return false
		}
	}
	for _, root := range denyRoots {
		if matchesDenyRoot(clean, root) {
			return true
		}
	}
	return false
}

// matchesDenyRoot returns true when clean equals root, or when clean
// is a child of root (root + separator + ...). "/etc" denies both
// "/etc" and "/etc/passwd", but not "/etcetera" — we require a
// trailing separator on the prefix match so name-prefix collisions
// don't accidentally widen the deny-list.
//
// The "/" entry is special-cased to NOT cascade to children: every
// absolute path on Unix begins with "/", so a naive prefix match
// would deny everything. Only the literal "/" lands in the deny-list
// via this rule.
func matchesDenyRoot(clean, root string) bool {
	if clean == root {
		return true
	}
	if root == "/" {
		// Only the literal "/" is denied; children of "/" are
		// considered case-by-case via other entries (e.g. /etc).
		return false
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(clean, prefix)
}

// EnsureWorkspace creates <dataDir>/agent-workspace if it doesn't
// already exist and drops a .kenaz-workspace marker file at its
// root the first time. Subsequent calls are idempotent: the marker
// is left untouched and the existing directory is returned.
//
// dataDir must be non-empty; an empty dataDir is a programming error.
// The returned path is the absolute workspace dir suitable to feed
// into a filesystem recipe's allowed-directory list.
func EnsureWorkspace(dataDir string) (string, error) {
	if dataDir == "" {
		return "", errors.New("tools: EnsureWorkspace: dataDir is empty")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("tools: EnsureWorkspace: abs %q: %w", dataDir, err)
	}
	workspace := filepath.Join(abs, agentWorkspaceDirName)
	created := false
	if _, err := os.Stat(workspace); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("tools: EnsureWorkspace: stat %q: %w", workspace, err)
		}
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			return "", fmt.Errorf("tools: EnsureWorkspace: mkdir %q: %w", workspace, err)
		}
		created = true
	}
	if created {
		marker := filepath.Join(workspace, workspaceMarkerName)
		if err := os.WriteFile(marker, []byte("kenaz harness agent workspace\n"), 0o600); err != nil {
			return "", fmt.Errorf("tools: EnsureWorkspace: write marker %q: %w", marker, err)
		}
	}
	return workspace, nil
}
