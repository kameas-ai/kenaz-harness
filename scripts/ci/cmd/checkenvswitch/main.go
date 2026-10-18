// Command checkenvswitch implements G-6 (model-settings-reach-the-model-
// 01PMZ101 WP12/WP14, spec §7 G-6): documented env-var values a `switch`
// silently absorbs into its `default:` arm.
//
// THE DEFECT CLASS THIS GATE CLOSES
// ----------------------------------
// core/llm/capabilities/cache.go's DefaultCache documented
// HARNESS_LLM_CAPABILITY_CACHE as accepting "sqlite" | "memory" | "off"
// (a doc comment of the shape `// Values: "a" | "b" | "c".`) while its
// switch statement had no `case "sqlite":` arm at all — the documented
// value silently fell through to `default:` (MemoryCache), and the
// capability probe cache (migration sessions/0329, shipped in v0.63.0)
// stayed permanently unwritten in every install that believed the doc
// comment and set the env var. This is the general shape of that defect:
// a doc comment PROMISES a value the switch does not implement.
//
// SCOPE (deliberately narrow, and said so rather than pretending
// otherwise)
// -----------------------------------------------------------------------
// This tool is a same-FILE, textual checker: for a `const Env\w+ = "..."`
// declaration whose doc comment contains a `Values: "a" | "b" | "c"`
// list, it looks for a `switch os.Getenv(<ConstName>)` (or
// `switch v := os.Getenv(<ConstName>); v` / `switch <localVar>` where
// localVar was assigned from `os.Getenv(<ConstName>)` two lines earlier)
// in the SAME FILE and verifies every documented value appears in some
// `case "...":` line of that switch. It does not follow the constant
// across files or through indirection beyond the one local-variable hop
// above — a documented env const consumed in a DIFFERENT file, or
// through a helper function, is invisible to this scan. That is a real
// limitation, not a secret one: the only confirmed instance of this
// class to date (EnvCapabilityCache) has both halves in the same file,
// and widening the scope is future work, not a blocker for closing the
// confirmed finding.
//
// ALLOWLIST
// ---------
// scripts/ci/allowlists/i21-documented-env-switch-gaps.txt. One
// FULLY-QUALIFIED KEY per line (`<constName>#<value>`), followed by a
// `#` comment naming the DATE, the BLOCKER and the OWNER.
//
// Exit codes:
//
//	0 — every documented value for every same-file env-const/switch pair
//	    has a matching case, modulo the allowlist; the allowlist has no
//	    stale entries.
//	1 — the scan itself failed (bad allowlist, etc.) — a tool defect.
//	2 — at least one documented value has no matching case and is not
//	    allowlisted, OR the allowlist has a stale entry.
package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const allowlistPath = "scripts/ci/allowlists/i21-documented-env-switch-gaps.txt"

// overlayEnvVar names a JSON file (shape: {"Replace": {"<absolute real
// path>": "<scratch path>"}}) that lets gates_can_fail_test.go plant a
// violation without writing to the real tree — same mechanism
// checknilopts's NIL_OPTIONAL_DEPS_OVERLAY and checkadaptercatalog's
// ADAPTER_CATALOG_OVERLAY already established.
const overlayEnvVar = "ENV_SWITCH_OVERLAY"

var overlay map[string]string

func readFile(path string) ([]byte, error) {
	if len(overlay) > 0 {
		if abs, err := filepath.Abs(path); err == nil {
			if scratch, ok := overlay[abs]; ok {
				return os.ReadFile(scratch)
			}
		}
	}
	return os.ReadFile(path)
}

func loadOverlay() (map[string]string, error) {
	path := os.Getenv(overlayEnvVar)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s=%s: %w", overlayEnvVar, path, err)
	}
	var doc struct {
		Replace map[string]string `json:"Replace"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s=%s: %w", overlayEnvVar, path, err)
	}
	return doc.Replace, nil
}

// scanRoots are walked (non-recursively into every subdirectory) for Go
// source files. core/ and cmd/ mirror every other gate in this
// directory's scan scope.
var scanRoots = []string{"core", "cmd"}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL:", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL:", err)
		os.Exit(1)
	}

	overlay, err = loadOverlay()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL:", err)
		os.Exit(1)
	}

	files, err := goFiles(scanRoots)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL: walking scan roots:", err)
		os.Exit(1)
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL: found ZERO .go files under", scanRoots,
			"— broken scan, not a clean bill of health.")
		os.Exit(1)
	}

	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL: reading allowlist:", err)
		os.Exit(1)
	}
	used := map[string]bool{}

	var violations []string
	envConstsFound := 0

	for _, f := range files {
		data, rerr := readFile(f)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL: reading", f, ":", rerr)
			os.Exit(1)
		}
		text := string(data)
		for _, m := range envConstRe.FindAllStringSubmatchIndex(text, -1) {
			// Group 1 (m[2]:m[3]) is the doc-comment block; group 2
			// (m[4]:m[5]) is the constant name — see envConstRe's own
			// group order.
			doc := text[m[2]:m[3]]
			constName := text[m[4]:m[5]]
			values := parseValuesList(doc)
			if len(values) == 0 {
				continue
			}
			envConstsFound++
			cases := findSwitchCases(text, constName)
			for _, v := range values {
				key := constName + "#" + v
				if cases[v] {
					continue
				}
				if allowlist[key] {
					used[key] = true
					continue
				}
				violations = append(violations, fmt.Sprintf(
					"%s: %s's doc comment documents value %q but no `case %q:` (or "+
						"equivalent) exists in a switch on os.Getenv(%s) in this file — "+
						"the documented value silently falls to `default:`. Add the case, "+
						"correct the doc comment, or add a dated line in %s.",
					f, constName, v, v, constName, allowlistPath))
			}
		}
	}

	if envConstsFound == 0 {
		fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL: found ZERO `Values: \"a\" | \"b\"`-documented "+
			"env constants under", scanRoots, "— broken scan (the one confirmed instance, "+
			"EnvCapabilityCache, must be visible), not a clean bill of health.")
		os.Exit(1)
	}

	var stale []string
	for entry := range allowlist {
		if !used[entry] {
			stale = append(stale, entry)
		}
	}
	sort.Strings(stale)
	for _, s := range stale {
		violations = append(violations, fmt.Sprintf(
			"allowlist entry %q in %s no longer corresponds to a live violation — remove the stale line.",
			s, allowlistPath))
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "[env-switch-coverage] FAIL:", v)
		}
		os.Exit(2)
	}

	fmt.Printf("[env-switch-coverage] clean — %d documented env constant(s) checked.\n", envConstsFound)
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return os.Getwd()
	}
	return strings.TrimSpace(string(out)), nil
}

