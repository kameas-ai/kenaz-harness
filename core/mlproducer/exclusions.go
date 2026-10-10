package mlproducer

// MATCHING SEMANTICS (normative; kenaz-fleet adopts this text verbatim in
// docs/contract-harness-ml.md "Typed exclusions" — change both together).
// Any ambiguity resolves toward excluding.
//
//  1. paths (globs). Matched on device against the absolute path, before
//     hashing. `/` separates (`\` is folded to `/`). Per segment: `*` any
//     run of non-separator characters, `?` one, `[...]` a class, `\`
//     escapes. A segment that is exactly `**` matches zero or more
//     segments. A leading `~` is the user's home directory. A pattern
//     starting with `/` (or a drive letter `C:/`) is anchored at the root;
//     any other pattern may match starting at any segment (`hr/**` ==
//     `**/hr/**`). A pattern that matches a parent directory excludes
//     everything below it (`/secret` excludes `/secret/a/b`). Matching is
//     case-insensitive. An invalid glob stops the producer (nothing ships)
//     rather than being ignored.
//  2. commands (prefixes). Matched on device against the full command
//     line, before truncation. Whitespace runs collapse to one space and
//     case is folded on both sides. The line is split into simple commands
//     on `;` `&` `|` `(` `)` backticks and newlines; each is tested from
//     its start and again after skipping leading `VAR=value` assignments
//     and the wrappers sudo, env, command, exec, nohup, time, nice,
//     builtin (with their `-flags`). The test is a literal string prefix
//     (`ssh` also matches `sshfs`).
//  3. paths inside commands. Every path-looking token of a command line
//     (contains `/` or `\`, starts with `~` or `.`, or is a bare
//     `name.ext`; quotes and a `--flag=` / `VAR=` prefix stripped;
//     redirect targets included) is resolved against the command's
//     working directory and matched as in 1. A match excludes the command
//     exactly as in 2.
//  4. An excluded file write or command is reported only as "a tool ran"
//     (tool, outcome, duration): no path, no command text, no derived
//     counters (files, tests, commits) and no phase change.
//  5. exclude_browser is a no-op for the harness (it sends no browser
//     data). legacy_exclusion_notes are never matched. An unknown key in
//     `exclusions` is ignored when its value is `[]` or `false`; any other
//     value stops the producer until it is upgraded.

