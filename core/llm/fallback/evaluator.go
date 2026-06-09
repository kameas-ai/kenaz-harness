package fallback

import (
	"errors"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// Match reports whether err matches any trigger in the provided slice,
// and returns the first matching TriggerCondition.
//
// Resolution order:
//  1. TriggerErrorAny — always matches any non-nil error.
//  2. TriggerError5xx — matches ErrTransient with Status 5xx OR 429 (per
//     the HTTP spec, 429 is often classified as 5xx by adapters that do
//     not distinguish rate-limiting from unavailability — see TriggerError429).
//     Also matches status 529 (Anthropic's "Overloaded" extension).
//  3. TriggerError429 — matches ErrTransient with Status == 429.
//  4. TriggerErrorAuthFailed — matches ErrAuth or ErrProviderAuthFailed.
//  5. TriggerErrorContextOverflow — matches ErrInvalidRequest (400) with
//     context/token/length keywords in the message.
//  6. TriggerErrorSafetyBlock — matches ErrInvalidRequest (400) with
//     safety/content_policy/blocked keywords in the message.
//
// The second return value is zero if no trigger matched.
func Match(err error, triggers []TriggerCondition) (bool, TriggerCondition) {
	if err == nil || len(triggers) == 0 {
		return false, ""
	}

	for _, t := range triggers {
		if matchOne(err, t) {
			return true, t
		}
	}
	return false, ""
}

func matchOne(err error, t TriggerCondition) bool {
	switch t {
	case TriggerErrorAny:
		return err != nil

	case TriggerError5xx:
		var transient *llm.ErrTransient
		if errors.As(err, &transient) {
			s := transient.Status
			// 429 is included here because many operators treat it as
			// "5xx-class overload"; dedicated TriggerError429 is finer.
			return (s >= 500 && s < 600) || s == 429 || s == 529
		}
		return false

	case TriggerError429:
		var transient *llm.ErrTransient
		if errors.As(err, &transient) {
			return transient.Status == 429
		}
		return false

	case TriggerErrorAuthFailed:
		var auth *llm.ErrAuth
		if errors.As(err, &auth) {
			return true
		}
		var provAuth *llm.ErrProviderAuthFailed
		return errors.As(err, &provAuth)

	case TriggerErrorContextOverflow:
		var inv *llm.ErrInvalidRequest
		if errors.As(err, &inv) && inv.Status == 400 {
			msg := strings.ToLower(inv.Message)
			return strings.Contains(msg, "context") ||
				strings.Contains(msg, "token") ||
				strings.Contains(msg, "length") ||
				strings.Contains(msg, "too long") ||
				strings.Contains(msg, "maximum context")
		}
		return false

	case TriggerErrorSafetyBlock:
		var inv *llm.ErrInvalidRequest
		if errors.As(err, &inv) && inv.Status == 400 {
			msg := strings.ToLower(inv.Message)
			return strings.Contains(msg, "safety") ||
				strings.Contains(msg, "content_policy") ||
				strings.Contains(msg, "content policy") ||
				strings.Contains(msg, "blocked") ||
				strings.Contains(msg, "harm")
		}
		return false
	}
	return false
}
