package rpc

import (
	"errors"
	"strings"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// overflowKeywords mirrors the heuristic used by the context-overflow
// recovery path in the chat runner (FR-005 / WP05) so the two
// classification functions stay in sync.
var overflowKeywords = []string{
	"context length",
	"maximum context",
	"context window",
	"too many tokens",
	"exceeds maximum",
	"input is too long",
	"message exceeds maximum length",
}

// MapLLMError converts a core/llm error into a structured *RPCError suitable
// for returning to the frontend across the Wails RPC boundary (FR-008 /
// agent-loop-robustness-parity WP08).
//
// The mapping is:
//
//   - *ErrAuth / *ErrProviderAuthFailed → code "auth", not retryable,
//     hint to check the API key in Settings.
//   - *ErrTransient (status 413 or any 5xx) → code "transient", retryable.
//   - *ErrRetryBudgetExhausted → code "budget_exhausted", not retryable,
//     hint to re-send the message (the LLM provider may be temporarily down).
//   - *ErrInvalidRequest with context-overflow keywords → code "context_overflow",
//     not retryable, hint to start a new conversation or use compaction.
//   - *ErrInvalidRequest (other) → code "invalid_request", not retryable.
//   - nil → nil.
//   - anything else → code "internal", not retryable.
func MapLLMError(err error) *RPCError {
	if err == nil {
		return nil
	}

	// ErrAuth / ErrProviderAuthFailed.
	var auth *corellm.ErrAuth
	var providerAuth *corellm.ErrProviderAuthFailed
	if errors.As(err, &providerAuth) || errors.As(err, &auth) {
		msg := err.Error()
		if providerAuth != nil {
			msg = providerAuth.Reason
		} else if auth != nil {
			msg = auth.Message
		}
		return &RPCError{
			Code:      "auth",
			Message:   msg,
			Hint:      "Check your API key in Settings → Providers.",
			Retryable: false,
		}
	}

	// ErrRetryBudgetExhausted — check before ErrTransient so the unwrap
	// chain doesn't reach the inner transient first.
	var budget *corellm.ErrRetryBudgetExhausted
	if errors.As(err, &budget) {
		return &RPCError{
			Code:      "budget_exhausted",
			Message:   budget.Error(),
			Hint:      "The provider may be temporarily down. Re-send your message in a moment.",
			Retryable: false,
		}
	}

	// ErrTransient — recoverable; surface status + retryable.
	var transient *corellm.ErrTransient
	if errors.As(err, &transient) {
		return &RPCError{
			Code:      "transient",
			Message:   transient.Error(),
			Retryable: true,
		}
	}

	// ErrInvalidRequest — may be a context-overflow variant.
	var invalid *corellm.ErrInvalidRequest
	if errors.As(err, &invalid) {
		lc := strings.ToLower(invalid.Message)
		for _, kw := range overflowKeywords {
			if strings.Contains(lc, kw) {
				return &RPCError{
					Code:      "context_overflow",
					Message:   invalid.Error(),
					Hint:      "The conversation is too long. Start a new chat or use compaction to summarise earlier messages.",
					Retryable: false,
				}
			}
		}
		return &RPCError{
			Code:      "invalid_request",
			Message:   invalid.Error(),
			Retryable: false,
		}
	}

	// Catch-all.
	return &RPCError{
		Code:      "internal",
		Message:   err.Error(),
		Retryable: false,
	}
}
