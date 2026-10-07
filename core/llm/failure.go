package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

// FailureClass is the coarse, user-facing taxonomy of why a model request
// failed (undelivered-message-retry, dogfood 2026-10-07). It answers the
// one question the chat surface needs to decide what to do next:
//
//   - FailureUserActionable — retrying unchanged cannot succeed; the user
//     has to do something first (add credits, fix the key, pick another
//     model). The surface must NEVER auto-retry these.
//   - FailureTransient — the provider or the network had a bad moment
//     (429, 5xx, overloaded, timeouts). Retrying later can succeed, so the
//     surface may auto-retry with backoff.
//   - FailureUnknown — the error carries no typed signal either way.
//
// The class is derived from the typed error taxonomy in errors.go (which
// every adapter already produces from the HTTP status — see
// ClassifyStatus), never from the error string.
type FailureClass string

const (
	FailureUserActionable FailureClass = "user_actionable"
	FailureTransient      FailureClass = "transient"
	FailureUnknown        FailureClass = "unknown"
)

// Failure codes — a finer, stable discriminator under FailureClass. The
// frontend keys copy and affordances (e.g. "Open settings" for a key
// problem) off these, never off Message.
const (
	FailureCodePaymentRequired  = "payment_required"
	FailureCodeAuthInvalid      = "auth_invalid"
	FailureCodeForbidden        = "forbidden"
	FailureCodeModelNotFound    = "model_not_found"
	FailureCodeInvalidRequest   = "invalid_request"
	FailureCodeUnsupported      = "unsupported"
	FailureCodeRateLimited      = "rate_limited"
	FailureCodeProviderDown     = "provider_unavailable"
	FailureCodeNetwork          = "network"
	FailureCodeTimeout          = "timeout"
	FailureCodeUnknown          = "unknown"
	failureMessageMaxRunes      = 300
	failureProviderUnknownLabel = "the provider"
)

// RunFailure is the classified description of a failed model request.
// Every field is safe to put on the wire and in the database: Message is
// sanitized by SanitizeProviderMessage (credential shapes redacted,
// length-capped) before it lands here.
type RunFailure struct {
	Class FailureClass
	Code  string
	// Status is the provider's HTTP status when the typed error carried
	// one; 0 for network failures and pre-flight rejections.
	Status int
	// Provider is the adapter kind ("openrouter", "anthropic", …) the
	// request was sent to. Empty when unknown.
	Provider string
	// Message is the provider's own explanation, sanitized. May be empty.
	Message string
	// Summary is the one-line human copy the chat surface shows on the
	// NOT DELIVERED badge, e.g. "Out of credits with OpenRouter".
	Summary string
}

// ClassifyFailure maps a model-request error onto a RunFailure.
// providerKind is the adapter kind of the profile the request targeted;
// it is used for the copy when the error itself does not name a
// provider (only the registry's ErrProvider* decorations do).
//
// Classification walks the typed taxonomy with errors.As, so it sees
// through every wrapping layer (kernel node chain, retry-budget wrapper,
// registry decoration). An error with no typed member classifies as
// FailureUnknown — never guessed from the string.
func ClassifyFailure(err error, providerKind string) RunFailure {
	f := RunFailure{Class: FailureUnknown, Code: FailureCodeUnknown, Provider: providerKind}
	if err == nil {
		return f
	}

	var (
		payDecorated  *ErrProviderPaymentRequired
		authDecorated *ErrProviderAuthFailed
		pay           *ErrPaymentRequired
		auth          *ErrAuth
		invalid       *ErrInvalidRequest
		transient     *ErrTransient
		capUnsup      *ErrCapabilityUnsupported
		featUnsup     *ErrUnsupportedFeature
		modality      *UnsupportedModalityError
		credRes       *ErrCredentialResolution
		netErr        net.Error
	)
	if errors.As(err, &payDecorated) && payDecorated.Provider != "" {
		f.Provider = payDecorated.Provider
	}
	if errors.As(err, &authDecorated) && authDecorated.Provider != "" {
		f.Provider = authDecorated.Provider
	}

	switch {
	case errors.As(err, &pay):
		f.Class, f.Code, f.Status = FailureUserActionable, FailureCodePaymentRequired, pay.Status
		f.Message = pay.Message
	case errors.As(err, &auth):
		f.Class, f.Status = FailureUserActionable, auth.Status
		f.Code = FailureCodeAuthInvalid
		if auth.Status == 403 {
			f.Code = FailureCodeForbidden
		}
		f.Message = auth.Message
	case errors.As(err, &credRes):
		// The key could not even be read from the keychain — the request
		// never left the machine. Same remedy as a rejected key.
		f.Class, f.Code = FailureUserActionable, FailureCodeAuthInvalid
		f.Message = credRes.Error()
	case errors.As(err, &invalid):
		f.Class, f.Status = FailureUserActionable, invalid.Status
		f.Code = FailureCodeInvalidRequest
		if invalid.Status == 404 || looksLikeModelNotFound(invalid.Message) {
			f.Code = FailureCodeModelNotFound
		}
		f.Message = invalid.Message
	case errors.As(err, &capUnsup), errors.As(err, &featUnsup), errors.As(err, &modality):
		f.Class, f.Code = FailureUserActionable, FailureCodeUnsupported
		f.Message = FriendlyOr(err, err.Error())
	case errors.As(err, &transient):
		f.Class, f.Status = FailureTransient, transient.Status
		switch {
		case transient.Status == 429:
			f.Code = FailureCodeRateLimited
		case transient.Status == 408:
			f.Code = FailureCodeTimeout
		case transient.Status >= 500:
			f.Code = FailureCodeProviderDown
		case transient.Status == 0:
			f.Code = FailureCodeNetwork
		default:
			f.Code = FailureCodeProviderDown
		}
		f.Message = transient.Message
	case errors.Is(err, context.DeadlineExceeded):
		f.Class, f.Code = FailureTransient, FailureCodeTimeout
	case errors.As(err, &netErr):
		f.Class, f.Code = FailureTransient, FailureCodeNetwork
		if netErr.Timeout() {
			f.Code = FailureCodeTimeout
		}
		f.Message = netErr.Error()
	}

	f.Message = SanitizeProviderMessage(f.Message)
	f.Summary = failureSummary(f)
	return f
}

