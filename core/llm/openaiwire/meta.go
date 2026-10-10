package openaiwire

import (
	"net/http"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// FrameMeta is the call metadata every OpenAI-compatible chat-completions
// SSE chunk carries next to its choices. Adapters embed it in their frame
// struct so the one json.Unmarshal they already do per frame also fills
// it — no second parse (llm-call-telemetry-01LLMTEL01 WP01).
type FrameMeta struct {
	ID                string `json:"id"`
	Model             string `json:"model"`
	SystemFingerprint string `json:"system_fingerprint"`
}

// ApplyTo folds f into m. The first non-empty value wins: every chunk of
// one stream repeats the same id / model / fingerprint.
func (f FrameMeta) ApplyTo(m *llm.CallMeta) {
	if m == nil {
		return
	}
	if m.ResponseID == "" {
		m.ResponseID = f.ID
	}
	if m.ResponseModel == "" {
		m.ResponseModel = f.Model
	}
	if m.SystemFingerprint == "" {
		m.SystemFingerprint = f.SystemFingerprint
	}
}

// PromptTokensDetails is usage.prompt_tokens_details on the OpenAI wire.
// cached_tokens is part of prompt_tokens, not added to it.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// RequestIDFromHeader returns the provider's request id response header:
// x-request-id (OpenAI and most compatible servers), else apim-request-id
// (Azure API Management). Empty when neither is sent.
func RequestIDFromHeader(h http.Header) string {
	if h == nil {
		return ""
	}
	if v := h.Get("x-request-id"); v != "" {
		return v
	}
	return h.Get("apim-request-id")
}

// MetaPtr returns a pointer to a copy of m, or nil when m is empty, for
// llm.Response.Meta.
func MetaPtr(m llm.CallMeta) *llm.CallMeta {
	if m.IsZero() {
		return nil
	}
	return &m
}
