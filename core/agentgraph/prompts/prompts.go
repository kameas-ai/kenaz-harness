// Package prompts holds shared, embedded system-prompt assets for the
// agentgraph kernel and its graph authors. Keeping these in a leaf
// subpackage (imported by graph-building code, not by the kernel itself)
// avoids any import cycle with core/agentgraph while letting both the
// kernel's callers and tests reference a single canonical constitution.
package prompts

import _ "embed"

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
