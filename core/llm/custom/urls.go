package custom

import (
	"fmt"
	"strings"
)

// ChatEndpoint resolves the full chat completions URL from a base URL.
// It handles the three documented forms:
//
//   - base ends with /v1            → append /chat/completions
//   - base ends with /v1/           → trim trailing slash, append /chat/completions
//   - base already ends with /chat/completions → use as-is
//
// Any other base URL has /chat/completions appended directly.
func ChatEndpoint(baseURL string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("custom-openai: base URL is empty")
	}
	b := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(b, "/chat/completions") {
		return b, nil
	}
	return b + "/chat/completions", nil
}

// ModelsEndpoint resolves the full /models URL from a base URL, following
// the same normalization rules as ChatEndpoint.
func ModelsEndpoint(baseURL string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("custom-openai: base URL is empty")
	}
	b := strings.TrimRight(baseURL, "/")
	// Strip /chat/completions if present, then append /models.
	if strings.HasSuffix(b, "/chat/completions") {
		b = strings.TrimSuffix(b, "/chat/completions")
	}
	return b + "/models", nil
}
