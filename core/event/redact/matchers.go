package redact

import (
	"regexp"
)

// Matcher names a built-in credential-pattern detector.
type Matcher struct {
	ID    string
	Re    *regexp.Regexp
	// SubmatchIndex is the regex group index whose match is the
	// substring to redact. 0 means "the entire match".
	SubmatchIndex int
}

// Built-in matchers. Patterns intentionally err on the side of
// over-matching: false positives leak benign strings to a deterministic
// placeholder, false negatives leak credentials. We accept the former.
var defaultMatchers = []Matcher{
	{
		ID: "aws-access-key-id",
		Re: regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`),
	},
	{
		ID: "aws-secret-key",
		// "aws_secret_access_key" style or 40-char base64-like
		// adjacent to "secret".
		Re: regexp.MustCompile(`(?i)(?:aws[_-]?secret[_-]?access[_-]?key|secret[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/+]{40})["']?`),
		SubmatchIndex: 1,
	},
	{
		// JWT: 3 base64url segments separated by dots.
		ID: "jwt",
		Re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	},
	{
		ID: "bearer-token",
		Re: regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9_\-.~+/=]{20,})`),
		SubmatchIndex: 1,
	},
	{
		ID: "basic-auth",
		Re: regexp.MustCompile(`(?i)\bBasic\s+([A-Za-z0-9+/=]{16,})`),
		SubmatchIndex: 1,
	},
	{
		ID: "pem-block",
		// Private-key PEM blocks (and certificates we don't want
		// landing in payloads either).
		Re: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
	},
	{
		ID: "generic-password-querystring",
		// password= or secret= or apikey= followed by non-empty token.
		Re: regexp.MustCompile(`(?i)(?:password|secret|apikey|api_key|api-key|token)\s*[:=]\s*["']?([^"'&\s]+)`),
		SubmatchIndex: 1,
	},
	{
		ID: "github-token",
		// GitHub PAT prefixes.
		Re: regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`),
	},
	{
		ID: "openai-key",
		Re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
	},
	{
		ID: "anthropic-key",
		Re: regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	},
}

// DefaultMatchers returns a copy of the built-in matcher catalog so
// callers can mix in operator policy without mutating the defaults.
func DefaultMatchers() []Matcher {
	out := make([]Matcher, len(defaultMatchers))
	copy(out, defaultMatchers)
	return out
}
