package llm

import (
	"fmt"
	"strings"
)

// Tool-call id handling shared across adapters.
//
// The cross-provider invariant: a tool result must echo a non-empty
// tool_call_id that matches its parent tool call. Several providers omit the
// id that OpenAI-compatible tool calling normally carries — Google Gemini's
// native function-call format has none, and some models behind OpenRouter
// (e.g. Moonshot/kimi) stream tool calls with no id at all. When the id is
// empty the follow-up tool result goes out with an empty tool_call_id and the
// provider rejects the next turn with a cryptic error (Moonshot:
// "Invalid request: tool_call_id  is not found", status 400). These helpers
// give every adapter one place to synthesize a stable id, and give the
// registry one place to reject an empty id before it reaches the wire.

// SynthesizeToolCallID returns a stable, non-empty tool-call id for providers
// that emit or stream tool calls without one. genID is a per-response unique
// token when available (e.g. an OpenRouter "gen-…" id); index is the tool
// call's position in the response. The result is generated once, stored on the
// assistant message, and reused verbatim for the tool result so the pair stays
// matched. When genID is empty the id falls back to a positional "call_N",
// which is unique within a single response.
func SynthesizeToolCallID(genID string, index int) string {
	if genID != "" {
		return fmt.Sprintf("%s-%d", genID, index)
	}
	return fmt.Sprintf("call_%d", index)
}

// EnsureToolCallID returns id unchanged when it is non-empty, otherwise a
// synthesized id. Adapters call this on every tool call they parse so a
// provider that supplies ids keeps them and one that omits them still yields a
// usable id.
func EnsureToolCallID(id, genID string, index int) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return SynthesizeToolCallID(genID, index)
}

// ValidateToolCallIDs enforces that every tool call and every typed tool result
// in a request carries a non-empty id. An empty id is never valid for any
// provider, so running this before dispatch converts a whole class of
// provider-side failures (cryptic 400s about a missing tool_call_id) into a
// clear, adapter-agnostic diagnostic that names the offending message.
//
// It deliberately does NOT require a tool result to match a tool call present
// in the same request: context compaction and history editing can legitimately
// drop a parent turn, and matching is the provider's to enforce. Only empty
// ids — always a bug — are rejected. Legacy raw-bytes tool-result blocks
// (ContentBlock.ToolData) carry no typed id and are skipped.
func ValidateToolCallIDs(messages []Message) error {
	for mi := range messages {
		for bi := range messages[mi].Content {
			b := messages[mi].Content[bi]
			switch b.Type {
			case "tool_use":
				if b.ToolUse == nil {
					continue
				}
				if strings.TrimSpace(b.ToolUse.ID) == "" {
					return fmt.Errorf("llm: message %d block %d: tool_use %q has an empty id — the adapter must synthesize one (see SynthesizeToolCallID)", mi, bi, b.ToolUse.Name)
				}
			case "tool_result":
				if b.ToolResult == nil {
					continue // legacy ToolData path carries no typed id
				}
				if strings.TrimSpace(b.ToolResult.ToolUseID) == "" {
					return fmt.Errorf("llm: message %d block %d: tool_result has an empty tool_use_id", mi, bi)
				}
			}
		}
	}
	return nil
}
