package llm

import (
	"encoding/json"
	"net/http"
	"strings"
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
//   - 408 / 425 / 429 → *ErrTransient (retryable)
//   - 5xx → *ErrTransient (retryable)
//   - everything else → *ErrInvalidRequest (non-retryable)
//
// The body is best-effort decoded for the error message; on failure
// a truncated snippet is included verbatim.
//
// Adapters whose error envelope differs from the OpenAI/Anthropic
// union shape should call this function and override the message
// field in the returned error if the generic extraction misses detail.
func ClassifyStatus(status int, body []byte) error {
	msg := extractClassifyMessage(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &ErrAuth{Status: status, Message: msg}
	case status == 408 || status == 425 || status == 429:
		return &ErrTransient{Status: status, Message: msg}
	case status >= 500 && status < 600:
		return &ErrTransient{Status: status, Message: msg}
	default:
		// 400 / 404 / 422 and other 4xx — non-retryable client errors.
		return &ErrInvalidRequest{Status: status, Message: msg}
	}
}

// extractClassifyMessage best-effort decodes an error message from a
// JSON body. Handles both the OpenAI envelope:
//
//	{"error": {"message": "...", "type": "...", "code": "..."}}
//
// and the Anthropic envelope:
//
//	{"type": "error", "error": {"type": "...", "message": "..."}}
//
// and a bare top-level "message" field (some OpenRouter responses).
// Falls back to a truncated body snippet on parse failure.
func extractClassifyMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		// Bare top-level message (some OpenRouter shapes).
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Message != "" {
			if env.Error.Type != "" {
				return env.Error.Type + ": " + env.Error.Message
			}
			return env.Error.Message
		}
		if env.Message != "" {
			return env.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
