// Package openaiwire provides a shared library for adapters that speak
// the OpenAI Chat Completions wire format (OpenAI, OpenRouter, and any
// future OpenAI-compatible adapter).
//
// Functions operate on a Base struct that carries the adapter-specific
// configuration (endpoint, auth header, headers for ranking/routing).
// There is intentionally NO inheritance — adapters embed Base and
// override URL/auth/capabilities only; the core body builder and SSE
// parser are shared.
//
// Accepted OpenAI-wire shape (subset relevant to this library):
//
//	POST /v1/chat/completions
//	Content-Type: application/json
//	Authorization: Bearer <key>
//	data: {"choices":[{"delta":{"content":"...","tool_calls":[...]}}]}
//	data: [DONE]
//
// (provider-implementation-uniformity-01KQ8V4F WP03)
package openaiwire

import (
	"net/http"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/httpx"
)

// Base is the shared configuration struct embedded by OpenAI-wire
// adapters. It is NOT a base class — it carries no virtual methods.
// Adapters compose it and delegate body building + SSE parsing to
// the functions in this package.
type Base struct {
	// HTTPClient is the transport. Should have no request-level timeout —
	// callers rely on context cancellation.
	HTTPClient *http.Client
	// ChatEndpoint is the full URL of the chat completions endpoint.
	ChatEndpoint string
	// DefaultHeaders are headers that are set on every request.
	// Typical use: "X-Title", "HTTP-Referer" for OpenRouter ranking.
	DefaultHeaders map[string]string
}

// NewBase returns a Base with a keepalive-armed HTTP client and no
// default headers.
func NewBase(chatEndpoint string) Base {
	return Base{
		HTTPClient:   &http.Client{Transport: httpx.DefaultTransport()},
		ChatEndpoint: chatEndpoint,
	}
}

// Client returns the HTTP client, falling back to http.DefaultClient
// when none has been set (defensive; production paths always configure
// one via NewBase or WithHTTPClient).
func (b *Base) Client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return http.DefaultClient
}

// WithHTTPClient replaces the transport. Returns a pointer to the same
// Base for fluent construction.
func (b *Base) WithHTTPClient(c *http.Client) *Base {
	if c != nil {
		b.HTTPClient = c
	}
	return b
}

// KnobsToParams extracts the RequestKnobs fields that map directly to
// the standard OpenAI Params map keys (temperature, top_p, max_tokens,
// presence_penalty, frequency_penalty, seed, stop). Adapters merge the
// returned map into their body builder BEFORE applying Params from the
// profile defaults so that Knobs always takes precedence.
//
// Knobs that require special wire handling (reasoning_effort,
// parallel_tool_calls) are NOT included here; body.go applies them
// explicitly.
//
// response_format is NOT applied from Knobs at all, by either
// function — this corrects a docstring that previously (falsely)
// grouped it with reasoning_effort/parallel_tool_calls as "applied
// explicitly" by body.go (structured-output-is-reachable-01PMZE14
// spec §1.5/FR-005). body.go's response-format block reads
// req.ResponseFormat / req.JSONMode directly; RequestKnobs.
// ResponseFormatMode and .JSONMode (capabilities.go) have zero
// readers anywhere in the tree. Wiring them as an override layer above
// req.ResponseFormat is deferred — see this package's knobcoverage
// registration (knob_coverage.go) for the blocker/owner/date, per
// CLAUDE.md's unwired-sweep rule that a justification names the change
// that will delete the line rather than "we'll get to it."
func KnobsToParams(k *llm.RequestKnobs) map[string]any {
	if k == nil {
		return nil
	}
	out := make(map[string]any, 8)
	if k.Temperature != nil {
		out["temperature"] = *k.Temperature
	}
	if k.TopP != nil {
		out["top_p"] = *k.TopP
	}
	if k.MaxTokens > 0 {
		out["max_tokens"] = k.MaxTokens
	}
	if k.FrequencyPenalty != nil {
		out["frequency_penalty"] = *k.FrequencyPenalty
	}
	if k.PresencePenalty != nil {
		out["presence_penalty"] = *k.PresencePenalty
	}
	if k.Seed != nil {
		out["seed"] = *k.Seed
	}
	if len(k.StopSequences) > 0 {
		out["stop"] = k.StopSequences
	}
	return out
}
