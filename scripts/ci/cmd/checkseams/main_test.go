package main

import "testing"

// TestCheckDirectiveAbove pins the "is the previous line a wiring:deferred
// directive" lookup checkseams uses to exempt an unimplemented seam
// interface. A full packages.Load(...) integration test is exercised
// manually against the real seams.go (see check-seam-implementers.sh's
// own smoke test in the WP06 commit body) rather than duplicated here —
// packages.Load is slow (multi-second) and this function is the only
// non-trivial logic worth pinning at unit-test speed.
func TestCheckDirectiveAbove(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		lines      []string
		declLine   int // 1-indexed, matches ast/token.Position.Line convention
		wantHit    bool
		wantReason string
	}{
		{
			name: "directive immediately above",
			lines: []string{
				"package agentgraph",
				"",
				"// wiring:deferred(needs mission X)",
				"type Foo interface{}",
			},
			declLine:   4,
			wantHit:    true,
			wantReason: "needs mission X",
		},
		{
			name: "no directive, plain doc comment",
			lines: []string{
				"package agentgraph",
				"// Foo does a thing.",
				"type Foo interface{}",
			},
			declLine: 3,
			wantHit:  false,
		},
		{
			name: "directive two lines up (blank line between) does not count",
			lines: []string{
				"package agentgraph",
				"// wiring:deferred(reason)",
				"",
				"type Foo interface{}",
			},
			declLine: 4,
			wantHit:  false,
		},
		{
			name: "reason with nested parens",
			lines: []string{
				"package agentgraph",
				"// wiring:deferred(see (this) note)",
				"type Foo interface{}",
			},
			declLine:   3,
			wantHit:    true,
			wantReason: "see (this) note",
		},
		{
			name:     "declLine at top of file — nothing above",
			lines:    []string{"type Foo interface{}"},
			declLine: 1,
			wantHit:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, reason := checkDirectiveAbove(tc.lines, tc.declLine)
			if hit != tc.wantHit {
				t.Fatalf("hit = %v, want %v", hit, tc.wantHit)
			}
			if hit && reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}
