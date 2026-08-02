package prompts

import (
	"strings"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
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
// must agree on joining semantics (trim, drop-empty, "\n\n" join) for a
// nil (un-profiled) tmpl — the default-renderer byte-identity guarantee
// (per-family-message-shaping-01PMDL06 WP01, spec §5).
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
			if got := Compose(nil, tc.parts...); got != tc.want {
				t.Errorf("Compose(nil, %q) = %q, want %q", tc.parts, got, tc.want)
			}
			// An empty-Format tmpl (PromptTemplateRef zero value) must
			// behave identically to nil — "" means "use default
			// renderer" per the field's own doc.
			if got := Compose(&corellm.PromptTemplateRef{}, tc.parts...); got != tc.want {
				t.Errorf("Compose(&PromptTemplateRef{}, %q) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

// TestCompose_UnregisteredFormatFallsBackToDefault guards that a tmpl
// naming a format with no registered renderer (a typo, or a future
// format this WP hasn't implemented yet) degrades to the default join
// rather than erroring or silently dropping content.
func TestCompose_UnregisteredFormatFallsBackToDefault(t *testing.T) {
	t.Parallel()
	got := Compose(&corellm.PromptTemplateRef{Format: "yaml"}, "BASE", "ROLE")
	want := "BASE\n\nROLE"
	if got != want {
		t.Errorf("Compose(Format=yaml) = %q, want %q (default fallback)", got, want)
	}
}

// TestCompose_XMLVariant asserts that a tmpl with Format=="xml" (the
// per-family variant an Anthropic-style profile would register)
// produces XML-tagged sections instead of the default Markdown join,
// and that the variant is case-insensitive and whitespace-tolerant on
// the Format field the same way a hand-authored profile YAML might be.
func TestCompose_XMLVariant(t *testing.T) {
	t.Parallel()
	want := "<section index=\"1\">\nBASE\n</section>\n<section index=\"2\">\nROLE\n</section>"
	for _, format := range []string{"xml", "XML", "  Xml  "} {
		if got := Compose(&corellm.PromptTemplateRef{Format: format}, "BASE", "ROLE"); got != want {
			t.Errorf("Compose(Format=%q) = %q, want %q", format, got, want)
		}
	}
}

// TestCompose_XMLVariant_DropsEmptyParts guards that the XML variant
// applies the same trim/drop-empty semantics as the default renderer —
// no empty <section> for a blank layer.
func TestCompose_XMLVariant_DropsEmptyParts(t *testing.T) {
	t.Parallel()
	got := Compose(&corellm.PromptTemplateRef{Format: "xml"}, "", "  ", "ONLY")
	want := "<section index=\"1\">\nONLY\n</section>"
	if got != want {
		t.Errorf("Compose(Format=xml, sparse) = %q, want %q", got, want)
	}
}