// exclusions.go — on-device org exclusions (ml-producer-01MLPRD01 WP05;
// spec §12 A-11, A-13; SA v1.1 DPA §4; kenaz-fleet
// docs/contract-harness-ml.md "Typed exclusions").
//
// Fleet serves `exclusions: {paths:[glob], commands:[prefix],
// exclude_browser}` on GET /me/ml. The contract: "Matching is on the
// device, before hashing. The harness (producer) applies the lists to each
// event and drops matches before anything is hashed or queued. The server
// does no matching at ingest." The gate's consent read compiles the newest
// lists into an *ExclusionSet and hands it to the Recorder
// (Recorder.SetExclusions), which applies it to every subsequent call.
//
// The contract names the lists ("file-path globs", "terminal command
// prefixes") and gives two path examples (`hr/**`, `**/secrets/*`) but no
// matching rules. The rules below are this producer's, chosen so that an
// ambiguity always resolves toward EXCLUDING: a false positive costs one
// event of ML signal; a false negative ships something a customer
// contractually excluded.
//
// Path globs (matched against the ABSOLUTE path the tool wrote, before
// hashing; the raw argument is checked too):
//
//   - Separators are `/` (`\` folded to `/` on Windows via ToSlash); the
//     pattern and the path are cleaned, and empty / `.` segments dropped.
//   - Per segment: `*` = any run of non-separator characters, `?` = one,
//     `[...]` = a class, `\` escapes (Go path.Match syntax).
//   - A segment that is exactly `**` matches zero or more whole segments.
//   - A leading `~` or `~/` expands to the user's home directory.
//   - A pattern starting with `/` (or a drive letter, `C:/`) is ANCHORED at
//     the filesystem root. Any other pattern is UNANCHORED: it may match
//     starting at any segment, i.e. `hr/**` behaves as `**/hr/**`. An org
//     admin cannot know where members keep their repos, so `hr/**` must
//     catch `/Users/a/acme/hr/plan.md`.
//   - DIRECTORY PREFIXES match: if the pattern matches the path or any of
//     its parent directories, the path is excluded. `/secret`, `/secret/`
//     and `/secret/**` all exclude `/secret/a/b.txt`.
//   - Case-insensitive on every platform (macOS and Windows file systems
//     are, and over-matching on Linux only drops signal).
//   - A pattern that is not a valid glob (an unclosed `[`) fails
//     compilation, which closes the gate (no recording) rather than
//     silently ignoring an exclusion.
//   - Symlinks are not resolved: a write through a link to an excluded
//     directory is matched on the path the agent used (known gap).
//
// Command prefixes (matched against the FULL command line, before the
// two-token truncation the minimiser applies):
//
//   - Both sides are normalised: whitespace runs collapse to one space,
//     leading/trailing whitespace is trimmed, case is folded.
//   - The line is split into simple commands on the shell list / pipe
//     operators `;` `&` `|` (so `&&` `||` too), newlines, `(` `)` and
//     backticks, without quote awareness (a split inside quotes can only
//     add candidates, i.e. over-match). `cd repo && git push` therefore
//     matches `git push`.
//   - Each simple command is tested from its start and again after
//     skipping leading `VAR=value` assignments (as the minimiser's
//     commandTokens does, but quote-aware: `A='x y' git push` skips to
//     `git push`) and the wrapper words sudo / env / command /
//     exec / nohup / time / nice / builtin together with any `-flags`
//     directly after them, so `sudo ssh host` and `FOO=1 ssh host` match
//     `ssh`.
//   - Matching is a literal string prefix: `ssh` also matches `sshfs`
//     (over-match by design: the contract says "prefixes", not "commands").
//
// exclude_browser: the harness never ships browser data (no browser
// collection exists in this producer; spec §1 rule 2), so it is a no-op
// here. It is kept on the set for display.
//
// legacy_exclusion_notes are free text (contract: "never enforced …
// Producers ignore them") and never reach this file.
//
// Path globs inside commands (MatchCommandPaths): a terminal row about an
// excluded file is data about that file (DPA §4), so EVERY token of the
// full command line is scanned before truncation. A token is path-looking
// when it contains `/` or `\`, starts with `~` or `.`, or is a bare
// `name.ext`. Surrounding quotes and a `--flag=` / `VAR=` prefix are
// stripped, `~` is expanded, and a relative token is resolved against the
// bash call's working_dir (relative to the workspace, else the workspace)
// and is ALSO matched as written (unanchored patterns catch it either
// way). Redirect targets count: `<` and `>` split tokens. Any match
// excludes the whole call exactly like a command-prefix match. This
// over-matches by design (an `echo hr/x` is excluded too).

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Exclusions is the fleet-free shape of /me/ml's exclusions (the
// ConsentSource maps fleet.MLExclusions onto it).
type Exclusions struct {
	Paths          []string
	Commands       []string
	ExcludeBrowser bool
	// Version is /me/ml's exclusions_version (informational: the newest
	// lists always apply; a broadening change closes the gate server-side
	// through notice_version, so no version gating happens here).
	Version int
}

// ErrInvalidExclusion is wrapped by CompileExclusions' errors.
var ErrInvalidExclusion = errors.New("mlproducer: exclusion pattern cannot be honoured")

// ExclusionSet is a compiled, immutable Exclusions. The zero value and nil
// exclude nothing.
type ExclusionSet struct {
	version        int
	excludeBrowser bool
	paths          []pathPattern
	commands       []string
	// home expands `~` in command tokens (MatchCommandPaths).
	home string
}

type pathPattern struct {
	segs []string
}

// userHomeDir is swapped by tests.
var userHomeDir = os.UserHomeDir