// looksLikeModelNotFound recognises the "no such model" 400s some
// OpenAI-compatible gateways return instead of a 404. Matching the
// provider's message is acceptable here because it only refines the Code
// within an already-typed FailureUserActionable — it never moves an
// error between classes.
func looksLikeModelNotFound(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "model") &&
		(strings.Contains(m, "not found") || strings.Contains(m, "does not exist") ||
			strings.Contains(m, "not a valid model") || strings.Contains(m, "no endpoints found"))
}

// ProviderDisplayName renders an adapter kind as the product name a user
// recognises. Unknown kinds come back unchanged.
func ProviderDisplayName(kind string) string {
	switch strings.ToLower(kind) {
	case "openrouter":
		return "OpenRouter"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "azure-openai":
		return "Azure OpenAI"
	case "bedrock":
		return "AWS Bedrock"
	case "gemini":
		return "Google Gemini"
	case "ollama":
		return "Ollama"
	case "custom-openai":
		return "your custom endpoint"
	case "":
		return failureProviderUnknownLabel
	default:
		return kind
	}
}

// failureSummary is the badge copy. It names the provider so a user with
// several configured accounts knows which one to fix.
func failureSummary(f RunFailure) string {
	who := ProviderDisplayName(f.Provider)
	switch f.Code {
	case FailureCodePaymentRequired:
		return fmt.Sprintf("Out of credits with %s", who)
	case FailureCodeAuthInvalid:
		return fmt.Sprintf("%s rejected the API key", who)
	case FailureCodeForbidden:
		return fmt.Sprintf("%s refused access to this model", who)
	case FailureCodeModelNotFound:
		return fmt.Sprintf("%s does not recognise this model", who)
	case FailureCodeInvalidRequest:
		return fmt.Sprintf("%s rejected the request", who)
	case FailureCodeUnsupported:
		return "This model cannot accept this request"
	case FailureCodeRateLimited:
		return fmt.Sprintf("%s is rate-limiting requests", who)
	case FailureCodeProviderDown:
		return fmt.Sprintf("%s is unavailable right now", who)
	case FailureCodeNetwork:
		return fmt.Sprintf("Could not reach %s", who)
	case FailureCodeTimeout:
		return fmt.Sprintf("%s timed out", who)
	default:
		return "The model request failed"
	}
}

// credentialShapes are the secret shapes a provider error body could
// plausibly echo back (some gateways quote the offending key or the
// Authorization header verbatim in a 401 body). Kept local to core/llm —
// the sentry redactor's list misses OpenRouter's "sk-or-v1-…" shape and
// core/llm must not depend on the crash reporter.
var credentialShapes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization\s*[:=]\s*\S+(\s+\S+)?`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`(?i)(x-api-key|api-key|api_key|apikey)\s*[:=]\s*\S+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`AIza[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`),
}

// SanitizeProviderMessage makes a provider-supplied error message safe to
// show in the UI and persist: every credential shape is replaced with
// "[redacted]", whitespace is collapsed, and the result is capped at 300
// runes. A provider message is untrusted text — it reaches a chat bubble
// and a database row, so nothing that looks like a secret may survive.
func SanitizeProviderMessage(msg string) string {
	if msg == "" {
		return ""
	}
	for _, re := range credentialShapes {
		msg = re.ReplaceAllString(msg, "[redacted]")
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if r := []rune(msg); len(r) > failureMessageMaxRunes {
		msg = string(r[:failureMessageMaxRunes]) + "…"
	}
	return msg
}
