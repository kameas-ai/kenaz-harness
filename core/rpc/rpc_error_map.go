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

// friendly is implemented by core/llm error types that render a
// model-agnostic, user-actionable message via Friendly() (e.g. ErrAuth,
// ErrInvalidRequest, and the pre-existing attachment-error family). It was
// previously defined but never consulted at any RPC boundary — see the
// review finding on tool-error-legibility-01PMDL02 WP02/WP03: every
// Friendly() implementation was dead code because MapLLMError read
// .Message/.Error() directly.
type friendly interface{ Friendly() string }

// friendlyOr returns the Friendly() text of the first error in err's chain
// that implements the friendly interface, provided that text is non-empty.
// Otherwise it returns fallback unchanged, preserving today's behavior for
// error types with no Friendly() method (and defending against a
// pathological Friendly() that returns "").
func friendlyOr(err error, fallback string) string {
	var f friendly
	if errors.As(err, &f) {
		if s := f.Friendly(); s != "" {
			return s
		}
	}
	return fallback
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
		msg = friendlyOr(err, msg)
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
			Message:   friendlyOr(err, budget.Error()),
			Hint:      "The provider may be temporarily down. Re-send your message in a moment.",
			Retryable: false,
		}
	}

	// ErrTransient — recoverable; surface status + retryable.
	var transient *corellm.ErrTransient
	if errors.As(err, &transient) {
		return &RPCError{
			Code:      "transient",
			Message:   friendlyOr(err, transient.Error()),
			Retryable: true,
		}
	}

	// ErrInvalidRequest — may be a context-overflow variant.
	var invalid *corellm.ErrInvalidRequest
	if errors.As(err, &invalid) {
		// Keyword detection stays on the raw provider message — Friendly()
		// text is a fixed template that doesn't carry the provider's
		// original wording, so it must not be substituted before the
		// overflow classification runs.
		lc := strings.ToLower(invalid.Message)
		for _, kw := range overflowKeywords {
			if strings.Contains(lc, kw) {
				// Deliberately NOT friendlyOr here: ErrInvalidRequest's
				// Friendly() text ("malformed parameters... check the
				// request shape") is generic 4xx copy that contradicts the
				// context_overflow Hint below. Surfacing both together
				// would tell the user two different, inconsistent things
				// about the same failure — so this path keeps the raw
				// invalid.Error(), which actually contains the provider's
				// "exceeds maximum context" wording the Hint refers to.
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
			Message:   friendlyOr(err, invalid.Error()),
			Retryable: false,
		}
	}

	// Catch-all. This is also where the pre-existing attachment-error
	// family (ErrAttachmentTooLarge, ErrAttachmentMimeUnsupported, …) is
	// mapped, since none of them are ErrAuth/ErrTransient/ErrInvalidRequest
	// — friendlyOr is what lights up their Friendly() renderers.
	return &RPCError{
		Code:      "internal",
		Message:   friendlyOr(err, err.Error()),
		Retryable: false,
	}
}