// CompileExclusions validates and compiles e. home expands `~`; "" uses
// the user's home directory.
func CompileExclusions(e Exclusions, home string) (*ExclusionSet, error) {
	if home == "" {
		if h, err := userHomeDir(); err == nil {
			home = h
		}
	}
	s := &ExclusionSet{version: e.Version, excludeBrowser: e.ExcludeBrowser, home: home}
	for i, raw := range e.Paths {
		p, err := compilePathPattern(raw, home)
		if err != nil {
			return nil, fmt.Errorf("%w: paths[%d]: %v", ErrInvalidExclusion, i, err)
		}
		s.paths = append(s.paths, p)
	}
	for i, raw := range e.Commands {
		c := normCommand(raw)
		if c == "" {
			return nil, fmt.Errorf("%w: commands[%d] is empty", ErrInvalidExclusion, i)
		}
		s.commands = append(s.commands, c)
	}
	return s, nil
}

// Version is the exclusions_version the set was compiled from.
func (s *ExclusionSet) Version() int {
	if s == nil {
		return 0
	}
	return s.version
}

// Empty reports whether the set excludes nothing.
func (s *ExclusionSet) Empty() bool {
	return s == nil || (len(s.paths) == 0 && len(s.commands) == 0)
}

// ---- paths ----

// isAbsSlash reports whether a slash-form path is absolute: `/…` or a
// drive letter `C:/…` / `C:`.
func isAbsSlash(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
}

// expandHome replaces a leading `~` / `~/` with home (slash form). With no
// home known, `~/x` becomes the unanchored `x` (still matched anywhere).
func expandHome(p, home string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/")
	if home == "" {
		return rest
	}
	h := strings.TrimRight(filepath.ToSlash(home), "/")
	if rest == "" {
		return h
	}
	return h + "/" + rest
}

// splitSegs folds case, cleans and splits a slash-form path, dropping
// empty and "." segments. abs reports an anchored (absolute) path.
func splitSegs(p string) (segs []string, abs bool) {
	p = strings.ToLower(p)
	abs = isAbsSlash(p)
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." {
			continue
		}
		segs = append(segs, s)
	}
	return segs, abs
}

func compilePathPattern(raw, home string) (pathPattern, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return pathPattern{}, errors.New("empty pattern")
	}
	p = expandHome(filepath.ToSlash(p), home)
	segs, abs := splitSegs(p)
	for _, s := range segs {
		if s == "**" {
			continue
		}
		if _, err := path.Match(s, ""); err != nil {
			return pathPattern{}, fmt.Errorf("invalid glob segment %q: %v", s, err)
		}
	}
	out := make([]string, 0, len(segs)+2)
	if !abs {
		out = append(out, "**") // unanchored
	}
	out = append(out, segs...)
	out = append(out, "**") // directory prefixes match
	return pathPattern{segs: collapseDoubleStar(out)}, nil
}

