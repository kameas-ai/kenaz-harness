package llm

import (
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/llm/tokenizer"
)

// Token estimates for the parts of a request. Every part is measured with
// core/llm/tokenizer — the same rule compaction and the request-too-large
// check use — so the composition, compaction thresholds and the
// too-large diagnosis agree on what a request weighs. It is a size
// measure, not a tokenizer: providers tokenize differently and the
// provider-reported prompt_tokens stays the authority on cost.

// EstimateTokens is the per-rune estimate of one piece of text with no
// message framing (tokenizer.CountText).
func EstimateTokens(s string) int {
	return tokenizer.CountText(s)
}

// EstimateToolSpecTokens estimates what one tool definition costs in the
// prompt: the per-rune rule over name + description + input_schema.
func EstimateToolSpecTokens(t ToolSpec) int {
	return tokenizer.CountText(t.Name + t.Description + string(t.InputSchema))
}

// ToolSpecTokens returns t.TokenEst when the producer computed it, else
// computes it. Callers summing a catalog use this so specs built without
// discovery metadata (tests, synthetic tools) still count.
func ToolSpecTokens(t ToolSpec) int {
	if t.TokenEst > 0 {
		return t.TokenEst
	}
	return EstimateToolSpecTokens(t)
}

// ToolsTokens sums ToolSpecTokens over a catalog.
func ToolsTokens(tools []ToolSpec) int {
	total := 0
	for _, t := range tools {
		total += ToolSpecTokens(t)
	}
	return total
}

// SystemTokens is the system prompt's share of tokenizer.CountRequestTokens:
// its content plus the system slot's framing.
func SystemTokens(system string) int {
	return tokenizer.CountRequestTokens(system, nil)
}

// MessagesTokens is the history's share of tokenizer.CountRequestTokens:
// each message's text, tool-call names and inputs, tool-result content
// and raw tool data, plus per-message framing — without the system slot,
// which SystemTokens counts. SystemTokens(s) + MessagesTokens(m) equals
// CountRequestTokens over the same request. Media bytes are not counted
// (providers bill images by their own pixel formulas).
func MessagesTokens(msgs []Message) int {
	if len(msgs) == 0 {
		return 0
	}
	flat := make([]tokenizer.Message, 0, len(msgs))
	for _, m := range msgs {
		var b strings.Builder
		for _, c := range m.Content {
			b.WriteString(c.Text)
			b.Write(c.ToolData)
			if c.ToolUse != nil {
				b.WriteString(c.ToolUse.Name)
				b.Write(c.ToolUse.Input)
			}
			if c.ToolResult != nil {
				b.Write(c.ToolResult.Content)
			}
		}
		flat = append(flat, tokenizer.Message{Role: string(m.Role), Content: b.String()})
	}
	return tokenizer.CountRequestTokens("", flat) - tokenizer.CountRequestTokens("", nil)
}

// PromptComposition is one request's breakdown by part. The part fields
// are estimates from the helpers above; Cached is provider-reported
// (Usage.CachedInputRead of the response). ToolsFull / ToolsSummary
// count tool definitions sent in full vs. listed only by summary.
// In-process only — it is never encoded.
type PromptComposition struct {
	System       int
	Tools        int
	History      int
	Attachments  int
	Memory       int
	Cached       int
	ToolsFull    int
	ToolsSummary int
}

// InputExcludesCache reports whether a provider kind's Usage.InputTokens
// leaves out the prompt-cache read and write counts. Anthropic's
// input_tokens is the uncached remainder; OpenAI-compatible prompt_tokens
// (OpenRouter, OpenAI, Azure, custom) and Gemini's promptTokenCount
// already include them.
func InputExcludesCache(providerKind string) bool {
	return providerKind == "anthropic"
}

// PromptTokensTotal is the whole prompt a call sent, as the provider
// counted it, under either convention: InputTokens plus the cache read
// and write counts where the provider reports those separately.
func PromptTokensTotal(u Usage, providerKind string) int {
	if InputExcludesCache(providerKind) {
		return u.InputTokens + u.CachedInputRead + u.CachedInputWrite
	}
	return u.InputTokens
}
