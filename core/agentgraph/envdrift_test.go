package agentgraph

import (
	"strings"
	"testing"
)

// TestAppendEnvironmentDriftHint is WP02 of
// tool-error-legibility-01PMDL02: the classifier must append a standard
// diagnostic suffix for each well-known signature and must be a
// conservative no-op for anything else — a false hint on an unrelated
// error is worse than none.
func TestAppendEnvironmentDriftHint(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantSuffix bool
	}{
		{name: "no such file or directory", content: `open /tmp/x: no such file or directory`, wantSuffix: true},
		{name: "permission denied", content: `open /etc/shadow: permission denied`, wantSuffix: true},
		{name: "not found", content: `resource "widget-42" not found`, wantSuffix: true},
		{name: "case-insensitive match", content: `Permission Denied while opening handle`, wantSuffix: true},
		{name: "unrelated schema error", content: `tool "svc__foo" received non-JSON arguments that could not be parsed as a JSON object`, wantSuffix: false},
		{name: "unrelated crash", content: `panic: index out of range [3] with length 2`, wantSuffix: false},
		{name: "policy denial", content: `tool "svc__rm" denied: cedar forbid rule matched`, wantSuffix: false},
		{name: "empty content", content: "", wantSuffix: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AppendEnvironmentDriftHint(tc.content)
			if !strings.HasPrefix(got, tc.content) {
				t.Fatalf("classifier must never rewrite the original text; got %q for input %q", got, tc.content)
			}
			hasSuffix := got != tc.content
			if hasSuffix != tc.wantSuffix {
				t.Errorf("content=%q: suffix appended = %v, want %v (result: %q)", tc.content, hasSuffix, tc.wantSuffix, got)
			}
		})
	}
}