func collapseDoubleStar(segs []string) []string {
	out := segs[:0:0]
	for _, s := range segs {
		if s == "**" && len(out) > 0 && out[len(out)-1] == "**" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// matchSegs matches pattern segments (with `**`) against path segments.
func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			rest := pat[1:]
			if len(rest) == 0 {
				return true
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegs(rest, segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// MatchPath reports whether p is excluded. p is the absolute path a tool
// wrote (or its raw relative argument); `~` is expanded in patterns only,
// since tool paths reach the recorder absolute or workspace-relative.
func (s *ExclusionSet) MatchPath(p string) bool {
	if s == nil || len(s.paths) == 0 || strings.TrimSpace(p) == "" {
		return false
	}
	segs, _ := splitSegs(path.Clean(filepath.ToSlash(p)))
	for _, pp := range s.paths {
		if matchSegs(pp.segs, segs) {
			return true
		}
	}
	return false
}

// ---- commands ----

// normCommand collapses whitespace, trims and folds case.
func normCommand(c string) string {
	return strings.ToLower(strings.Join(strings.Fields(c), " "))
}

// commandSplitters separate simple commands in a shell line (not quote
// aware: extra splits only add candidates).
func isCommandSplitter(r rune) bool {
	switch r {
	case ';', '&', '|', '\n', '\r', '(', ')', '`':
		return true
	}
	return false
}

// shellFields splits on unquoted whitespace, keeping quoted runs (single,
// double, backslash-escaped) inside their token, so `A='x y' cmd` yields
// the assignment as ONE token and its skip lands on `cmd`. Quotes are kept
// in the token text; an unbalanced quote runs to the end.
func shellFields(s string) []string {
	var out []string
	var cur strings.Builder
	in := false
	var quote rune
	escaped := false
	flush := func() {
		if in {
			out = append(out, cur.String())
			cur.Reset()
			in = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\v' || r == '\f':
			flush()
			continue
		}
		cur.WriteRune(r)
		in = true
	}
	flush()
	return out
}

// wrapperWords run the command that follows them.
var wrapperWords = map[string]bool{
	"sudo": true, "env": true, "command": true, "exec": true,
	"nohup": true, "time": true, "nice": true, "builtin": true,
}

// commandCandidates returns every normalised form a prefix is tested
// against: each simple command from its start, and from each position
// reached by skipping leading assignments / wrapper words / their flags.
func commandCandidates(line string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(line, isCommandSplitter) {
		toks := shellFields(part)
		if len(toks) == 0 {
			continue
		}
		out = append(out, normCommand(strings.Join(toks, " ")))
		inWrapper := false
		for i := 0; i < len(toks)-1; i++ {
			t := toks[i]
			skip := envAssign.MatchString(t) || wrapperWords[strings.ToLower(t)] || (inWrapper && strings.HasPrefix(t, "-"))
			if !skip {
				break
			}
			if wrapperWords[strings.ToLower(t)] {
				inWrapper = true
			}
			out = append(out, normCommand(strings.Join(toks[i+1:], " ")))
		}
	}
	return out
}

// MatchCommand reports whether the full command line cmd is excluded.
func (s *ExclusionSet) MatchCommand(cmd string) bool {
	if s == nil || len(s.commands) == 0 || strings.TrimSpace(cmd) == "" {
		return false
	}
	for _, c := range commandCandidates(cmd) {
		for _, pre := range s.commands {
			if strings.HasPrefix(c, pre) {
				return true
			}
		}
	}
	return false
}

// ---- paths inside commands ----

// isPathTokenSplitter adds redirections to the simple-command splitters,
// so `>hr/a.txt` and `2>/hr/err` yield the target as its own token.
func isPathTokenSplitter(r rune) bool {
	return isCommandSplitter(r) || r == '<' || r == '>'
}

// bareFileName is a `name.ext` token with no separator.
func bareFileName(t string) bool {
	i := strings.LastIndexByte(t, '.')
	if i <= 0 || i == len(t)-1 || len(t)-i-1 > 12 {
		return false
	}
	for _, r := range t[i+1:] {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// commandPathTokens returns every path-looking token of line, unquoted and
// with any `--flag=` / `VAR=` prefix removed.
func commandPathTokens(line string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(line, isPathTokenSplitter) {
		for _, tok := range shellFields(part) {
			t := strings.NewReplacer(`"`, "", `'`, "").Replace(tok)
			if i := strings.IndexByte(t, '='); i >= 0 && (strings.HasPrefix(t, "-") || envAssign.MatchString(t)) {
				t = t[i+1:]
			}
			t = strings.TrimSpace(t)
			if t == "" || t == "-" || t == "--" {
				continue
			}
			if strings.ContainsAny(t, `/\`) || strings.HasPrefix(t, "~") || strings.HasPrefix(t, ".") || bareFileName(t) {
				out = append(out, t)
			}
		}
	}
	return out
}

// MatchCommandPaths reports whether any path-looking token of the full
// command line, resolved against cwd (absolute; "" = unresolved), matches
// a path exclusion.
func (s *ExclusionSet) MatchCommandPaths(cmd, cwd string) bool {
	if s == nil || len(s.paths) == 0 || strings.TrimSpace(cmd) == "" {
		return false
	}
	for _, t := range commandPathTokens(cmd) {
		t = expandHome(filepath.ToSlash(t), s.home)
		if s.MatchPath(t) {
			return true
		}
		if !isAbsSlash(t) && cwd != "" {
			if s.MatchPath(path.Join(filepath.ToSlash(cwd), t)) {
				return true
			}
		}
	}
	return false
}
