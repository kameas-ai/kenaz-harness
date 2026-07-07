package contextbootstrap

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// quarantine applies prompt-injection safety transforms to raw source content
// before it is included in any model prompt (FR-010 / WP09).
//
// Design: treat ALL source content as DATA, never as instructions. The
// quarantine function:
//
//  1. Extracts text from the raw MCP JSON (best-effort; falls back to
//     JSON-encoded string).
//  2. Strips secrets matching common credential patterns (API keys, tokens).
//  3. Wraps the text in a data-framing envelope that instructs the model to
//     treat the enclosed block as inert data.
//
// The envelope approach (framing with explicit DATA markers) is the primary
// injection-safety mechanism; secret stripping is defence-in-depth.
//
// The result is a string safe to embed in an extraction prompt. The caller
// (ConnectorRun.runExtractionPrompt) places this inside the
// ExtractionPrompt template — the model sees the content only within the
// explicit DATA block markers.
func quarantine(env sourceContentEnvelope) (quarantinedContent, error) {
	// Step 1: extract text from raw JSON.
	text, err := extractText(env.rawContent)
	if err != nil {
		return quarantinedContent{}, fmt.Errorf("quarantine: extract text: %w", err)
	}

	// Step 2: strip secrets.
	stripped := stripSecrets(text)

	// Step 3: wrap in data-framing envelope.
	framed := frameAsData(stripped, env.sourceRef, env.senderIdentifier)

	return quarantinedContent{
		framed:           framed,
		sourceRef:        env.sourceRef,
		senderIdentifier: env.senderIdentifier,
	}, nil
}

// quarantinedContent is the result of quarantine(). It is the only type
// the extraction engine is allowed to pass to model calls.
// The framed field is safe for inclusion in prompts.
type quarantinedContent struct {
	// framed is the prompt-injection-safe, secret-stripped, data-framed text.
	framed string
	// sourceRef and senderIdentifier are preserved for provenance labelling
	// but are NEVER included in the framed prompt text verbatim.
	sourceRef        string
	senderIdentifier string
}

// ─── secret stripping ─────────────────────────────────────────────────────────

// secretPatterns are the credential patterns stripped from source content.
// They match common API key formats by shape, not by value.
var secretPatterns = []*regexp.Regexp{
	// Anthropic keys: sk-ant-api03-...
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`),
	// OpenAI-style keys: sk-...
	regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
	// Bearer tokens in headers
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`),
	// Generic "token": "..." patterns in JSON
	regexp.MustCompile(`(?i)"[a-z_]*token[a-z_]*"\s*:\s*"[^"]{8,}"`),
	// Password fields
	regexp.MustCompile(`(?i)"password"\s*:\s*"[^"]{4,}"`),
	// AWS-style access keys
	regexp.MustCompile(`(?i)(AKIA|ASIA|AROA)[A-Z0-9]{16}`),
}

const redactedPlaceholder = "[REDACTED]"

// stripSecrets replaces credential-shaped strings with [REDACTED].
func stripSecrets(s string) string {
	for _, pat := range secretPatterns {
		s = pat.ReplaceAllString(s, redactedPlaceholder)
	}
	return s
}

// ─── data framing ─────────────────────────────────────────────────────────────

// dataFrameOpen and dataFrameClose are the sentinel strings that frame source
// content as DATA in the extraction prompt. The model is instructed above the
// DATA block to treat everything inside as inert data, never as instructions.
//
// These delimiters use unusual Unicode characters (┤ ├) to reduce the chance
// that an adversarial source string can escape the framing by predicting the
// delimiter. Additional robustness comes from the extraction prompt itself,
// which instructs the model explicitly.
const (
	dataFrameOpen  = "┤DATA:BEGIN┼"
	dataFrameClose = "┤DATA:END┼"
)

// frameAsData wraps text in a DATA block with a source-attribution header.
// The attribution header uses only the sourceRef and a sanitized
// senderIdentifier — never the raw text itself — so the model can build
// provenance without treating the identifier as trusted instruction.
func frameAsData(text, sourceRef, senderIdentifier string) string {
	// Sanitize the attribution fields: strip control characters and limit length.
	safeRef := sanitizeAttribution(sourceRef, 200)
	safeSender := sanitizeAttribution(senderIdentifier, 100)

	var b strings.Builder
	b.WriteString(dataFrameOpen)
	if safeRef != "" {
		b.WriteString("\nsource_ref:")
		b.WriteString(safeRef)
	}
	if safeSender != "" {
		b.WriteString("\nsender:")
		b.WriteString(safeSender)
	}
	b.WriteString("\ncontent:\n")
	b.WriteString(text)
	b.WriteString("\n")
	b.WriteString(dataFrameClose)
	return b.String()
}

// sanitizeAttribution strips control characters and limits the length of an
// attribution field. Attribution fields are included in the prompt but must
// not themselves become injection vectors.
func sanitizeAttribution(s string, maxLen int) string {
	// Strip control characters (keep printable ASCII + common Unicode letters).
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r != 0x7F {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if len(result) > maxLen {
		result = result[:maxLen] + "…"
	}
	return result
}

// ─── text extraction ──────────────────────────────────────────────────────────

// extractText extracts a plain-text representation from an MCP tool response.
// MCP responses are JSON; the actual content might be nested. We use a
// best-effort heuristic:
//
//  1. If the top-level JSON is a string, use it directly.
//  2. If it is an object with a "text", "body", "content", or "value" string
//     field, use that field.
//  3. If it is an array, concatenate text from each element.
//  4. Otherwise, pretty-print the JSON (safe fallback).
func extractText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}

	// Try string directly.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	// Try object with known text fields.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, key := range []string{"text", "body", "content", "value", "message", "snippet"} {
			if v, ok := obj[key]; ok {
				var ts string
				if json.Unmarshal(v, &ts) == nil {
					return ts, nil
				}
			}
		}
		// Recurse on "items" or "messages" arrays.
		for _, key := range []string{"items", "messages", "emails", "results"} {
			if v, ok := obj[key]; ok {
				return extractText(v)
			}
		}
		// Fall through to pretty-print.
	}

	// Try array: concatenate text from each element.
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err == nil {
		var parts []string
		for _, elem := range arr {
			t, err := extractText(elem)
			if err == nil && t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n---\n"), nil
	}

	// Fallback: pretty-print.
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return string(raw), nil
	}
	return string(pretty), nil
}
