package narrative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SynthesisResult is the parsed output of a successful synthesis call.
// It mirrors the {request, investigated, learned, outcome, next_steps}
// schema the summariser model is instructed to produce.
type SynthesisResult struct {
	Request      string `json:"request"`
	Investigated string `json:"investigated"`
	Learned      string `json:"learned"`
	Outcome      string `json:"outcome"`
	NextSteps    string `json:"next_steps"`
}

// String renders a compact human-readable representation for embedding.
func (r SynthesisResult) String() string {
	return fmt.Sprintf(
		"[narrative kind:synthesised request:%s investigated:%s learned:%s outcome:%s next:%s]",
		r.Request, r.Investigated, r.Learned, r.Outcome, r.NextSteps,
	)
}

// ErrSynthesisFailed is returned when the summariser produces output
// that cannot be parsed into SynthesisResult. The Promoter treats this
// as a retriable error.
var ErrSynthesisFailed = errors.New("narrative: synthesis response parse failed")

// LLMCaller is the narrow interface the SyntheticBuilder uses to invoke
// the configured summariser model. The production wiring resolves the
// summariser via Settings.SummarizerProfileID and the llm.ProviderProfile
// registry; tests pass a fake. The method contract mirrors the existing
// llm.Caller pattern in the harness.
type LLMCaller interface {
	// Complete sends the system+user prompt to the model and returns
	// the raw text response. Context cancellation bubbles up.
	Complete(ctx context.Context, system, user string) (string, error)
}

// SyntheticBuilder calls the summariser model to produce a structured
// narrative for a completed agent turn. It is instantiated by the
// Promoter and is safe for concurrent use when LLMCaller is safe for
// concurrent use.
type SyntheticBuilder struct {
	caller LLMCaller
}

// NewSyntheticBuilder constructs a SyntheticBuilder with the given caller.
// A nil caller causes Build to return ErrNoLLMCaller.
func NewSyntheticBuilder(caller LLMCaller) *SyntheticBuilder {
	return &SyntheticBuilder{caller: caller}
}

// ErrNoLLMCaller is returned by Build when no LLM caller is wired.
var ErrNoLLMCaller = errors.New("narrative: no llm caller configured")

// systemPrompt instructs the model to respond with a JSON object that
// matches SynthesisResult. The schema is explicit so parse errors catch
// model hallucinations.
const systemPrompt = `You are a memory summariser. Analyse the given agent turn and respond with a JSON object and nothing else:

{
  "request":      "brief statement of what the user asked",
  "investigated": "concise summary of what was examined or tried",
  "learned":      "key facts discovered",
  "outcome":      "what was accomplished or produced",
  "next_steps":   "likely follow-on actions or open questions"
}

Rules:
- All fields are required; use "(none)" for empty fields.
- Keep each field under 300 characters.
- Respond with valid JSON only — no markdown fences, no prose.`

// Build calls the summariser model and parses the result. On parse
// failure it returns ErrSynthesisFailed; on LLM error it returns the
// wrapped LLM error. Both are retriable by the Promoter.
func (b *SyntheticBuilder) Build(ctx context.Context, payload JobPayload) (SynthesisResult, error) {
	if b.caller == nil {
		return SynthesisResult{}, ErrNoLLMCaller
	}

	userMsg := buildUserMessage(payload)
	raw, err := b.caller.Complete(ctx, systemPrompt, userMsg)
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("narrative: synthesis call: %w", err)
	}

	result, err := parseSynthesisJSON(raw)
	if err != nil {
		return SynthesisResult{}, fmt.Errorf("%w: %v", ErrSynthesisFailed, err)
	}
	return result, nil
}

// buildUserMessage assembles the user-facing summary of the turn to send
// to the summariser.
func buildUserMessage(payload JobPayload) string {
	var sb strings.Builder
	sb.WriteString("User request:\n")
	sb.WriteString(headTailTruncate(payload.LastUserMsg, 800))
	if len(payload.ToolNarratives) > 0 {
		sb.WriteString("\n\nTools called:\n")
		for _, t := range payload.ToolNarratives {
			sb.WriteString("- " + t.String() + "\n")
		}
	}
	if payload.LastAssistantMsg != "" {
		sb.WriteString("\nAssistant response:\n")
		sb.WriteString(headTailTruncate(payload.LastAssistantMsg, 800))
	}
	return sb.String()
}

// parseSynthesisJSON attempts to unmarshal raw into SynthesisResult.
// It strips Markdown code fences defensively.
func parseSynthesisJSON(raw string) (SynthesisResult, error) {
	s := strings.TrimSpace(raw)
	// Strip optional ```json ... ``` fences.
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = strings.TrimSpace(lines[1])
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	var result SynthesisResult
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return SynthesisResult{}, fmt.Errorf("json.Unmarshal: %w (raw: %q)", err, truncateForLog(raw, 200))
	}
	return result, nil
}

// truncateForLog returns s truncated to n bytes for use in error messages.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
