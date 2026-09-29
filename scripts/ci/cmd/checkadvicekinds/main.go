// Command checkadvicekinds implements tasks.md WP03 (laya-advisors-
// 01LAYA001): "every registered kind carries id/extractor/prompt_version/
// safety-class, and no suggest-only kind reaches an auto-act call site."
//
// Three checks:
//
//  1. STRUCTURAL DISCOVERY FLOOR. core/advice/kind.go must declare both
//     known SafetyClass values ("reversible", "suggest_only") and the
//     AdviceKind struct's four spec-named fields (ID, PromptVersion,
//     SafetyClass, Extract). This is the seam's own shape, present from
//     WP01 onward and unrelated to how many kinds have shipped — if this
//     ever resolves to zero matches, the SCAN is broken (a rename, a
//     moved file), not "nothing to check." This is the non-vacuous floor
//     tasks.md asks for; see the package-level doc note below for why it
//     is NOT a "count of registered kinds > 0" gate.
//
//  2. REGISTRY COMPLETENESS. Every `advice.(Must)?Register(advice.AdviceKind{
//     ... })` composite literal found under core/ and cmd/ (non-test
//     files) must carry all four spec-named fields (id, extractor,
//     prompt_version, safety class). A kind literal missing one is a
//     violation named by file:line and the missing field name.
//
//  3. SUGGEST-ONLY CANNOT AUTO-ACT. Every `advice.RequireCanAutoAct("<id>")`
//     call site found under core/ and cmd/ whose kind id resolves (via
//     check #2's collected registrations) to SafetyClass suggest_only is
//     a violation.
//
// # Why check #1, not "count of registered kinds > 0", is the floor
//
// laya-advisors-01LAYA001 ships its seam (WP01-03) with ZERO advice
// kinds registered in production — the three v1 kinds (branch_now,
// compact_now, escalate_model) are WP04-06, a later mission phase. A
// gate requiring >=1 registered kind would legitimately fail on THIS
// release for weeks between the seam merging and the first kind
// shipping, on every unrelated PR touching the release branch — the
// exact "merge gates key on paths, not verdicts nobody can act on"
// failure mode CLAUDE.md warns against. Check #2 still runs over
// whatever IS registered (reporting "0 kind(s) found" as an informational
// WARN, not a FAIL, when the count is zero) so it stays non-vacuous
// automatically the moment a kind ships — a completeness bug in a
// REAL registration is caught the day it lands, and check #1 catches
// the gate's own scan breaking regardless of the population size.
//
// Exit codes (when this binary is run directly):
//
//	0 — clean: the structural floor holds, every found kind registration
//	    is complete, and no found auto-act call site names a
//	    suggest-only kind.
//	1 — the scan itself failed (missing scan root, kind.go's shape moved,
//	    a required source file not found) — a tool defect or a
//	    scan target that moved without this tool being updated.
//	2 — at least one real violation was found.
//
// check-advice-kinds.sh invokes this via `go run`, whose own process
// exit code collapses ANY non-zero child exit to 1 (verified against
// this repo's toolchain) — so from the shell wrapper's perspective,
// "scan defect" and "real violation" are both simply "non-zero"; the
// distinction survives only in this program's stderr text (the
// "[advice-kinds] FAIL:" lines), which is what callers — including
// gates_can_fail_test.go's planted-violation proofs — should match on
// rather than the wrapper's exit code.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var scanRoots = []string{"core", "cmd"}

const kindGoPath = "core/advice/kind.go"

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", err)
		os.Exit(1)
	}

	var violations []string

	if err := checkStructuralFloor(); err != nil {
		fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", err)
		os.Exit(1)
	}

	kinds, kindViolations, err := checkKindRegistrations()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", err)
		os.Exit(1)
	}
	violations = append(violations, kindViolations...)
	if len(kinds) == 0 {
		fmt.Println("[advice-kinds] WARN: 0 AdviceKind registration(s) found under", scanRoots,
			"— expected during laya-advisors-01LAYA001's WP01-03 bootstrap (the seam ships before "+
				"any kind; WP04-06 add branch_now/compact_now/escalate_model). This is NOT the "+
				"discovery-floor failure — see check #1 (the structural floor over core/advice/kind.go's "+
				"own shape) for what actually guards against a broken scan.")
	}

	autoActViolations, autoActSites, err := checkSuggestOnlyCannotAutoAct(kinds)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", err)
		os.Exit(1)
	}
	violations = append(violations, autoActViolations...)

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "[advice-kinds] FAIL:", v)
		}
		os.Exit(2)
	}

	fmt.Printf("[advice-kinds] clean — structural floor holds, %d kind registration(s) found and complete, "+
		"%d advice.RequireCanAutoAct call site(s) checked with no suggest-only violation.\n",
		len(kinds), autoActSites)
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

