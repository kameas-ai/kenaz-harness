// Package toolloop — autonomy prompt-skip sets
// (confirm-each-enforcement-01PMAG05 WP04).
//
// WP01 gave `confirm_each` a real prompt. This file gives the autonomy
// knobs a real way to skip it: a set of *tool families* that bypass the
// confirmation, and a classifier that decides which family a given tool
// belongs to.
//
// Before this, kernelToolAdapter asked "does AutoApproveFamilies contain
// shell-safe or network?" and, if so, skipped the prompt for **every**
// tool. That is not what the knob means. `autoApproveFamilies: [read]`
// should auto-approve reads and still confirm writes. The skip is now
// per-family, per-call.
//
// Two rules constrain everything here:
//
//   - **Explicit Cedar deny is the floor.** The skip set is consulted
//     only for a `confirm_each` verdict. A `deny` verdict short-circuits
//     before any of this runs, under every posture and knob value
//     (FR-005).
//   - **Unclassifiable tools are never skipped.** ClassifyToolFamily
//     returns FamilyUnknown when a tool's name carries no signal, and
//     FamilyUnknown is never a member of a skip set. A tool the harness
//     cannot categorise prompts — an extra confirmation is an
//     annoyance, a wrongly-skipped one is the defect this mission
//     exists to fix.
//
// The family names match autonomy's canonical set (autonomy.FamilyRead
// etc.) as plain strings so this package does not import autonomy; the
// chat adapter builds the set from ResolvedKnobs.
package toolloop

import (
	"sort"
	"strings"
	"unicode"
)

// Canonical tool-family names. The first four mirror
// autonomy.Family{Read,Write,ShellSafe,Network} by value — they are the
// families autoApproveFamilies can name. FamilyDestructive is reachable
// only through destructiveActionPosture, and FamilyUnknown is never
// skippable.
//
// FamilyShellSafe means "shell-shaped and inspection-only" (echo, pwd,
// whoami, …) — NOT "shell". A general-execution tool (bash, sh,
// run_command, execute_sql) classifies FamilyUnknown and always prompts.
// The autonomy tiers that name shell-safe (bold, autonomous) are
// therefore granting the curated surface, not arbitrary execution; see
// shellSafeWholeNames for the list and the reasoning.
const (
	FamilyRead        = "read"
	FamilyWrite       = "write"
	FamilyShellSafe   = "shell-safe"
	FamilyNetwork     = "network"
	FamilyDestructive = "destructive"
	FamilyUnknown     = "unknown"
)

// PromptSkipSet is the set of tool families whose `confirm_each`
// verdicts bypass the confirmation prompt for the current session.
//
// The zero value (nil) skips nothing — every confirm_each verdict
// prompts. That is the correct default for an unconfigured session.
type PromptSkipSet map[string]struct{}

// NewPromptSkipSet builds a set from family names. Unknown or empty
// names are dropped, and FamilyUnknown is rejected outright: "I could
// not classify this tool" must never become "so allow it".
func NewPromptSkipSet(families ...string) PromptSkipSet {
	out := make(PromptSkipSet, len(families))
	for _, f := range families {
		switch f {
		case FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork, FamilyDestructive:
			out[f] = struct{}{}
		}
	}
	return out
}

// Add inserts a family, returning the (possibly new) set so callers can
// chain on a nil receiver. FamilyUnknown and unrecognised names are
// ignored.
func (s PromptSkipSet) Add(family string) PromptSkipSet {
	switch family {
	case FamilyRead, FamilyWrite, FamilyShellSafe, FamilyNetwork, FamilyDestructive:
	default:
		return s
	}
	if s == nil {
		s = make(PromptSkipSet)
	}
	s[family] = struct{}{}
	return s
}

// Has reports whether the family bypasses the prompt.
func (s PromptSkipSet) Has(family string) bool {
	if s == nil || family == "" || family == FamilyUnknown {
		return false
	}
	_, ok := s[family]
	return ok
}

