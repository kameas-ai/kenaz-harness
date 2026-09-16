// Command checkriskgatedecides implements WP08 (risk-rated-autonomy-
// 01PMRA01, tasks.md WP08): "fail the build when the risk gate cannot
// decide." Three independent assertions, matching the WP08 brief
// exactly:
//
//  1. Every production switch on a cedar.Outcome-typed value that
//     already branches explicitly on Allow AND Deny (the signature of a
//     real cedar.Outcome switch, as opposed to some unrelated type that
//     happens to have a field named Outcome) must ALSO branch
//     explicitly on Confirm — never absorb it into an unlabelled
//     `default:`. A switch that silently treats "not deny" as "allow"
//     reintroduces the exact default-allow hole layer 3 exists to close
//     one layer up (spec.md FR-001; tasks.md WP01's own proof
//     requirement: "a test enumerating every production switch on
//     Outcome and asserting confirm is handled explicitly (not via
//     default:)"). core/policy/cedar/outcome_confirm_audit_test.go
//     already pins this at the Go-test level for the six switches (plus
//     two non-switch if-sites) WP01's own audit found; that is a
//     regression pin for KNOWN call sites, not a gate — nothing stops a
//     NEW switch statement from shipping with a silent default-to-ask-
//     nothing arm. This tool is the static, monotonic version: it scans
//     the whole tree, so a new violation cannot land unnoticed.
//
//  2. autonomy.ResolvedKnobs.RiskThreshold has the real, named consumer
//     WP03 wired: the knobcoverage.Register call site in
//     core/rpc/views/agentgraph/chat/kernel_tool_adapter.go's init().
//     check-knob-coverage.sh already fails generically if ANY registered
//     knob (this one included) has no test-asserted consumer; this is a
//     second, narrower, self-contained check that THIS SPECIFIC
//     registration is still present, so WP08 does not silently depend on
//     an unrelated gate remaining wired to catch its own knob being
//     deleted or renamed.
//
//  3. The family floor (core/policy/risk/floor.go's familyFloorScore)
//     sits strictly ABOVE the most permissive autonomy tier's
//     RiskThreshold preset (core/autonomy/presets.go's TierAutonomous
//     entry), so FamilyDestructive/FamilyUnknown can never resolve to
//     Allow via score < threshold at ANY tier (spec.md's security
//     section, "a floor by family"; floor.go's own doc comment states
//     this invariant in prose — this tool is what enforces it in CI
//     rather than trusting the comment to stay true).
//
// SCOPE, HONESTLY STATED
// -----------------------
// Assertion #1 is a same-file, textual/brace-matched scan (mirrors
// checkenvswitch's documented scope note): it finds `switch <expr>.Outcome {`
// headers and brace-matches the block that follows in the SAME file. It
// does not follow a switch expression assigned via a local variable one
// or more hops away, and it does not understand type-switches. Every
// confirmed instance of a cedar.Outcome switch in this repo as of this
// gate's introduction (core/tools/bash/bash.go, core/tools/fs/gate.go,
// core/rpc/views/agentgraph/chat/kernel_tool_adapter.go,
// core/policy/cedar/hooks.go x4, core/policy/cedar/risk_layer.go) is a
// direct `switch d.Outcome {` shape, so this is not a hypothetical
// scope — it is calibrated to the real, confirmed population.
//
// A switch only counts as "a cedar.Outcome switch" if its case set
// already contains both an Allow-shaped and a Deny-shaped case value —
// this is what distinguishes it from an unrelated type that happens to
// have a field named Outcome (several exist in this repo: e.g.
// core/secrets, core/memory/narrative) without ever branching on
// Allow/Deny/Confirm-shaped values at all.
//
// ALLOWLIST
// ---------
// scripts/ci/allowlists/i2X-risk-gate-outcome-switch-gaps.txt (created
// lazily — this gate ships with zero known violations; see
// core/policy/cedar/risk_layer.go's own switch, which was rewritten in
// the same commit as this gate to spell out `case NotApplicable,
// Confirm:` explicitly instead of relying on `default:`, specifically so
// this gate has nothing to allowlist on day one). One FULLY-QUALIFIED
// KEY per line (`<file>:<switch-start-line>`), a locator comment, and a
// `#` comment naming the DATE, the BLOCKER and the OWNER — same format
// as every other allowlist in this directory (see i18/i13's headers).
//
// Exit codes:
//
//	0 — clean: every qualifying Outcome switch handles Confirm
//	    explicitly (modulo the allowlist), RiskThreshold's registration
//	    is present, and the family floor sits above the autonomous
//	    threshold.
//	1 — the scan itself failed (bad allowlist, missing scan root, a
//	    required source file/pattern not found) — a tool defect or a
//	    scan-target that moved without this tool being updated.
//	2 — at least one real violation was found.
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
	"strconv"
	"strings"
)

