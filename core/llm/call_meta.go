package llm

import "errors"

// CallMeta is the provider-returned metadata of one provider call that
// is not usage or content: the ids, the model that actually served the
// call, and whatever per-call extras the provider puts on the response
// (llm-call-telemetry-01LLMTEL01 WP01).
//
// Adapters fill it from bytes they already receive — the SSE frames and
// the response headers of the one request they make — and never make an
// extra round trip for it. Every field is an identifier, an enum-like
// token or a count; none of them is, or may ever carry, model content.
// The only reader is core/llm/genaitrace, which maps it onto gen_ai.* /
// kenaz.provider.* span attributes through a bounded allowlist.
//
// A zero field means "the provider did not report it"; the span simply
// omits the attribute.
type CallMeta struct {
	// ResponseID is the provider's id for this generation: Anthropic
	// "msg_…", OpenAI "chatcmpl-…", OpenRouter's generation id "gen-…",
	// Gemini responseId. It is NEVER a span attribute (Fleet/counsel D32,
	// 2026-10-10: the provider's own traces carry it, so it would join
	// them to member-attributed spans); genaitrace hands it only to the
	// local model-call store, where the OpenRouter stats pull keys on it.
	ResponseID string
	// ResponseModel is the model the provider reports having run, which
	// can differ from the requested one (aliases, OpenRouter routing).
	ResponseModel string
	// RequestID is the provider's request-id response header, when sent.
	RequestID string
	// SystemFingerprint is OpenAI-family's backend configuration id.
	SystemFingerprint string
	// UpstreamProvider is the provider a router (OpenRouter) dispatched
	// the call to, e.g. "Anthropic", "Google AI Studio".
	UpstreamProvider string
	// CacheReadTokens is a prompt-cache read count the adapter saw on the
	// wire but does not fold into Usage (OpenAI / Azure / custom
	// prompt_tokens_details.cached_tokens). Usage.CachedInputRead, when
	// set, takes precedence.
	CacheReadTokens int
	// ToolUsePromptTokens is Gemini usageMetadata.toolUsePromptTokenCount.
	ToolUsePromptTokens int
}

// IsZero reports whether no field is set.
func (m *CallMeta) IsZero() bool {
	return m == nil || *m == (CallMeta{})
}

// ErrProviderStatsNotReady marks a provider stats pull (OpenRouter's
// /api/v1/generation, llm-call-telemetry-01LLMTEL01 WP02) that should be
// retried later: the stats are not there yet, the provider throttled, or
// the transport failed. Any other pull error is final.
var ErrProviderStatsNotReady = errors.New("llm: provider stats not available yet")
