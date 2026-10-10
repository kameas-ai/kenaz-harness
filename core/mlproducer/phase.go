package mlproducer

import "strings"

// testCommands are the §3.3 test-runner prefixes, as token sequences.
var testCommands = [][]string{
	{"go", "test"},
	{"pytest"},
	{"npm", "test"},
	{"npm", "run", "test"},
	{"cargo", "test"},
	{"make", "test"},
	{"vitest"},
	{"jest"},
}

func hasTokenPrefix(toks, prefix []string) bool {
	if len(toks) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if toks[i] != p {
			return false
		}
	}
	return true
}

// isTestCommand reports whether a bash command line runs a test suite
// (spec §3.3). Leading env assignments are ignored.
func isTestCommand(cmd string) bool {
	toks := commandTokens(cmd)
	for _, p := range testCommands {
		if hasTokenPrefix(toks, p) {
			return true
		}
	}
	return false
}

// gitSubcommand returns the git subcommand of a command line ("commit",
// "diff", …) or "" when it is not a git invocation. Global options before
// the subcommand (-C <dir>, -c <k=v>, --no-pager, …) are skipped.
func gitSubcommand(cmd string) string {
	toks := commandTokens(cmd)
	if len(toks) == 0 || toks[0] != "git" {
		return ""
	}
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		switch {
		case t == "-C" || t == "-c" || t == "--git-dir" || t == "--work-tree" || t == "--namespace":
			i++ // option with a separate value
		case strings.HasPrefix(t, "-"):
			// flag (or --opt=value)
		default:
			return t
		}
	}
	return ""
}

// inferPhase is the deterministic §3.3 mapping for one tool call. ok is
// false when the call says nothing about the phase.
func inferPhase(tool, cmd string) (Phase, bool) {
	switch tool {
	case toolWriteFile, toolEditFile:
		return PhaseCoding, true
	case toolReadFile, toolListDir, toolGlob, toolGrep, toolWebFetch, toolWebSearch:
		return PhaseExploring, true
	case toolBash:
		if isTestCommand(cmd) {
			return PhaseTesting, true
		}
		switch gitSubcommand(cmd) {
		case "diff", "log", "show":
			return PhaseReviewing, true
		}
	}
	return "", false
}