const allowlistPath = "scripts/ci/allowlists/i2X-risk-gate-outcome-switch-gaps.txt"

// overlayEnvVar names a JSON file (shape: {"Replace": {"<absolute real
// path>": "<scratch path>"}}) letting gates_can_fail_test.go plant a
// violation without writing to the real tree — the same mechanism
// checkenvswitch's ENV_SWITCH_OVERLAY and checknilopts's
// NIL_OPTIONAL_DEPS_OVERLAY already established.
const overlayEnvVar = "RISK_GATE_DECIDES_OVERLAY"

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

// scanRoots mirrors every other gate in this directory's scan scope.
var scanRoots = []string{"core", "cmd"}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}
	if err := os.Chdir(root); err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}

	overlay, err = loadOverlay()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}

	var violations []string

	switchViolations, switchCount, err := checkOutcomeSwitches()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}
	violations = append(violations, switchViolations...)
	if switchCount == 0 {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL: found ZERO qualifying cedar.Outcome switches "+
			"(a switch with both an Allow-shaped and a Deny-shaped case) under", scanRoots, "— broken scan, "+
			"not a clean bill of health. The confirmed population as of this gate's introduction includes "+
			"core/tools/bash/bash.go and core/policy/cedar/hooks.go.")
		os.Exit(1)
	}

	thresholdViolation, err := checkRiskThresholdConsumer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}
	if thresholdViolation != "" {
		violations = append(violations, thresholdViolation)
	}

	floorViolation, floorScore, autonomousThreshold, err := checkFamilyFloorAboveThreshold()
	if err != nil {
		fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", err)
		os.Exit(1)
	}
	if floorViolation != "" {
		violations = append(violations, floorViolation)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "[risk-gate-decides] FAIL:", v)
		}
		os.Exit(2)
	}

	fmt.Printf("[risk-gate-decides] clean — %d qualifying Outcome switch(es) handle Confirm explicitly, "+
		"RiskThreshold's consumer registration is present, and the family floor (%d) sits above the "+
		"autonomous tier's threshold (%d).\n", switchCount, floorScore, autonomousThreshold)
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

// switchHeaderRe matches `switch <selector-expr>.Outcome {` headers.
// Group 1 is the full switched expression (unused beyond documentation
// in error messages).
var switchHeaderRe = regexp.MustCompile(`switch\s+([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\.Outcome)\s*\{`)

var caseLineRe = regexp.MustCompile(`(?m)^[ \t]*case\s+([^:{]+):`)

var allowWordRe = regexp.MustCompile(`\bAllow\b`)
var denyWordRe = regexp.MustCompile(`\bDeny\b`)
var confirmWordRe = regexp.MustCompile(`\bConfirm\b`)

// checkOutcomeSwitches implements assertion #1. Returns the violations
// found and the total number of qualifying switches scanned (for the
// discovery-floor guard in main).
func checkOutcomeSwitches() ([]string, int, error) {
	files, err := goFiles(scanRoots)
	if err != nil {
		return nil, 0, fmt.Errorf("walking scan roots: %w", err)
	}
	if len(files) == 0 {
		return nil, 0, fmt.Errorf("found ZERO .go files under %v", scanRoots)
	}

	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		return nil, 0, fmt.Errorf("reading allowlist: %w", err)
	}
	used := map[string]bool{}

	var violations []string
	qualifying := 0

	for _, f := range files {
		data, rerr := readFile(f)
		if rerr != nil {
			return nil, 0, fmt.Errorf("reading %s: %w", f, rerr)
		}
		text := string(data)
		for _, loc := range switchHeaderRe.FindAllStringIndex(text, -1) {
			openBrace := loc[1] - 1 // index of the '{' the regex matched
			block, blockErr := braceMatch(text, openBrace)
			if blockErr != nil {
				return nil, 0, fmt.Errorf("%s: %w", f, blockErr)
			}
			cases := caseLineRe.FindAllStringSubmatch(block, -1)
			hasAllow, hasDeny, hasConfirm := false, false, false
			for _, c := range cases {
				vals := c[1]
				if allowWordRe.MatchString(vals) {
					hasAllow = true
				}
				if denyWordRe.MatchString(vals) {
					hasDeny = true
				}
				if confirmWordRe.MatchString(vals) {
					hasConfirm = true
				}
			}
			if !hasAllow || !hasDeny {
				// Not a cedar.Outcome switch (or at least not one this
				// tool can confidently classify as one) — some other
				// type with a field named Outcome. Skip.
				continue
			}
			qualifying++
			if hasConfirm {
				continue
			}
			line := 1 + strings.Count(text[:loc[0]], "\n")
			key := fmt.Sprintf("%s:%d", f, line)
			if allowlist[key] {
				used[key] = true
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s:%d: switch on a cedar.Outcome-shaped value branches on Allow and Deny but has no "+
					"explicit `case ...Confirm:` — an unmatched Outcome (NotApplicable or Confirm) falls "+
					"through to `default:` (or has no default, silently no-op), which is exactly the "+
					"default-allow shape this mission exists to close one layer up. Add an explicit case, "+
					"or add a dated line to %s if this switch genuinely cannot reach Confirm.",
				f, line, allowlistPath))
		}
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

	return violations, qualifying, nil
}

