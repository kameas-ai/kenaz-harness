// Package prompts holds shared, embedded system-prompt assets and the
// composition helper used by the agentgraph kernel and by graph-authoring
// code (e.g. the chat GraphLoader in core/rpc/api.go). Package prompts is
// a leaf: it imports nothing from core/agentgraph, so core/agentgraph can
// safely import it (as exec_compute.go's composePrompt does) without
// creating an import cycle.
package prompts

import (
	_ "embed"
	"fmt"
	"strings"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

//go:embed base.md
var baseConstitution string

// DefaultBaseConstitution returns the shared "constitution" — the
// grounding + honesty + tool-use base system prompt that graph authors
// can set as a Graph's SystemPrompt (e.g. WP02 wires it into
// chat_default). The kernel does NOT auto-inject this; composing it into
// a graph is a deliberate graph-authoring choice.
func DefaultBaseConstitution() string {
	return baseConstitution
}

// Compose joins system-prompt fragments (e.g. a graph-level base
// constitution + a model node's own role prompt) into a single system
// prompt. Each part is trimmed of surrounding whitespace and empty
// parts are dropped.
//
// tmpl is the per-family-message-shaping-01PMDL06 mechanism: an
// optional pointer to the active model's ModelProfile.PromptTemplate
// (llm.PromptTemplateRef, versioned-model-profile-01PMDL04). When tmpl
// is nil, or its Format names no registered variant renderer, the
// survivors are joined with a blank line ("\n\n") — exactly today's
// behaviour, byte-for-byte, for every un-profiled model. A single
// non-empty part with no variant is returned as-is; all-empty input
// yields "". This byte-identity is the hard requirement (spec §5): the
// layered system prompt ships on every call, so an accidental change
// here alters behaviour for every user on every turn.
//
// A non-nil tmpl with a registered Format (e.g. "xml") selects a
// per-family renderer instead — see variantRenderer. The real
// per-family template content, ordering, and attention placement
// (tmpl.AttentionPlacement) are later WPs of this mission (02/04);
// this WP only wires the mechanism plus one illustrative variant so a
// profile can prove it takes effect.
func Compose(tmpl *corellm.PromptTemplateRef, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	if tmpl != nil {
		if render := variantRenderer(tmpl.Format); render != nil {
			return render(kept)
		}
	}
	return strings.Join(kept, "\n\n")
}

// variantRenderer looks up the per-family renderer registered for a
// PromptTemplateRef.Format value, or nil when format names no known
// variant (including "" — PromptTemplateRef's own doc: "use default
// renderer"). format is a rendering-style token ("xml", "markdown", …)
// sourced from profile data, never a hardcoded model-family name, so
// this switch does not trip scripts/ci/check-no-model-family-
// literals.sh — that lint bans family literals like "claude"/
// "anthropic"/"sonnet", not format-style tokens like "xml".
func variantRenderer(format string) func([]string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "xml":
		return renderXMLSections
	default:
		return nil
	}
}

// renderXMLSections renders each surviving part as an indexed
// `<section>` element instead of the default blank-line Markdown join.
// This is the illustrative XML-tagged variant the spec calls for (e.g.
// for a profile that opts an Anthropic-family model into it via
// PromptTemplateRef.Format=="xml"); the real per-family template
// content is later-WP work (02/04), so the tagging here is
// intentionally minimal — it exists to prove the variant-selection
// mechanism, not to be the final XML shaping.
func renderXMLSections(parts []string) string {
	sections := make([]string, 0, len(parts))
	for i, p := range parts {
		sections = append(sections, fmt.Sprintf("<section index=\"%d\">\n%s\n</section>", i+1, p))
	}
	return strings.Join(sections, "\n")
}
