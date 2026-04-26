package bash

// DefaultAllowlist is the set of safe-by-default commands the bash
// tool permits (FR-012). It is intentionally conservative: read-only
// inspection tools, common interpreters, and language-level build
// tooling. Anything destructive (rm, dd, kill, mv) is omitted on
// purpose; users who want it can extend the per-installation list via
// Settings.BashAllowlist.
var DefaultAllowlist = []string{
	"ls", "cat", "head", "tail", "grep", "find", "wc", "file", "stat",
	"du", "df", "which", "type", "echo", "pwd", "env", "date", "uname",
	"git", "python", "python3", "node", "go", "cargo", "npm", "npx",
	"make", "gcc", "clang", "ruby", "rustc",
}

// Allows reports whether name (the basename of argv[0]) appears in
// allowlist. Match is exact-name; no globbing, no path traversal.
// Callers MUST pass a basename — the allowlist is checked BEFORE
// exec.LookPath so a planted binary at "../bin/rm" cannot bypass it
// (NFR-005).
//
// An empty allowlist denies every command.
func Allows(allowlist []string, name string) bool {
	if name == "" {
		return false
	}
	for _, allowed := range allowlist {
		if allowed == name {
			return true
		}
	}
	return false
}
