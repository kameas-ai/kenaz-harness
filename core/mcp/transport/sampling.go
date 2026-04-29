package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// llmSamplingHandler bridges server-initiated sampling/createMessage
// requests onto the host LLM. Provider+model selection is delegated
// to a function injected at construction time so the handler stays
// independent of any specific provider package.
//
// SAMPLING COST AMPLIFICATION RISK: sampling is gated per recipe and
// defaults OFF — a malicious or buggy server could otherwise issue
// arbitrary completions on the user's account. The pool's
// SetSamplingEnabled gate is the user's consent boundary. Do NOT
// flip the default to on without the user-trust mission shipping.
type llmSamplingHandler struct {
	registry corellm.Registry
	selector func() (provider, model string)
}

// LLMSamplingHandler returns a SamplingHandler that adapts onto a
// core/llm.Registry. The selector returns the active (provider, model)
// pair at request time — typically wired to the same source the
// chat surface uses, so sampling rides the user's "currently active"
// provider without needing a separate config knob.
func LLMSamplingHandler(reg corellm.Registry, selector func() (provider, model string)) SamplingHandler {
	return &llmSamplingHandler{registry: reg, selector: selector}
}

// CreateMessage translates an MCP SamplingRequest into a
// corellm.GenerationRequest, drains the resulting stream
// synchronously, and returns the terminal response shaped as a
// SamplingResponse. The server argument is accepted for symmetry
// with the WP01 interface; per-server gating happens in the pool's
// dispatch path before this handler is reached.
func (h *llmSamplingHandler) CreateMessage(ctx context.Context, _ string, req SamplingRequest) (SamplingResponse, error) {
	if h.registry == nil {
		return SamplingResponse{}, errors.New("transport: sampling handler has no registry")
	}
	if h.selector == nil {
		return SamplingResponse{}, errors.New("transport: sampling handler has no selector")
	}
	provider, model := h.selector()
	if provider == "" {
		return SamplingResponse{}, errors.New("transport: no active provider for sampling")
	}

	gen := corellm.GenerationRequest{
		ProfileID: provider,
		Model:     model,
		System:    req.SystemPrompt,
		Messages:  mapSamplingMessages(req.Messages),
	}
	if req.MaxTokens > 0 || req.Temperature != nil || len(req.StopSequences) > 0 {
		gen.Params = make(map[string]any)
		if req.MaxTokens > 0 {
			gen.Params["max_tokens"] = req.MaxTokens
		}
		if req.Temperature != nil {
			gen.Params["temperature"] = *req.Temperature
		}
		if len(req.StopSequences) > 0 {
			gen.Params["stop_sequences"] = req.StopSequences
		}
	}

	stream, err := h.registry.Stream(ctx, gen)
	if err != nil {
		return SamplingResponse{}, fmt.Errorf("transport: sampling stream: %w", err)
	}

	// Drain the event channel synchronously. The stream contract is
	// that the channel closes on terminal state; Final blocks until
	// it does, so calling Final after the drain is safe.
	for range stream.Events() {
		// Sampling does not surface streaming deltas to the server —
		// the spec models sampling as a single completion. We pull the
		// terminal Response from Final() below.
	}
	final, err := stream.Final()
	if err != nil {
		return SamplingResponse{}, fmt.Errorf("transport: sampling final: %w", err)
	}

	text := concatText(final.Content)
	contentJSON, err := json.Marshal(map[string]any{
		"type": "text",
		"text": text,
	})
	if err != nil {
		return SamplingResponse{}, fmt.Errorf("transport: marshal sampling content: %w", err)
	}
	resp := SamplingResponse{
		Role:       "assistant",
		Content:    contentJSON,
		Model:      model,
		StopReason: final.FinishReason,
	}
	return resp, nil
}

// mapSamplingMessages translates MCP SamplingMessage entries into
// corellm.Message entries. The MCP content is JSON-encoded as either
// a {type:"text", text:...} object or an image content block — for
// v1 we only forward text content, dropping non-text blocks with no
// error so the server still gets a completion.
func mapSamplingMessages(in []SamplingMessage) []corellm.Message {
	out := make([]corellm.Message, 0, len(in))
	for _, m := range in {
		role := corellm.Role(m.Role)
		switch role {
		case corellm.RoleSystem, corellm.RoleAssistant, corellm.RoleUser, corellm.RoleTool:
		default:
			role = corellm.RoleUser
		}
		text := extractText(m.Content)
		out = append(out, corellm.Message{
			Role:    role,
			Content: []corellm.ContentBlock{corellm.ContentBlockFromText(text)},
		})
	}
	return out
}

// extractText decodes the MCP SamplingMessage.Content union and
// returns the text body (empty string for non-text blocks).
func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var typed struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &typed); err != nil {
		return ""
	}
	if typed.Type != "" && typed.Type != "text" {
		return ""
	}
	return typed.Text
}

// concatText flattens a corellm.Response.Content slice into a single
// string. Sampling responses are single-shot text completions; we
// drop tool-use parts since the MCP spec does not model sampling
// tool calls in the response shape.
func concatText(parts []corellm.ContentBlock) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0].Text
	}
	total := 0
	for _, p := range parts {
		total += len(p.Text)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p.Text...)
	}
	return string(out)
}
