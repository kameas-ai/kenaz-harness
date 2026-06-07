package openaiwire

import (
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// BuildFinalResponse assembles an llm.Response from the accumulators
// maintained by the pump goroutine during an SSE session.
//
//   - textBuf: all text fragments concatenated in order
//   - toolCalls: fully assembled tool-call slice (from ToolCallAccumulator)
//   - usage: token-accounting snapshot
//   - finishReason: last seen finish_reason (may be empty if stream ended abruptly)
func BuildFinalResponse(textBuf *strings.Builder, toolCalls []llm.ToolUse, usage llm.Usage, finishReason string) llm.Response {
	var content []llm.ContentBlock
	if textBuf != nil && textBuf.Len() > 0 {
		content = []llm.ContentBlock{{Type: "text", Text: textBuf.String()}}
	}
	return llm.Response{
		Content:      content,
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}
}

// UsageFromSSE converts an SSEUsage struct to llm.Usage.
func UsageFromSSE(u *SSEUsage) llm.Usage {
	if u == nil {
		return llm.Usage{}
	}
	return llm.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
}