// ── check #1: structural discovery floor ───────────────────────────────

var (
	safetyReversibleRe   = regexp.MustCompile(`"reversible"`)
	safetySuggestOnlyRe  = regexp.MustCompile(`"suggest_only"`)
	adviceKindStructRe   = regexp.MustCompile(`type AdviceKind struct\s*\{`)
	adviceKindFieldIDRe  = regexp.MustCompile(`\bID\s+string\b`)
	adviceKindFieldPVRe  = regexp.MustCompile(`\bPromptVersion\s+string\b`)
	adviceKindFieldSCRe  = regexp.MustCompile(`\bSafetyClass\s+SafetyClass\b`)
	adviceKindFieldExtRe = regexp.MustCompile(`\bExtract\s+FeatureExtractor\b`)
)

// checkStructuralFloor implements assertion #1: core/advice/kind.go must
// still declare both safety classes and the AdviceKind struct's four
// spec-named fields. Failure here means the SCAN moved or broke, not
// that the seam has a problem — hence exit code 1, not 2.
func checkStructuralFloor() error {
	data, err := os.ReadFile(kindGoPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w (the advisor seam's registry file moved or was removed; "+
			"update this tool in the same change)", kindGoPath, err)
	}
	text := string(data)

	if !safetyReversibleRe.MatchString(text) || !safetySuggestOnlyRe.MatchString(text) {
		return fmt.Errorf("%s: could not find both SafetyClass values (\"reversible\", \"suggest_only\") — "+
			"the discovery floor resolved to zero safety-class matches, which means this tool's scan is "+
			"broken (renamed constant, moved file), not that the seam lost its safety classes", kindGoPath)
	}

	loc := adviceKindStructRe.FindStringIndex(text)
	if loc == nil {
		return fmt.Errorf("%s: no `type AdviceKind struct {` declaration found — the registry entry type "+
			"moved or was renamed; update this tool in the same change", kindGoPath)
	}
	block, berr := braceMatch(text, loc[1]-1)
	if berr != nil {
		return fmt.Errorf("%s: %w", kindGoPath, berr)
	}
	var missing []string
	if !adviceKindFieldIDRe.MatchString(block) {
		missing = append(missing, "ID string")
	}
	if !adviceKindFieldPVRe.MatchString(block) {
		missing = append(missing, "PromptVersion string")
	}
	if !adviceKindFieldSCRe.MatchString(block) {
		missing = append(missing, "SafetyClass SafetyClass")
	}
	if !adviceKindFieldExtRe.MatchString(block) {
		missing = append(missing, "Extract FeatureExtractor")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: AdviceKind struct is missing expected field declaration(s): %s — "+
			"the scan's field patterns no longer match the real struct; update this tool in the same "+
			"change as any AdviceKind field rename", kindGoPath, strings.Join(missing, ", "))
	}
	return nil
}

// braceMatch returns the text strictly between the '{' at index open and
// its matching '}'. Mirrors checkriskgatedecides's braceMatch.
func braceMatch(text string, open int) (string, error) {
	if open >= len(text) || text[open] != '{' {
		return "", fmt.Errorf("braceMatch: index %d is not '{'", open)
	}
	depth := 1
	for i := open + 1; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open+1 : i], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced braces starting at offset %d", open)
}

// ── check #2: registry completeness ─────────────────────────────────────

// registeredKind is one AdviceKind{...} composite literal this tool found,
// resolved well enough to check completeness and (for check #3) the
// kind's declared safety class.
type registeredKind struct {
	id     string
	safety string // "reversible", "suggest_only", or "" if unresolved/missing
	file   string
	line   int
}

// registerCallRe matches `(advice.)?(Must)?Register(advice.AdviceKind{` or
// `(advice.)?(Must)?Register(AdviceKind{` (the bare form, for code living
// inside package advice itself) — the opening brace of the composite
// literal argument.
var registerCallRe = regexp.MustCompile(`(?:advice\.)?(?:Must)?Register\(\s*(?:advice\.)?AdviceKind\{`)

var (
	kindIDFieldRe     = regexp.MustCompile(`\bID\s*:\s*"([^"]*)"`)
	kindPromptVerRe   = regexp.MustCompile(`\bPromptVersion\s*:`)
	kindSafetyClassRe = regexp.MustCompile(`\bSafetyClass\s*:\s*(?:advice\.)?(Safety\w+|"[^"]*")`)
	kindExtractRe     = regexp.MustCompile(`\bExtract\s*:`)
)

