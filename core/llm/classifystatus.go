package llm

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ClassifyStatus maps an HTTP error response to the connector's typed
// taxonomy. It is the canonical top-level implementation, lifted from
// the three per-adapter copies that previously lived in
// core/llm/{openai,openrouter,anthropic} (WP01 of
// provider-implementation-uniformity-01KQ8V4F).
//
// Classification is by HTTP status code only — not by error body
// content — to guarantee retryability semantics are stable:
//
//   - 401 / 403 → *ErrAuth (non-retryable)
//   - 402 → *ErrPaymentRequired (non-retryable; insufficient credits —
//     retrying cannot conjure them, so this must never fall into the
//     ErrTransient bucket below)
//   - 408 / 425 / 429 → *ErrTransient (retryable)
//   - 5xx → *ErrTransient (retryable)
//   - everything else → *ErrInvalidRequest (non-retryable)
//
// The body is best-effort decoded for the error message (see
// ExtractProviderMessage); on failure a bounded excerpt is used. Either
// way the message is scrubbed (ScrubSecrets) before it is returned.
//
// Adapters whose error envelope differs from the OpenAI/Anthropic
// union shape should call this function and override the message
// field in the returned error if the generic extraction misses detail.
//
// secrets are the request's exact secret material (RequestSecrets); the
// message is always scrubbed of them and of generic credential shapes.
func ClassifyStatus(status int, body []byte, secrets ...string) error {
	return ClassifyStatusMessage(status, ScrubSecrets(extractClassifyMessage(body), secrets...))
}

// ClassifyStatusMessage is ClassifyStatus for callers that already hold
// the provider's message rather than a raw body — e.g. an OpenRouter
// mid-stream error frame, which carries the upstream HTTP-style status in
// `error.code` on an HTTP 200 stream (undelivered-message-retry,
// 2026-10-07: that frame used to be flattened to ErrTransient, so a 402
// that arrived in-stream was auto-retried as if it were a blip).
func ClassifyStatusMessage(status int, msg string) error {
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &ErrAuth{Status: status, Message: msg}
	case status == 402:
		return &ErrPaymentRequired{Status: status, Message: msg}
	case status == 408 || status == 425 || status == 429:
		return &ErrTransient{Status: status, Message: msg}
	case status >= 500 && status < 600:
		return &ErrTransient{Status: status, Message: msg}
	default:
		// 400 / 404 / 422 and other 4xx — non-retryable client errors.
		return &ErrInvalidRequest{Status: status, Message: msg}
	}
}

// extractClassifyMessage is ExtractProviderMessage; kept as the
// internal name ClassifyStatus has always used.
func extractClassifyMessage(body []byte) string { return ExtractProviderMessage(body) }

// providerExcerptMaxBytes bounds the free-text excerpt of an error body
// that matched no known envelope.
const providerExcerptMaxBytes = 512

// ExtractProviderMessage best-effort decodes a provider's error message
// from an HTTP error body. It understands every shape the OpenAI-
// compatible ecosystem is known to return, because downstream classifiers
// (context-overflow recovery, the fallback trigger, model-not-found)
// read this text:
//
//	{"error": {"message": "...", "type": "..."}}   OpenAI / Anthropic / vLLM
//	{"error": "..."}                               TGI / Ollama
//	{"message": "..."}                             older vLLM / OpenRouter
//	{"detail": "..."}                              FastAPI
//	{"detail": [{"msg": "..."}, ...]}              FastAPI validation
//
// Anything else becomes a bounded excerpt (see BodyExcerpt). The result
// is NOT scrubbed — use ProviderErrorMessage for text that will reach an
// error, a log line or an event.
func ExtractProviderMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if len(env.Error) > 0 {
			var flat string
			if json.Unmarshal(env.Error, &flat) == nil && flat != "" {
				return flat
			}
			var nested struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			}
			if json.Unmarshal(env.Error, &nested) == nil && nested.Message != "" {
				if nested.Type != "" {
					return nested.Type + ": " + nested.Message
				}
				return nested.Message
			}
		}
		if env.Message != "" {
			return env.Message
		}
		if len(env.Detail) > 0 {
			var flat string
			if json.Unmarshal(env.Detail, &flat) == nil && flat != "" {
				return flat
			}
			var list []struct {
				Msg string `json:"msg"`
			}
			if json.Unmarshal(env.Detail, &list) == nil {
				var msgs []string
				for _, d := range list {
					if d.Msg != "" {
						msgs = append(msgs, d.Msg)
					}
				}
				if len(msgs) > 0 {
					return strings.Join(msgs, "; ")
				}
			}
		}
	}
	return BodyExcerpt(body)
}

var (
	htmlScriptStyleRe = regexp.MustCompile(`(?is)<(script|style)\b.*?</(script|style)\s*>`)
	htmlTagRe         = regexp.MustCompile(`(?s)<[^>]*>`)
)

// BodyExcerpt renders an error body that matched no known envelope as a
// bounded diagnostic: HTML is reduced to its text (script/style dropped,
// tags stripped), whitespace is collapsed, and the result is capped at
// 512 bytes on a rune boundary. Like ExtractProviderMessage it is not
// scrubbed.
func BodyExcerpt(body []byte) string {
	s := string(body)
	if strings.Contains(s, "<") && strings.Contains(s, ">") && htmlTagRe.MatchString(s) {
		s = htmlScriptStyleRe.ReplaceAllString(s, " ")
		s = htmlTagRe.ReplaceAllString(s, " ")
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > providerExcerptMaxBytes {
		cut := providerExcerptMaxBytes
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

// ProviderErrorMessage is ExtractProviderMessage scrubbed for any sink:
// the exact request secrets (RequestSecrets) and every generic credential
// shape are redacted. Every adapter's HTTP-error path goes through this
// (directly or via ClassifyStatus), because the message reaches
// emitter.Error → persisted events.
func ProviderErrorMessage(body []byte, secrets ...string) string {
	return ScrubSecrets(ExtractProviderMessage(body), secrets...)
}
