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
	"unicode/utf8"

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
// per-family renderer instead — see variantRenderer. When
// tmpl.AttentionPlacement is also set, applyAttentionPlacement (WP02)
// anchors the highest-priority part at both the start and end of the
// composed prompt — off by default, so an un-profiled or
// non-opted-in model's output is unaffected. The real per-family
// template *content* remains later work (a possible WP04 few-shot/
// prefill mechanism); this package only wires the rendering/placement
// mechanism plus one illustrative XML variant so a profile can prove
// it takes effect.
func Compose(tmpl *corellm.PromptTemplateRef, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	var composed string
	if tmpl != nil {
		if render := variantRenderer(tmpl.Format); render != nil {
			composed = render(kept)
		} else {
			composed = strings.Join(kept, "\n\n")
		}
	} else {
		composed = strings.Join(kept, "\n\n")
	}
	if tmpl != nil && tmpl.AttentionPlacement {
		composed = applyAttentionPlacement(composed, kept)
	}
	return composed
}

// attentionPlacementMaxChars bounds the token cost of the recency-anchor
// duplicate applyAttentionPlacement appends. Attention placement should
// reinforce a short, high-priority directive, not double the whole
// prompt — an opted-in profile must not silently inflate every call's
// context by the full first part. ~4 chars/token, so 480 chars is
// roughly 120 tokens (spec §4/§5: "token cost bounded").
const attentionPlacementMaxChars = 480

// applyAttentionPlacement is the WP02 primacy/recency transform: it
// anchors the first surviving part — the highest-priority instruction
// by construction, since callers order parts most-important-first (see
// exec_compute.go's composePrompt call sites: graph base, then node
// role) — at *both* ends of the composed prompt. It is already at the
// start (primacy) by virtue of being kept[0]; this appends a bounded
// duplicate at the end (recency), since models attend more to the start
// and end of a context window than the middle.
//
// Off unless tmpl.AttentionPlacement is true (profile opt-in, spec §4:
// "off by default"). A composed empty string or an empty kept slice is
// returned unchanged — nothing to anchor.
func applyAttentionPlacement(composed string, kept []string) string {
	if composed == "" || len(kept) == 0 || kept[0] == "" {
		return composed
	}
	reminder := truncateForAttention(kept[0], attentionPlacementMaxChars)
	return composed + "\n\n" + "[Reminder — see above]\n" + reminder
}

// truncateForAttention bounds s to maxChars bytes, breaking at the last
// whitespace boundary at or before the limit so the reminder doesn't
// end mid-word. s shorter than maxChars is returned unchanged.
//
// s[:maxChars] can land mid-rune (maxChars is a byte offset, not a rune
// count) — but that slice is only fed to LastIndexAny as a search
// haystack, never returned: every byte LastIndexAny can match (space,
// \n, \t) is single-byte ASCII, so a found index always sits on a rune
// boundary regardless of how s[:maxChars] itself was cut. The corrupted
// bytes, if any, live past the found index and are never included in
// the result.
//
// When no whitespace boundary exists in the first maxChars bytes (a
// long CJK/Cyrillic run, a base64 blob, a long URL), LastIndexAny
// returns -1 and cut falls back to the raw byte offset maxChars — which
// itself can land mid-rune. Walk it back to the nearest rune boundary
// so s[:cut] is never invalid UTF-8.
func truncateForAttention(s string, maxChars int) string {
	if len(s) <= maxChars {
		return s
	}
	cut := strings.LastIndexAny(s[:maxChars], " \n\t")
	if cut <= 0 {
		cut = maxChars
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
	}
	return strings.TrimRight(s[:cut], " \n\t") + " …"
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
// content is later work, so the tagging here is intentionally minimal
// — it exists to prove the variant-selection mechanism, not to be the
// final XML shaping.
func renderXMLSections(parts []string) string {
	sections := make([]string, 0, len(parts))
	for i, p := range parts {
		sections = append(sections, fmt.Sprintf("<section index=\"%d\">\n%s\n</section>", i+1, p))
	}
	return strings.Join(sections, "\n")
}
