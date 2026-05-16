package sentry

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	longStringThreshold = 200
	longStringHead      = 50
	longStringTail      = 20
)

// redactorPatterns is the set of regex patterns that match secret material.
// Patterns are matched in order; the first match wins per token.
// All patterns are compiled once at package init.
var redactorPatterns []*regexp.Regexp

// redactorReplacements maps each compiled pattern to its replacement string.
var redactorReplacements []string

func init() {
	type rule struct {
		pattern     string
		replacement string
	}
	rules := []rule{
		// @secret:<locator> syntactic shapes (even bogus locators)
		{`@secret:[A-Za-z0-9_/.:@-]+`, `[REDACTED:secret-ref]`},
		// Anthropic API keys (sk-ant-...)
		{`sk-ant-[A-Za-z0-9_-]{20,}`, `[REDACTED:anthropic-key]`},
		// OpenAI project keys (sk-proj-...)
		{`sk-proj-[A-Za-z0-9_-]{20,}`, `[REDACTED:openai-key]`},
		// Generic OpenAI keys (sk-... not already matched by sk-ant or sk-proj)
		{`sk-[A-Za-z0-9]{20,}`, `[REDACTED:openai-key]`},
		// Gemini / Google AI Studio API keys (F-004): 39-char keys starting
		// with "AIzaSy" followed by 33 alphanumeric + hyphen + underscore chars.
		{`AIzaSy[A-Za-z0-9_-]{33}`, `[REDACTED:apikey]`},
		// Azure OpenAI api-key header values (F-004): the "api-key:" context
		// followed by a 32-char lowercase hex string.
		{`(?i)api-key[:\s]+[0-9a-f]{32}`, `[REDACTED:apikey]`},
		// Bearer tokens: "Bearer <token>" form in headers/args.
		{`(?i)bearer\s+[A-Za-z0-9._~+/=-]{20,}`, `[REDACTED:bearer-token]`},
		// Bare JWTs: three base64url segments separated by dots (eyJ... header).
		// The eyJ prefix is the base64url encoding of '{"', highly characteristic.
		{`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`, `[REDACTED:jwt-token]`},
		// AWS access key IDs (AKIA..., ASIA..., AROA...)
		{`(?:AKIA|ASIA|AROA|AGPA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}`, `[REDACTED:aws-key-id]`},
		// AWS secret access keys (40-char base64url, often following "aws_secret" etc.)
		{`(?i)(?:aws_secret_access_key|aws_secret)[=:\s]+[A-Za-z0-9+/]{40}`, `[REDACTED:aws-secret-key]`},
		// Sentry DSN tokens (F-004): the 32-char lowercase hex public key embedded
		// in the DSN URL: https://<32-char-hex>@<host>/<project_id>.
		{`https://[0-9a-f]{32}@[A-Za-z0-9._-]+/[0-9]+`, `[REDACTED:sentry-dsn]`},
		// Email addresses
		{`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`, `[REDACTED:contact]`},
		// Phone numbers (E.164 and common US formats)
		{`(?:\+?1[\s.-]?)?\(?\d{3}\)?[\s.-]?\d{3}[\s.-]?\d{4}`, `[REDACTED:contact]`},
	}

	redactorPatterns = make([]*regexp.Regexp, len(rules))
	redactorReplacements = make([]string, len(rules))
	for i, r := range rules {
		redactorPatterns[i] = regexp.MustCompile(r.pattern)
		redactorReplacements[i] = r.replacement
	}
}

// homeDir is captured once at startup for path redaction.
var homeDir = func() string {
	h, _ := os.UserHomeDir()
	return h
}()

// RedactString applies all privacy rules to a single string:
//  1. Strips private. slog attribute prefix tokens.
//  2. Replaces home-dir path prefix with ~/...
//  3. Applies secret-pattern regexes.
//  4. Truncates strings longer than longStringThreshold chars (in the
//     binding-arg context). Callers that need truncation should pass the
//     string through TruncateLong separately.
func RedactString(s string) string {
	// Home-dir path replacement.
	if homeDir != "" && strings.Contains(s, homeDir) {
		s = strings.ReplaceAll(s, homeDir, "~")
	}

	// Apply secret patterns.
	for i, re := range redactorPatterns {
		s = re.ReplaceAllString(s, redactorReplacements[i])
	}
	return s
}

// TruncateLong truncates a binding-arg string that exceeds longStringThreshold
// characters. The first longStringHead chars and the last longStringTail chars
// are preserved; the middle is replaced with a marker.
func TruncateLong(s string) string {
	if len(s) <= longStringThreshold {
		return s
	}
	head := s[:longStringHead]
	tail := s[len(s)-longStringTail:]
	return fmt.Sprintf("%s... [LONG_STRING_REDACTED %d chars] ...%s", head, len(s), tail)
}

// RedactStringDeep applies redaction plus long-string truncation. Use this
// for binding-arg summary values.
func RedactStringDeep(s string) string {
	return TruncateLong(RedactString(s))
}

// ShouldDropSlogKey reports whether a slog attribute key should be silently
// dropped because it carries the private. prefix.
func ShouldDropSlogKey(key string) bool {
	return strings.HasPrefix(key, "private.")
}

// RedactMap applies RedactString to all string values in m, dropping keys
// that match ShouldDropSlogKey. Non-string values are passed through as-is.
// The original map is not modified; a new map is returned.
func RedactMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if ShouldDropSlogKey(k) {
			continue
		}
		switch val := v.(type) {
		case string:
			out[k] = RedactStringDeep(val)
		default:
			out[k] = v
		}
	}
	return out
}

// RedactStacktrace redacts all string frames in a raw stacktrace slice.
// The input is expected to be a slice of strings (file:line or function
// format). Home-dir paths are normalised; secret patterns are stripped.
func RedactStacktrace(frames []string) []string {
	if len(frames) == 0 {
		return frames
	}
	out := make([]string, len(frames))
	for i, f := range frames {
		out[i] = RedactString(f)
	}
	return out
}
