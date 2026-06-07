package gemini

import (
	"encoding/json"
	"net/http"
	"strings"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// geminiErrorEnvelope is the JSON error body returned by the Gemini API.
type geminiErrorEnvelope struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// classifyStatus maps a Gemini HTTP error response to the connector taxonomy.
//
// Google API error classification by HTTP status:
//   - 401 / 403 → ErrAuth (UNAUTHENTICATED / PERMISSION_DENIED)
//   - 429 → ErrTransient (RESOURCE_EXHAUSTED)
//   - 408 / 425 → ErrTransient (timeout / too early)
//   - 5xx → ErrTransient (server errors)
//   - other 4xx → ErrInvalidRequest
func classifyStatus(status int, body []byte) error {
	msg := extractGeminiError(body)
	if msg == "" {
		msg = http.StatusText(status)
	}
	switch {
	case status == 401 || status == 403:
		return &llm.ErrAuth{Status: status, Message: "gemini: " + msg}
	case status == 429:
		return &llm.ErrTransient{Status: status, Message: "gemini: " + msg}
	case status >= 500 && status < 600:
		return &llm.ErrTransient{Status: status, Message: "gemini: " + msg}
	case status == 408 || status == 425:
		return &llm.ErrTransient{Status: status, Message: "gemini: " + msg}
	default:
		return &llm.ErrInvalidRequest{Status: status, Message: "gemini: " + msg}
	}
}

// extractGeminiError best-effort parses a Gemini API error envelope.
func extractGeminiError(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var env geminiErrorEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Error.Message != "" {
			if env.Error.Status != "" {
				return env.Error.Status + ": " + env.Error.Message
			}
			return env.Error.Message
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