// Sorted returns the member family names in stable lexical order — for
// logging and audit records.
func (s PromptSkipSet) Sorted() []string {
	if len(s) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(s))
	for f := range s {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// SkipsTool reports whether a confirm_each verdict for (server, tool)
// bypasses the prompt under this set. This is the single question the
// dispatch path asks.
func (s PromptSkipSet) SkipsTool(server, tool string) bool {
	return s.Has(ClassifyToolFamily(server, tool))
}

// shellSafeWholeNames is the curated set of tool names that classify as
// FamilyShellSafe — and it is matched against the WHOLE tool name, never
// against a segment.
//
// # Why this is an allowlist and not a token table
//
// The first cut of this file put "exec", "shell", "bash", "sh", "spawn",
// "command", "process", "script" in a *segment* table under the
// shell-safe family. The consequence, found in review: `bash`, `sh`,
// `run_command`, `shell_exec`, `spawn_process` and `execute_sql` all
// classified as shell-**safe**, so the bold and autonomous tiers — which
// name shell-safe in autoApproveFamilies — auto-approved arbitrary shell
// execution and arbitrary SQL without a prompt. "Shell-safe" is not a
// synonym for "shell"; it names the small surface of shell-shaped tools
// whose effect is inspection.
//
// General execution therefore matches NOTHING here and falls to
// FamilyUnknown, which is never skippable: `bash` prompts at every tier.
// The family name stays live (autonomy.FamilyShellSafe is referenced by
// the bold + autonomous presets) because these curated names still
// classify into it — the knob means something, it just no longer means
// "run anything".
//
// Whole-name matching is the second half of the guard: a segment match
// would readmit the hole through `echo_and_delete_everything`.
var shellSafeWholeNames = tokenSet(
	"echo", "pwd", "cwd", "whoami", "hostname", "uname",
	"date", "uptime", "which", "version",
)

// familyTokens maps a normalised name segment to a family. Checked in
// the priority order below, so a token appearing in two buckets resolves
// to the more dangerous one.
//
// FamilyShellSafe is deliberately absent: it is whole-name-only (see
// shellSafeWholeNames) and is checked separately in ClassifyToolFamily.
var familyTokens = []struct {
	family string
	tokens map[string]struct{}
}{
	// Destructive first: "delete_file" is destructive, not a write.
	{FamilyDestructive, tokenSet(
		"delete", "destroy", "drop", "erase", "purge", "remove", "rm",
		"truncate", "unlink", "wipe", "kill", "revoke", "format", "reset",
		"prune", "rmdir", "uninstall")},
	{FamilyNetwork, tokenSet(
		"http", "https", "curl", "download", "browse", "browser", "crawl",
		"scrape", "webhook", "url", "api", "net", "network", "socket",
		"email", "smtp", "dns", "ping")},
	{FamilyWrite, tokenSet(
		"write", "create", "update", "edit", "insert", "append", "save",
		"patch", "mkdir", "rename", "move", "mv", "copy", "cp", "upload",
		"put", "post", "set", "publish", "commit", "apply", "install",
		"add", "replace", "modify")},
	// "query" is deliberately NOT here. `run_query` / `execute_query` /
	// `db_query` are arbitrary-statement tools: a read classification let
	// `DELETE FROM …` skip the prompt at the cautious tier, which
	// auto-approves reads. A tool that runs a statement the caller
	// supplies is unclassifiable by name, and unclassifiable prompts.
	{FamilyRead, tokenSet(
		"read", "get", "list", "ls", "cat", "view", "show", "describe",
		"inspect", "stat", "find", "search", "grep", "head",
		"tail", "count", "info", "status", "tree", "diff", "log", "fetch",
		"lookup", "load", "open", "peek", "summarize")},
}

func tokenSet(names ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// ClassifyToolFamily maps a (server, tool) pair to one of the canonical
// families, or FamilyUnknown when the name carries no signal.
//
// Classification is name-based and deliberately conservative: it reads
// the tool name's segments (splitting on separators and camelCase) and
// matches them against the token tables above in priority order
// destructive → shell-safe → network → write → read. Three consequences
// are intentional:
//
//   - A name matching several families resolves to the most dangerous,
//     so "delete_file" is destructive rather than write, and never
//     bypasses a prompt because the user auto-approved writes.
//   - FamilyShellSafe is reachable only by an exact whole-name match
//     against shellSafeWholeNames. General execution tools ("bash",
//     "run_command", "shell_exec", "spawn_process", "execute_sql") match
//     nothing and are therefore FamilyUnknown.
//   - A name matching nothing is FamilyUnknown and always prompts.
//
// The server name is accepted for future per-server classification (a
// recipe could declare its families); it is not consulted today.
func ClassifyToolFamily(_ /*server*/, tool string) string {
	segments := splitIdentifier(tool)
	if len(segments) == 0 {
		return FamilyUnknown
	}
	// Destructive outranks everything, including the shell-safe
	// allowlist: a tool named "reset" is not made safe by a curated list
	// it does not appear on, and a curated name can never re-enter this
	// branch because the allowlist and the destructive table are
	// disjoint. Checked first so the ordering is explicit rather than
	// incidental.
	for _, seg := range segments {
		if _, ok := familyTokens[0].tokens[seg]; ok {
			return FamilyDestructive
		}
	}
	if _, ok := shellSafeWholeNames[normalizedWholeName(tool)]; ok {
		return FamilyShellSafe
	}
	for _, bucket := range familyTokens[1:] {
		for _, seg := range segments {
			if _, ok := bucket.tokens[seg]; ok {
				return bucket.family
			}
		}
	}
	return FamilyUnknown
}

// normalizedWholeName lowercases and trims a tool name for comparison
// against the shell-safe allowlist. It does NOT split or strip
// separators — that is the point.
//
// The allowlist is matched against the whole name exactly as the server
// reported it (modulo case and surrounding space), so "echo", "Echo" and
// " echo " match while "echo_file", "echo-file" and "myEcho" do not. A
// separator-stripping normaliser would fold "echo_file" to "echofile",
// which still misses — but it would also fold names apart that a reader
// expects to behave alike, and the exactness here is a security property
// rather than a convenience: any name carrying an extra word is a
// different tool and does not inherit the grant.
func normalizedWholeName(tool string) string {
	return strings.ToLower(strings.TrimSpace(tool))
}

// splitIdentifier lowercases and splits a tool name into its parts on
// separators (_ - . / space) and camelCase boundaries. "readFileSync"
// → [read file sync]; "kenaz__save_artifact" → [kenaz save artifact].
func splitIdentifier(name string) []string {
	var (
		out  []string
		cur  strings.Builder
		last rune
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range name {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' || r == ':':
			flush()
		case unicode.IsUpper(r) && unicode.IsLower(last):
			// camelCase boundary.
			flush()
			cur.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
		last = r
	}
	flush()
	return out
}
