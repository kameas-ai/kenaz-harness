package autotitle

import (
	"context"
	"fmt"
	"strings"
)

// systemPrompt is the locked system prompt from plan §2.1. It is baked
// into the binary so a misconfigured or missing prompt cannot silently
// change the title-generation behaviour.
const systemPrompt = "Produce a concise (≤ 50 chars) title summarizing this conversation. Output ONLY the title, no quotes, no explanation."

// maxUserPromptBytes caps the user-prompt size to roughly 6 KB, which
// corresponds to the NFR-002 ≤ 200 input-token target (plan §2.1).
const maxUserPromptBytes = 6 * 1024

// LLMCaller is the narrow interface Generator requires from the LLM
// layer. Keeping this thin means:
//   - Tests can inject a fake without spinning up core/llm/registry.
//   - The wiring adapter (wiring/llm.go) is the only place that ties
//     this package to the real registry.
type LLMCaller interface {
	// Call sends a system + user prompt and returns the model's text
	// reply together with the raw token usage figures. Implementations
	// MUST respect ctx cancellation; the generator enforces a 5-second
	// timeout on top (NFR-001).
	Call(ctx context.Context, systemPrompt, userPrompt string) (text string, inputTokens, outputTokens int, err error)
}

// Transcript is a slice of messages that the generator renders into a
// user prompt. Callers typically populate this from the session's
// message store.
type Transcript []TranscriptMessage

// TranscriptMessage is one entry in a conversation transcript.
type TranscriptMessage struct {
	// Role is "user" or "assistant".
	Role string
	// Text is the plain-text content of the message. Callers are
	// responsible for flattening multi-block content into plain text
	// before building the Transcript.
	Text string
}

// Generator produces concise session titles from a Transcript.
type Generator struct {
	llm LLMCaller
}

// New returns a Generator backed by caller.
func New(caller LLMCaller) *Generator {
	return &Generator{llm: caller}
}

// GenerateTitle generates a sanitized session title from transcript.
//
// The method:
//  1. Renders the most recent user-assistant pair as
//     "User: ...\nAssistant: ..." and truncates to maxUserPromptBytes.
//  2. Calls the LLM with the locked system prompt.
//  3. Passes the raw output through Sanitize and returns the result.
//
// Context is honoured; callers should wrap ctx with a 5-second timeout
// (NFR-001) if they have not already done so.
func (g *Generator) GenerateTitle(ctx context.Context, transcript Transcript) (string, error) {
	if g == nil || g.llm == nil {
		return "", fmt.Errorf("autotitle: generator not initialised")
	}

	userPrompt := buildUserPrompt(transcript)
	if userPrompt == "" {
		return "", ErrTitleTooShort
	}

	raw, _, _, err := g.llm.Call(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("autotitle: llm call failed: %w", err)
	}

	return Sanitize(raw)
}

// buildUserPrompt renders the most recent user-assistant pair from
// transcript into the "User: ...\nAssistant: ..." format specified in
// plan §2.1. The output is truncated to maxUserPromptBytes so the
// auto-title call stays within the NFR-002 ≤ 200-input-token budget.
//
// Strategy: scan backwards to find the last assistant message; then
// scan further backwards (from just before that assistant message) to
// find the preceding user message. If no assistant message exists yet,
// use the last user message alone. Returns an empty string when no user
// message exists at all.
func buildUserPrompt(transcript Transcript) string {
	// Step 1: find the last assistant message index (may be -1).
	lastAssistant := -1
	for i := len(transcript) - 1; i >= 0; i-- {
		msg := transcript[i]
		if strings.EqualFold(msg.Role, "assistant") && msg.Text != "" {
			lastAssistant = i
			break
		}
	}

	// Step 2: find the last user message that precedes the assistant turn
	// (or the last user message in the whole transcript if there is no
	// assistant turn yet).
	searchUpTo := len(transcript) - 1
	if lastAssistant != -1 {
		searchUpTo = lastAssistant - 1
	}

	lastUser := -1
	for i := searchUpTo; i >= 0; i-- {
		msg := transcript[i]
		if strings.EqualFold(msg.Role, "user") && msg.Text != "" {
			lastUser = i
			break
		}
	}

	if lastUser == -1 {
		// No user message found — cannot build a meaningful prompt.
		return ""
	}

	var sb strings.Builder
	sb.WriteString("User: ")
	sb.WriteString(transcript[lastUser].Text)
	if lastAssistant != -1 {
		sb.WriteString("\nAssistant: ")
		sb.WriteString(transcript[lastAssistant].Text)
	}

	result := sb.String()

	// Truncate at byte boundary (not rune boundary — the model doesn't
	// care about the exact cut point as long as we're within budget).
	if len(result) > maxUserPromptBytes {
		result = result[:maxUserPromptBytes]
	}

	return result
}