// braceMatch returns the text strictly between the '{' at index open and
// its matching '}', given open indexes a '{' byte in text.
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

// riskThresholdConsumerRe matches the exact knobcoverage.Register call
// WP03 wired for RiskThreshold. Kept as a literal-ish regex (rather than
// a bare substring check) so incidental whitespace reformatting does not
// false-positive a FAIL.
var riskThresholdConsumerRe = regexp.MustCompile(`knobcoverage\.Register\[autonomy\.ResolvedKnobs\]\(\s*"RiskThreshold"\s*,`)

// checkRiskThresholdConsumer implements assertion #2.
func checkRiskThresholdConsumer() (string, error) {
	files, err := goFiles(scanRoots)
	if err != nil {
		return "", fmt.Errorf("walking scan roots: %w", err)
	}
	for _, f := range files {
		data, rerr := readFile(f)
		if rerr != nil {
			return "", fmt.Errorf("reading %s: %w", f, rerr)
		}
		if riskThresholdConsumerRe.Match(data) {
			return "", nil
		}
	}
	return "no `knobcoverage.Register[autonomy.ResolvedKnobs](\"RiskThreshold\", ...)` call found under " +
		strings.Join(scanRoots, ", ") + " — the risk-threshold dial has no registered consumer. " +
		"(See core/rpc/views/agentgraph/chat/kernel_tool_adapter.go's init(), which wired this " +
		"registration for risk-rated-autonomy-01PMRA01 WP03.)", nil
}

var familyFloorScoreRe = regexp.MustCompile(`familyFloorScore\s*=\s*(\d+)`)
var tierAutonomousBlockRe = regexp.MustCompile(`TierAutonomous:\s*\{`)
var knobRiskThresholdValueRe = regexp.MustCompile(`KnobRiskThreshold:\s*(\d+)`)

// checkFamilyFloorAboveThreshold implements assertion #3. Returns
// (violation, floorScore, autonomousThreshold, error).
func checkFamilyFloorAboveThreshold() (string, int, int, error) {
	const floorPath = "core/policy/risk/floor.go"
	const presetsPath = "core/autonomy/presets.go"

	floorData, err := readFile(floorPath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("reading %s: %w", floorPath, err)
	}
	m := familyFloorScoreRe.FindSubmatch(floorData)
	if m == nil {
		return "", 0, 0, fmt.Errorf("%s: no `familyFloorScore = <N>` declaration found — the family "+
			"floor moved or was renamed; update this tool in the same change", floorPath)
	}
	floorScore, cerr := strconv.Atoi(string(m[1]))
	if cerr != nil {
		return "", 0, 0, fmt.Errorf("%s: parsing familyFloorScore value %q: %w", floorPath, m[1], cerr)
	}

	presetsData, err := readFile(presetsPath)
	if err != nil {
		return "", 0, 0, fmt.Errorf("reading %s: %w", presetsPath, err)
	}
	presetsText := string(presetsData)
	loc := tierAutonomousBlockRe.FindStringIndex(presetsText)
	if loc == nil {
		return "", 0, 0, fmt.Errorf("%s: no `TierAutonomous: {` block found — the preset table's shape "+
			"moved; update this tool in the same change", presetsPath)
	}
	block, berr := braceMatch(presetsText, loc[1]-1)
	if berr != nil {
		return "", 0, 0, fmt.Errorf("%s: %w", presetsPath, berr)
	}
	tm := knobRiskThresholdValueRe.FindStringSubmatch(block)
	if tm == nil {
		return "", 0, 0, fmt.Errorf("%s: TierAutonomous's block has no `KnobRiskThreshold: <N>` entry — "+
			"the preset table's shape moved; update this tool in the same change", presetsPath)
	}
	autonomousThreshold, cerr := strconv.Atoi(tm[1])
	if cerr != nil {
		return "", 0, 0, fmt.Errorf("%s: parsing TierAutonomous's KnobRiskThreshold value %q: %w", presetsPath, tm[1], cerr)
	}

	if floorScore <= autonomousThreshold {
		return fmt.Sprintf(
			"the family floor (%s's familyFloorScore = %d) does not sit strictly above the autonomous "+
				"tier's risk threshold (%s's TierAutonomous.KnobRiskThreshold = %d) — a floored "+
				"FamilyDestructive/FamilyUnknown score could resolve to Allow via score < threshold at "+
				"the most permissive tier, defeating the floor entirely.",
			floorPath, floorScore, presetsPath, autonomousThreshold), floorScore, autonomousThreshold, nil
	}
	return "", floorScore, autonomousThreshold, nil
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
