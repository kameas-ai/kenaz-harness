// Package prompts holds shared, embedded system-prompt assets and the
// composition helper used by the agentgraph kernel and by graph-authoring
// code (e.g. the chat GraphLoader in core/rpc/api.go). Package prompts is
// a leaf: it imports nothing from core/agentgraph, so core/agentgraph can
// safely import it (as exec_compute.go's composePrompt does) without
// creating an import cycle.
package prompts

import (
	_ "embed"
	"strings"
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
// prompt. Each part is trimmed of surrounding whitespace; empty parts
// are dropped; the survivors are joined with a blank line ("\n\n") so
// the sections stay visually distinct. A single non-empty part is
// returned as-is; all-empty input yields "".
func Compose(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n\n")
}
