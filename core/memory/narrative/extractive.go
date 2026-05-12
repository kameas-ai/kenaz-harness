package narrative

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSummaryBytes is the head+tail budget per field. Keeping it small
// ensures per-tool narratives are cheap to embed and store.
const maxSummaryBytes = 256

// ToolNarrative is the structured output of BuildToolNarrative.
// It follows the per-tool extractive schema from FR-003:
//
//	{tool, args_summary, result_summary, duration_ms, success}
type ToolNarrative struct {
	Tool          string `json:"tool"`
	ArgsSummary   string `json:"args_summary"`
	ResultSummary string `json:"result_summary"`
	DurationMs    int64  `json:"duration_ms"`
	Success       bool   `json:"success"`
}

// String renders a compact human-readable representation for the chunk
// Content field so the embedder can produce a meaningful vector.
func (t ToolNarrative) String() string {
	status := "ok"
	if !t.Success {
		status = "error"
	}
	return fmt.Sprintf("[tool:%s status:%s args:%s result:%s dur:%dms]",
		t.Tool, status, t.ArgsSummary, t.ResultSummary, t.DurationMs)
}

// TurnFallback is the mechanically-populated per-turn narrative written
// by BuildTurnFallback when the LLM summariser is unavailable. It
// mirrors the {request, investigated, learned, outcome, next_steps}
// schema from FR-001 / FR-003, but every field is derived
// deterministically from raw content without an LLM call.
type TurnFallback struct {
	Request     string `json:"request"`
	Investigated string `json:"investigated"`
	Learned     string `json:"learned"`
	Outcome     string `json:"outcome"`
	NextSteps   string `json:"next_steps"`
}

// String renders a compact human-readable representation for embedding.
func (t TurnFallback) String() string {
	return fmt.Sprintf("[narrative kind:extractive_fallback request:%s investigated:%s learned:%s outcome:%s next:%s]",
		t.Request, t.Investigated, t.Learned, t.Outcome, t.NextSteps)
}

// ExtractiveBuilder builds narrative chunks from raw content without
// any LLM call. It is safe for concurrent use.
type ExtractiveBuilder struct{}

// NewExtractiveBuilder constructs an ExtractiveBuilder.
func NewExtractiveBuilder() *ExtractiveBuilder {
	return &ExtractiveBuilder{}
}

// BuildToolNarrative builds a ToolNarrative from raw hook-fire inputs.
// The args and result strings are head+tail truncated to maxSummaryBytes
// so the resulting chunk is cheap to embed. Never blocks on I/O.
func (b *ExtractiveBuilder) BuildToolNarrative(toolName, toolArgs, toolResult string, duration time.Duration, success bool) ToolNarrative {
	return ToolNarrative{
		Tool:          toolName,
		ArgsSummary:   headTailTruncate(toolArgs, maxSummaryBytes),
		ResultSummary: headTailTruncate(toolResult, maxSummaryBytes),
		DurationMs:    duration.Milliseconds(),
		Success:       success,
	}
}

// BuildTurnFallback builds a TurnFallback from a list of tool narratives
// produced during the turn plus the last user message and last assistant
// response. All fields are populated mechanically — no LLM required.
// Used as the immediate fallback when the Promoter's synthesis job fails
// on its first attempt (FR-001-C).
func (b *ExtractiveBuilder) BuildTurnFallback(
	lastUserMsg string,
	lastAssistantMsg string,
	tools []ToolNarrative,
) TurnFallback {
	// request: head of the last user message.
	request := headTailTruncate(lastUserMsg, maxSummaryBytes)

	// investigated: list of tool names called.
	var toolNames []string
	for _, t := range tools {
		toolNames = append(toolNames, t.Tool)
	}
	investigated := strings.Join(toolNames, ", ")
	if investigated == "" {
		investigated = "(no tools called)"
	}
	if len(investigated) > maxSummaryBytes {
		investigated = investigated[:maxSummaryBytes]
	}

	// learned: concatenated successful result summaries.
	var learnedParts []string
	for _, t := range tools {
		if t.Success && t.ResultSummary != "" {
			learnedParts = append(learnedParts, t.Tool+": "+t.ResultSummary)
		}
	}
	learned := strings.Join(learnedParts, "; ")
	if learned == "" {
		learned = "(extractive fallback)"
	}
	if len(learned) > maxSummaryBytes {
		learned = learned[:maxSummaryBytes]
	}

	// outcome: head of assistant response.
	outcome := headTailTruncate(lastAssistantMsg, maxSummaryBytes)

	return TurnFallback{
		Request:      request,
		Investigated: investigated,
		Learned:      learned,
		Outcome:      outcome,
		NextSteps:    "(pending LLM synthesis)",
	}
}

// headTailTruncate returns s trimmed to at most maxBytes. When s exceeds
// the budget it preserves the first half and last half of the budget, joined
// by " … " so the reader has context at both ends. The result is always
// valid UTF-8: any multi-byte rune that would be split is consumed into the
// preceding slice.
func headTailTruncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Leave room for the separator.
	sep := " … "
	if maxBytes <= len(sep)+2 {
		// Extremely small budget — just take the head.
		return safePrefix(s, maxBytes)
	}
	half := (maxBytes - len(sep)) / 2
	head := safePrefix(s, half)
	tail := safeSuffix(s, half)
	return head + sep + tail
}

// safePrefix returns the largest UTF-8-valid prefix of s that fits in n
// bytes.
func safePrefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 {
		if utf8.ValidString(s[:n]) {
			return s[:n]
		}
		n--
	}
	return ""
}

// safeSuffix returns the largest UTF-8-valid suffix of s that fits in n
// bytes.
func safeSuffix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) {
		if utf8.ValidString(s[start:]) {
			return s[start:]
		}
		start++
	}
	return ""
}
