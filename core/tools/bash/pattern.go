package bash

import (
	"path/filepath"
	"strings"
)

// DerivePattern extracts the policy-gate pattern from an argv slice.
//
// Rules (spec FR-014):
//
//  1. argv[0] is normalised to its basename with path stripping so a
//     planted binary at "../bin/git" produces the same pattern as the
//     system "git". Scripts like "./scripts/run.sh" become "run.sh".
//
//  2. argv[1] is appended when it is "subcommand-shaped": a single word
//     with no flag prefix ("-"), no path separators, no spaces, and not
//     the empty string. This captures the common <tool> <subcommand>
//     shape (git status, npm install, go build) without over-matching on
//     literal file paths or option values.
//
//  3. Further arguments are always dropped. The spec locks pattern
//     granularity to argv[0]+argv[1?] for v1.
//
// Pattern derivation is deterministic: the same argv always returns the
// same pattern string. The result is used as the Cedar BashCommand
// resource id and the filename stem of "Allow always" .cedar files.
//
// Empty argv returns the empty string.
func DerivePattern(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	base := filepath.Base(argv[0])
	if base == "" || base == "." {
		return ""
	}

	if len(argv) < 2 {
		return base
	}

	sub := argv[1]
	if isSubcommandShaped(sub) {
		return base + " " + sub
	}
	return base
}

// isSubcommandShaped returns true when s looks like a plain subcommand
// token — a single word with no flag prefix ("-"), no path separators,
// no file-extension dots (to exclude file arguments like "script.py"),
// no spaces, and non-empty. This is the definition the spec uses for
// "argv[1]-if-subcommand-shaped" (FR-014).
func isSubcommandShaped(s string) bool {
	if s == "" {
		return false
	}
	// Flag-prefixed tokens (--verbose, -r, -rf, etc.) are option args,
	// not subcommands.
	if strings.HasPrefix(s, "-") {
		return false
	}
	// Path-like tokens (contains a separator) are not subcommands.
	if strings.ContainsAny(s, string(filepath.Separator)+"/\\") {
		return false
	}
	// Dot-prefixed names that look like a relative path invocation
	// ("./foo") are already rejected by the path-separator check above.
	// Plain dots (e.g. just ".") are not subcommands.
	if s == "." || s == ".." {
		return false
	}
	// Tokens containing a dot look like file names (script.py, main.go,
	// index.js) rather than subcommands. Subcommands in real CLI tools
	// are always plain identifiers with no extension dots.
	if strings.Contains(s, ".") {
		return false
	}
	// URL-like tokens (contain "://") are protocol references, not
	// subcommands.
	if strings.Contains(s, "://") {
		return false
	}
	// Spaces inside the token would only occur if the caller pre-joined
	// arguments, which the parser never does. Guard anyway.
	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}
	return true
}
