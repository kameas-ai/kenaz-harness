// Package fs implements the filesystem gate for mission
// cedar-credential-policy-01KQ8TDE (WP04). It provides:
//
//   - Canonicalize — path sanitisation (no symlink follow, no ..)
//   - IsDangerousPath — two-tier classification of system-critical paths
//   - RecipeDirs — concurrent-safe cache of recipe-declared allowed dirs
//   - Gate — cedar-backed orchestrator (canonicalize → classify → evaluate
//     → prompt on NotApplicable → persist AllowAlways snippets)
//
// All filesystem-path comparisons in the harness MUST go through
// Canonicalize before reaching the cedar engine. Raw user-supplied paths
// are never compared directly.
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package fs

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode"
)

// ErrInvalidPath is returned by Canonicalize when the supplied path
// is rejected before cedar evaluation. Callers MUST propagate this
// error without forwarding the path — a rejected path is evidence of
// an attempted traversal or malformed input.
var ErrInvalidPath = errors.New("fs/canonical: invalid path")

// Canonicalize converts path to an absolute, clean form suitable for
// cedar resource UIDs and direct string comparisons. It applies
// filepath.Abs + filepath.Clean and then rejects the result if:
//
//   - path was empty.
//   - any rune in the input is a C0/C1 control character (< U+0020 or
//     U+007F–U+009F), including NUL and tab.
//   - the cleaned result still contains a ".." segment (e.g. "/../").
//   - the input contains a NUL byte (shell injection guard).
//
// Symlinks are NOT followed (spec: preserve user-stated path semantics;
// symlink dereferencing is a denial-of-service surface). The returned
// string is filepath.Abs + filepath.Clean without any lstat/readlink.
//
// On success the returned path is guaranteed to:
//   - be absolute (starts with filepath.Separator).
//   - contain no ".." segments.
//   - contain no control characters.
func Canonicalize(path string) (string, error) {
	if path == "" {
		return "", ErrInvalidPath
	}
	// Reject control characters in the raw input. filepath.Abs does not
	// strip control chars and Cedar would embed them verbatim in the UID.
	for _, r := range path {
		if r == 0 || unicode.IsControl(r) {
			return "", ErrInvalidPath
		}
	}
	// filepath.Abs calls filepath.Clean internally. We call both
	// explicitly to be explicit about what we want.
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ErrInvalidPath
	}
	clean := filepath.Clean(abs)

	// After cleaning, no segment may be "..". Walk every component.
	// filepath.Clean on an absolute path should remove all "..", but we
	// verify as a defence-in-depth: a platform that returns ".." in a
	// clean path would be a bug in filepath, and we should catch it here.
	for _, seg := range strings.Split(clean, string(filepath.Separator)) {
		if seg == ".." {
			return "", ErrInvalidPath
		}
	}
	return clean, nil
}