// checkKindRegistrations implements assertion #2. Returns every kind
// registration this tool could resolve (used by check #3) and any
// completeness violations found.
func checkKindRegistrations() ([]registeredKind, []string, error) {
	files, err := goFiles(scanRoots)
	if err != nil {
		return nil, nil, fmt.Errorf("walking scan roots: %w", err)
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("found ZERO .go files under %v", scanRoots)
	}

	var kinds []registeredKind
	var violations []string

	for _, f := range files {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", f, rerr)
		}
		text := string(data)
		for _, loc := range registerCallRe.FindAllStringIndex(text, -1) {
			openBrace := loc[1] - 1
			block, berr := braceMatch(text, openBrace)
			if berr != nil {
				return nil, nil, fmt.Errorf("%s: %w", f, berr)
			}
			line := 1 + strings.Count(text[:loc[0]], "\n")

			id := ""
			if m := kindIDFieldRe.FindStringSubmatch(block); m != nil {
				id = m[1]
			}
			safety := ""
			if m := kindSafetyClassRe.FindStringSubmatch(block); m != nil {
				safety = strings.Trim(m[1], `"`)
				// Normalize the Go identifier form (SafetyReversible /
				// SafetySuggestOnly) to the string value it carries —
				// both forms are legal AdviceKind literal shapes.
				switch safety {
				case "SafetyReversible":
					safety = "reversible"
				case "SafetySuggestOnly":
					safety = "suggest_only"
				}
			}

			var missing []string
			if id == "" {
				missing = append(missing, "id")
			}
			if !kindPromptVerRe.MatchString(block) {
				missing = append(missing, "prompt_version")
			}
			if safety == "" {
				missing = append(missing, "safety_class")
			}
			if !kindExtractRe.MatchString(block) {
				missing = append(missing, "extractor")
			}
			if len(missing) > 0 {
				name := id
				if name == "" {
					name = "<unresolved>"
				}
				violations = append(violations, fmt.Sprintf(
					"%s:%d: AdviceKind registration for %q is missing required field(s): %s",
					f, line, name, strings.Join(missing, ", ")))
				continue
			}

			kinds = append(kinds, registeredKind{id: id, safety: safety, file: f, line: line})
		}
	}

	return kinds, violations, nil
}

// ── check #3: suggest-only cannot auto-act ──────────────────────────────

// autoActCallRe matches `advice.RequireCanAutoAct("<id>")` (or the bare
// `RequireCanAutoAct(` form for code inside package advice itself),
// capturing the string-literal kind id argument. A non-string-literal
// argument (a variable) cannot be statically resolved and is skipped —
// documented scope limit, mirrors checkriskgatedecides's own "SCOPE,
// HONESTLY STATED" convention.
var autoActCallRe = regexp.MustCompile(`(?:advice\.)?RequireCanAutoAct\(\s*"([^"]+)"\s*\)`)

// checkSuggestOnlyCannotAutoAct implements assertion #3: every found call
// site naming a kind id that check #2 resolved to SafetySuggestOnly is a
// violation. Returns (violations, call sites found).
func checkSuggestOnlyCannotAutoAct(kinds []registeredKind) ([]string, int, error) {
	files, err := goFiles(scanRoots)
	if err != nil {
		return nil, 0, fmt.Errorf("walking scan roots: %w", err)
	}

	safetyByID := map[string]string{}
	for _, k := range kinds {
		safetyByID[k.id] = k.safety
	}

	var violations []string
	sites := 0

	for _, f := range files {
		data, rerr := os.ReadFile(f)
		if rerr != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", f, rerr)
		}
		text := string(data)
		for _, m := range autoActCallRe.FindAllStringSubmatchIndex(text, -1) {
			sites++
			id := text[m[2]:m[3]]
			line := 1 + strings.Count(text[:m[0]], "\n")
			safety, known := safetyByID[id]
			if !known {
				// Cannot verify — the id does not match any registration
				// this tool resolved. Not a violation (the kind may be
				// registered in a way check #2 could not parse, or the
				// id may not exist at all, which RequireCanAutoAct
				// itself already fails at runtime via ErrKindNotRegistered).
				continue
			}
			if safety == "suggest_only" {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: advice.RequireCanAutoAct(%q) reaches a SUGGEST-ONLY kind — "+
						"spec §3: a suggest-only kind must never auto-act, regardless of autonomy tier",
					f, line, id))
			}
		}
	}

	return violations, sites, nil
}
