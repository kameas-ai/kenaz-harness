// Package tokenizer estimates token counts for LLM request payloads.
//
// The package is leaf infrastructure: it has no dependency on
// core/llm or any provider adapter, so adapters can call it without
// import cycles. Callers translate their richer message types to the
// minimal Message shape exported here.
//
// The current implementation is a rune-count heuristic
// (roughly runeCount/4 plus a small per-message framing overhead).
// This matches the documented "≤ 25% off real tiktoken" budget noted
// in plan §R6 and is suitable as a trigger threshold for compaction —
// trigger one turn early or late is acceptable.
//
// TODO: per-provider tokenizer dispatch. When a real tiktoken-go (or
// equivalent) dependency is admitted, gate accuracy-sensitive call
// sites on a provider hint and fall back to the heuristic for unknown
// providers.
package tokenizer

import "unicode/utf8"

// messageFramingOverhead is the per-message constant added to the
// rune-derived count to approximate the role / formatting tokens that
// chat-style providers wrap each message in. Four is the rough
// consensus across OpenAI and Anthropic chat templates; tightening
// this is cheap if a future tokenizer dispatch improves accuracy.
const messageFramingOverhead = 4

// runesPerToken is the heuristic divisor: English text averages ~4
// characters per token under BPE tokenizers. Rounding up (see ceilDiv)
// keeps the heuristic from underestimating.
const runesPerToken = 4

// Message is the minimal shape the tokenizer needs. Adapters and the
// chat runner translate their concrete message types (which carry tool
// calls, attachments, role enums, etc.) into this shape before
// calling CountRequestTokens.
type Message struct {
	// Role is the speaker — "system" / "user" / "assistant" / "tool".
	// Currently only its presence is significant (it adds framing
	// overhead); future per-provider dispatch may use it.
	Role string

	// Content is the textual payload. Tool calls and attachments are
	// the caller's responsibility to flatten into text first.
	Content string
}

// CountRequestTokens returns an estimated token count for the given
// system prompt plus chat messages. The estimate is intended for
// trigger-threshold checks (e.g. "are we at 80% of cap?") and is
// documented to be within ~25% of a real tokenizer's count for
// English text.
//
// The system prompt is treated as one implicit message: it contributes
// the framing overhead exactly once even when empty, matching the way
// providers always reserve space for a system slot.
func CountRequestTokens(systemPrompt string, messages []Message) int {
	total := 0

	// System prompt — always counts as one message slot, even when
	// empty, because the request payload reserves the slot regardless.
	total += countContent(systemPrompt) + messageFramingOverhead

	for _, m := range messages {
		total += countContent(m.Content) + messageFramingOverhead
		// Role is short and rarely exceeds 1 token in real
		// tokenizers; we fold it into the framing overhead constant
		// rather than counting it separately.
	}

	return total
}

// countContent returns the rune-derived token estimate for a single
// content string. Empty strings cost zero tokens (the caller adds
// framing separately).
func countContent(s string) int {
	if s == "" {
		return 0
	}
	runes := utf8.RuneCountInString(s)
	return ceilDiv(runes, runesPerToken)
}

// ceilDiv returns ceil(a/b) for non-negative integers. Used so the
// heuristic rounds up — better to think we're slightly closer to the
// cap than slightly farther.
func ceilDiv(a, b int) int {
	if b <= 0 {
		return 0
	}
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}
