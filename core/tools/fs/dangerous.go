package fs

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// homeDir is the resolved user home directory. It is cached after first
// use because os.UserHomeDir performs syscalls that are safe to amortise.
var (
	homeDirOnce sync.Once
	homeDirVal  string
)

// resolvedHome returns the user's home directory, or "" if unavailable.
func resolvedHome() string {
	homeDirOnce.Do(func() {
		h, err := os.UserHomeDir()
		if err == nil {
			homeDirVal = filepath.Clean(h)
		}
	})
	return homeDirVal
}

// expandEntry replaces the leading "~/" (or bare "~") in a
// dangerousEntry.Path with the real home directory. Returns the
// expanded path and whether expansion succeeded.
//
// IMPORTANT: filepath.Join strips trailing slashes. We preserve the
// trailing slash after expansion so that IsDangerousPath's subtree-
// match logic ("entries ending in '/' match any path that starts with
// the prefix") works correctly.
func expandEntry(e dangerousEntry) (string, bool) {
	p := e.Path
	trailingSlash := strings.HasSuffix(p, "/")

	if strings.HasPrefix(p, "~/") {
		home := resolvedHome()
		if home == "" {
			return "", false
		}
		// filepath.Join(home, ".ssh/") → "/Users/…/.ssh" (no slash).
		// We re-apply the trailing slash after joining.
		joined := filepath.Join(home, p[2:])
		if trailingSlash {
			joined += "/"
		}
		return joined, true
	}
	if p == "~" {
		home := resolvedHome()
		if home == "" {
			return "", false
		}
		return home, true
	}
	return p, true
}

// IsDangerousPath reports whether the supplied canonical path falls
// within a system-critical subtree or matches a specific dangerous file.
// canonical MUST have been produced by Canonicalize; passing a raw
// user-supplied path is a programming error (the function will still
// return a result but the match semantics are undefined).
//
// When the function returns true it also returns a human-readable reason
// string sourced from dangerousTable (dangerousEntry.Copy). This copy is
// suitable for display in the hazard-banner of the permission modal.
//
// Matching rules:
//   - Entries with a trailing "/" match any path whose canonical form
//     starts with the expanded prefix.
//   - Entries without a trailing "/" (exact-file entries) match only
//     when canonical == expanded entry path.
//
// Matching is case-sensitive on all platforms; path semantics on
// case-insensitive filesystems are the caller's responsibility (macOS
// APFS is case-insensitive but case-preserving — the gate uses the
// user-typed canonical form verbatim).
func IsDangerousPath(canonical string) (bool, string) {
	if canonical == "" {
		return false, ""
	}
	for _, entry := range dangerousTable {
		expanded, ok := expandEntry(entry)
		if !ok {
			continue
		}
		if strings.HasSuffix(expanded, "/") {
			// Subtree match: canonical must start with the prefix.
			if strings.HasPrefix(canonical, expanded) {
				return true, entry.Copy
			}
			// Also match the prefix directory itself (without trailing
			// slash), e.g. canonical == "/etc" against prefix "/etc/".
			trimmed := strings.TrimSuffix(expanded, "/")
			if canonical == trimmed {
				return true, entry.Copy
			}
		} else {
			// Exact-file match.
			if canonical == expanded {
				return true, entry.Copy
			}
		}
	}
	return false, ""
}
