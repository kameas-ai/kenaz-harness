package llm

// Token estimates for the parts of a request. One estimator for every
// part — tool schemas, system prompt, history — so the parts are
// comparable with each other. It is
// a size measure, not a tokenizer: providers tokenize differently and
// the provider-reported prompt_tokens stays the authority on cost.

// bytesPerToken is the divisor of the estimate: ceil(bytes / 3.5).
// Expressed as 7/2 so the arithmetic stays in integers.
const (
	bytesPerTokenNum = 7
	bytesPerTokenDen = 2
)

// EstimateTokens returns ceil(n / 3.5) for a byte count n; 0 for n <= 0.
func EstimateTokens(n int) int {
	if n <= 0 {
		return 0
	}
	return (n*bytesPerTokenDen + bytesPerTokenNum - 1) / bytesPerTokenNum
}

// EstimateToolSpecTokens estimates what one tool definition costs in the
// prompt: ceil(bytes(name + description + input_schema) / 3.5).
func EstimateToolSpecTokens(t ToolSpec) int {
	return EstimateTokens(len(t.Name) + len(t.Description) + len(t.InputSchema))
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

// MessagesTokens estimates the history part of a request: the summed
// estimate of every message's text, tool-call input, tool-result content
// and raw tool data. Media bytes are not counted — providers bill images
// by their own pixel formulas, which a byte count does not approximate.
func MessagesTokens(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		n := 0
		for _, b := range m.Content {
			n += len(b.Text) + len(b.ToolData)
			if b.ToolUse != nil {
				n += len(b.ToolUse.Name) + len(b.ToolUse.Input)
			}
			if b.ToolResult != nil {
				n += len(b.ToolResult.Content)
			}
		}
		total += EstimateTokens(n)
	}
	return total
}

// PromptComposition is one request's breakdown by part. The part fields
// are estimates from the estimator above; Cached is provider-reported
// (Usage.CachedInputRead of the response). ToolsFull / ToolsSummary
// count tool definitions sent in full vs. listed only by summary.
type PromptComposition struct {
	System       int `json:"system"`
	Tools        int `json:"tools"`
	History      int `json:"history"`
	Attachments  int `json:"attachments"`
	Memory       int `json:"memory"`
	Cached       int `json:"cached"`
	ToolsFull    int `json:"tools_full"`
	ToolsSummary int `json:"tools_summary"`
}