func goFiles(roots []string) ([]string, error) {
	var out []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// envConstRe matches an exported `const Env<Name> = "<VALUE>"`
// declaration together with the doc-comment block immediately above it
// (captured as a contiguous run of `//`-prefixed lines). Group 1 is the
// constant name; group 2 is the doc comment text.
var envConstRe = regexp.MustCompile(`(?m)((?:^[ \t]*//[^\n]*\n)+)[ \t]*(Env[A-Za-z0-9_]*)\s*=\s*"[^"]+"`)

// valuesLineRe matches a `Values: "a" | "b" | "c".` doc-comment line
// (optionally continued; this tool only reads the first such line).
var valuesLineRe = regexp.MustCompile(`Values:\s*(.+)`)

var quotedRe = regexp.MustCompile(`"([^"]*)"`)

// parseValuesList extracts the pipe-separated quoted values from a
// `Values: "a" | "b" | "c"` doc-comment fragment. Empty-string values
// ("" meaning "no override") are skipped — there is no `case "":` to
// check for something that means "absence", and a bare unset Getenv
// naturally produces "".
func parseValuesList(doc string) []string {
	m := valuesLineRe.FindStringSubmatch(doc)
	if m == nil {
		return nil
	}
	// Stop at the first sentence-ending period that is not inside a
	// quoted value, by simply taking everything up to the first '.'
	// that follows a quote-close — approximated here by cutting at the
	// first '.' not immediately preceded by a quote char, which is
	// good enough for this file's own doc-comment style.
	line := m[1]
	if idx := strings.Index(line, ". "); idx >= 0 {
		line = line[:idx]
	}
	var out []string
	for _, qm := range quotedRe.FindAllStringSubmatch(line, -1) {
		v := qm[1]
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

var caseValRe = regexp.MustCompile(`case\s+((?:"[^"]*"\s*,?\s*)+):`)
var caseQuotedRe = regexp.MustCompile(`"([^"]*)"`)

// findSwitchCases returns the set of quoted case values in the FIRST
// switch statement (in file text order) whose header references
// os.Getenv(constName) — either directly (`switch os.Getenv(X)`) or via
// a one-hop local variable (`v := os.Getenv(X)` ... `switch v {`,
// searched within a short window after the assignment).
func findSwitchCases(text, constName string) map[string]bool {
	out := map[string]bool{}

	directRe := regexp.MustCompile(`switch\s+os\.Getenv\(\s*` + regexp.QuoteMeta(constName) + `\s*\)\s*\{`)
	loc := directRe.FindStringIndex(text)

	if loc == nil {
		// One-hop local variable: `<name> := os.Getenv(ConstName)` then,
		// within the next 400 bytes, `switch <name>` or
		// `switch <keyword> <name> := os.Getenv(...); <name> {`.
		assignRe := regexp.MustCompile(`(\w+)\s*:?=\s*os\.Getenv\(\s*` + regexp.QuoteMeta(constName) + `\s*\)`)
		am := assignRe.FindStringSubmatchIndex(text)
		if am == nil {
			return out
		}
		varName := text[am[2]:am[3]]
		windowEnd := am[1] + 400
		if windowEnd > len(text) {
			windowEnd = len(text)
		}
		window := text[am[1]:windowEnd]
		swRe := regexp.MustCompile(`switch\s+(?:\w+\s*:=\s*os\.Getenv\([^)]*\)\s*;\s*)?` + regexp.QuoteMeta(varName) + `\s*\{`)
		swLoc := swRe.FindStringIndex(window)
		if swLoc == nil {
			return out
		}
		loc = []int{am[1] + swLoc[0], am[1] + swLoc[1]}
	}

	// Walk forward from the opening brace, tracking brace depth, and
	// collect every `case "...":` line until the switch's closing brace.
	depth := 1
	i := loc[1]
	body := text[i:]
	end := len(body)
	d := depth
	for j := 0; j < len(body); j++ {
		switch body[j] {
		case '{':
			d++
		case '}':
			d--
			if d == 0 {
				end = j
				j = len(body)
			}
		}
	}
	block := body[:end]
	for _, cm := range caseValRe.FindAllStringSubmatch(block, -1) {
		for _, qm := range caseQuotedRe.FindAllStringSubmatch(cm[1], -1) {
			out[qm[1]] = true
		}
	}
	return out
}

func loadAllowlist(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out[line] = true
	}
	return out, nil
}
