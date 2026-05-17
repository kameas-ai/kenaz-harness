package fleet

import (
	"regexp"
	"strings"
)

// TelemetryRedactor scrubs sensitive field values from outgoing telemetry
// payloads before they are shipped to the fleet endpoint.
//
// The redactor operates on two axes:
//  1. Field names — attribute keys whose names signal sensitivity (e.g. "password",
//     "token"). Values for these keys are replaced with "[REDACTED]" regardless of
//     their content.
//  2. Value patterns — regex patterns that match known secret formats anywhere in
//     a string value. Matching substrings are replaced with "[REDACTED:<label>]".
//
// The pattern set deliberately overlaps with core/sentry.RedactString so that the
// two redactors stay consistent. Any additions made here should also be evaluated
// for the sentry package and vice versa.
//
// Thread-safe: all state is read-only after construction.
type TelemetryRedactor struct {
	sensitiveKeys  map[string]bool
	keyPrefixes    []string
	valuePatterns  []*regexp.Regexp
	valueReplacer  []string
}

// NewTelemetryRedactor constructs a TelemetryRedactor with the default deny list.
func NewTelemetryRedactor() *TelemetryRedactor {
	r := &TelemetryRedactor{
		sensitiveKeys: buildSensitiveKeySet(),
		keyPrefixes:   sensitiveKeyPrefixes,
	}

	type rule struct {
		pattern     string
		replacement string
	}
	rules := []rule{
		// @secret:<locator> syntactic shapes
		{`@secret:[A-Za-z0-9_/.:@-]+`, `[REDACTED:secret-ref]`},
		// Anthropic API keys (sk-ant-...)
		{`sk-ant-[A-Za-z0-9_-]{20,}`, `[REDACTED:anthropic-key]`},
		// OpenAI project keys (sk-proj-...)
		{`sk-proj-[A-Za-z0-9_-]{20,}`, `[REDACTED:openai-key]`},
		// Generic sk- keys (not caught by the two above)
		{`sk-[A-Za-z0-9]{20,}`, `[REDACTED:openai-key]`},
		// Gemini / Google AI Studio (AIzaSy...)
		{`AIzaSy[A-Za-z0-9_-]{33}`, `[REDACTED:apikey]`},
		// Azure OpenAI api-key header pattern
		{`(?i)api-key[:\s]+[0-9a-f]{32}`, `[REDACTED:apikey]`},
		// Bearer tokens
		{`(?i)bearer\s+[A-Za-z0-9._~+/=-]{20,}`, `[REDACTED:bearer-token]`},
		// Bare JWTs (eyJ... header)
		{`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`, `[REDACTED:jwt-token]`},
		// AWS access key IDs
		{`(?:AKIA|ASIA|AROA|AGPA|AIPA|ANPA|ANVA|APKA)[A-Z0-9]{16}`, `[REDACTED:aws-key-id]`},
		// AWS secret access keys
		{`(?i)(?:aws_secret_access_key|aws_secret)[=:\s]+[A-Za-z0-9+/]{40}`, `[REDACTED:aws-secret-key]`},
		// OAuth2 client_secret in key=value pairs
		{`(?i)client_secret[=:\s]+[A-Za-z0-9_.-]{16,}`, `[REDACTED:oauth-secret]`},
		// Generic password= key=value pairs
		{`(?i)password[=:\s]+\S{8,}`, `[REDACTED:password]`},
		// Sentry DSN tokens
		{`https://[0-9a-f]{32}@[A-Za-z0-9._-]+/[0-9]+`, `[REDACTED:sentry-dsn]`},
	}

	r.valuePatterns = make([]*regexp.Regexp, len(rules))
	r.valueReplacer = make([]string, len(rules))
	for i, rule := range rules {
		r.valuePatterns[i] = regexp.MustCompile(rule.pattern)
		r.valueReplacer[i] = rule.replacement
	}
	return r
}

// sensitiveKeyNames is the canonical set of attribute key names whose values
// must always be redacted regardless of value content.
var sensitiveKeyNames = []string{
	"password", "passwd", "secret", "token", "api_key", "apikey",
	"access_key", "secret_key", "private_key", "auth", "authorization",
	"credential", "credentials", "passphrase", "pin",
}

// sensitiveKeyPrefixes are name prefixes that trigger redaction when the key
// starts with any of them.
var sensitiveKeyPrefixes = []string{
	"private.", "secret.", "auth.", "credential.",
}

func buildSensitiveKeySet() map[string]bool {
	m := make(map[string]bool, len(sensitiveKeyNames))
	for _, k := range sensitiveKeyNames {
		m[strings.ToLower(k)] = true
	}
	return m
}

// IsSensitiveKey reports whether the given attribute key name is on the
// deny list. Case-insensitive.
func (r *TelemetryRedactor) IsSensitiveKey(key string) bool {
	if r.sensitiveKeys[strings.ToLower(key)] {
		return true
	}
	lower := strings.ToLower(key)
	for _, prefix := range r.keyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// RedactString applies all value-pattern rules to s, replacing matching
// substrings with the corresponding [REDACTED:...] label. Returns the
// cleaned string.
func (r *TelemetryRedactor) RedactString(s string) string {
	for i, re := range r.valuePatterns {
		s = re.ReplaceAllString(s, r.valueReplacer[i])
	}
	return s
}

// RedactAttributes takes a map of span/metric/log attribute key-value pairs
// and returns a new map with sensitive fields replaced. String values are
// passed through RedactString; non-string values for sensitive keys become
// "[REDACTED]". The original map is not modified.
func (r *TelemetryRedactor) RedactAttributes(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return attrs
	}
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if r.IsSensitiveKey(k) {
			out[k] = "[REDACTED]"
			continue
		}
		switch val := v.(type) {
		case string:
			out[k] = r.RedactString(val)
		default:
			out[k] = v
		}
	}
	return out
}

// RedactSpanName applies value-pattern rules to a span name. Span names should
// rarely contain secrets but are run through the redactor as a belt-and-
// suspenders measure.
func (r *TelemetryRedactor) RedactSpanName(name string) string {
	return r.RedactString(name)
}

// RedactLogBody applies redaction to a log body string.
func (r *TelemetryRedactor) RedactLogBody(body string) string {
	return r.RedactString(body)
}

// DefaultRedactor is a package-level singleton that can be used without
// explicit construction. Callers that need a custom deny list should use
// NewTelemetryRedactor instead.
var DefaultRedactor = NewTelemetryRedactor()
