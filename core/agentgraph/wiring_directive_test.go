package agentgraph

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// wiringDeferredDirective mirrors the grammar scripts/ci/check-output-
// ports.sh and scripts/ci/check-seam-implementers.sh consult (and
// docs/wiring-audit.md documents): a //wiring:deferred(<reason>)
// comment, alone on its line, POSIX character classes (not \s — BSD
// grep on macOS dev boxes doesn't support \s in -E mode) and a greedy
// reason group so a reason containing its own parentheses still
// matches through to the line's final closing paren.
var wiringDeferredDirective = regexp.MustCompile(`^[[:space:]]*//[[:space:]]*wiring:deferred\((.+)\)[[:space:]]*$`)

// TestWiringDeferredDirective_Grammar pins the directive regex against
// representative accept/reject cases (wiring-integrity-01PMAG04 WP02).
func TestWiringDeferredDirective_Grammar(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		line  string
		match bool
	}{
		{"plain", "// wiring:deferred(needs mission X)", true},
		{"tab-indented", "\t\t// wiring:deferred(reason)", true},
		{"nested parens in reason", "// wiring:deferred(see (this) note)", true},
		{"trailing space", "// wiring:deferred(reason) ", true},
		{"no space after slashes", "//wiring:deferred(reason)", true},
		{"empty reason", "// wiring:deferred()", false},
		{"missing parens", "// wiring:deferred reason", false},
		{"not a directive at all", "// TODO: wire this up", false},
		{"trailing code after directive", "// wiring:deferred(reason) res.Outputs[\"x\"] = 1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wiringDeferredDirective.MatchString(tc.line)
			if got != tc.match {
				t.Errorf("MatchString(%q) = %v, want %v", tc.line, got, tc.match)
			}
			if tc.match {
				groups := wiringDeferredDirective.FindStringSubmatch(tc.line)
				if len(groups) < 2 || strings.TrimSpace(groups[1]) == "" {
					t.Errorf("expected a non-empty captured reason for %q", tc.line)
				}
			}
		})
	}
}

// TestWiringDeferredDirective_ExistingUsagesParse walks every .go and
// .yaml file under core/agentgraph and asserts that any line containing
// the literal substring "wiring:deferred" is a syntactically valid
// directive per the grammar above. Catches a malformed directive (typo,
// missing paren, accidental second directive on the same line) at
// authoring time rather than silently failing to suppress a guard.
func TestWiringDeferredDirective_ExistingUsagesParse(t *testing.T) {
	t.Parallel()
	root := "." // core/agentgraph — this test's own package directory.
	var checked int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		// Skip this file itself: its grammar test cases contain the
		// literal substring "wiring:deferred" inside Go string literals
		// that are deliberately NOT well-formed directives (that's the
		// point of the negative test cases above).
		if filepath.Base(path) == "wiring_directive_test.go" {
			return nil
		}
		data, rerr := os.ReadFile(path) //nolint:gosec // fixed repo-relative walk, not user input
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !strings.Contains(line, "wiring:deferred") {
				continue
			}
			checked++
			// YAML comments use "#" not "//" — accept either prefix by
			// normalising a leading "#" to "//" before matching.
			candidate := line
			if trimmed := strings.TrimLeft(line, " \t"); strings.HasPrefix(trimmed, "#") {
				indent := line[:len(line)-len(trimmed)]
				candidate = indent + "//" + strings.TrimPrefix(trimmed, "#")
			}
			if !wiringDeferredDirective.MatchString(candidate) {
				t.Errorf("%s:%d: malformed wiring:deferred directive: %q", path, i+1, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if checked == 0 {
		t.Fatal("expected at least one wiring:deferred usage under core/agentgraph — did the walk root change?")
	}
	t.Logf("checked %d wiring:deferred usages", checked)
}
