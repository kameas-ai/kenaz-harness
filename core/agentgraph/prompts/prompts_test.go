package prompts

import (
	"strings"
	"testing"
)

// TestDefaultBaseConstitution_NonEmpty guards that the embed resolved.
func TestDefaultBaseConstitution_NonEmpty(t *testing.T) {
	if strings.TrimSpace(DefaultBaseConstitution()) == "" {
		t.Fatal("DefaultBaseConstitution() is empty; base.md embed failed")
	}
}

// TestDefaultBaseConstitution_TokenBudget is a rough guard so the shared
// constitution can't silently bloat and eat every model call's context
// budget. ~4 chars/token puts 3000 chars around 750 tokens.
func TestDefaultBaseConstitution_TokenBudget(t *testing.T) {
	const maxChars = 3000
	if n := len(DefaultBaseConstitution()); n >= maxChars {
		t.Errorf("DefaultBaseConstitution() = %d chars, want < %d", n, maxChars)
	}
}

// TestCompose mirrors the exec_compute_test.go TestComposePrompt table —
// composePrompt in core/agentgraph delegates to this function, so both
// must agree on joining semantics (trim, drop-empty, "\n\n" join).
func TestCompose(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"both parts, order preserved", []string{"BASE", "ROLE"}, "BASE\n\nROLE"},
		{"empty base dropped", []string{"", "ROLE"}, "ROLE"},
		{"empty role dropped", []string{"BASE", ""}, "BASE"},
		{"both empty", []string{"", ""}, ""},
		{"no parts", nil, ""},
		{"whitespace-only parts dropped", []string{"  \n\t ", "ROLE"}, "ROLE"},
		{"parts trimmed", []string{"  BASE  ", "  ROLE  "}, "BASE\n\nROLE"},
		{"more than two parts", []string{"A", "B", "C"}, "A\n\nB\n\nC"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Compose(tc.parts...); got != tc.want {
				t.Errorf("Compose(%q) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}
