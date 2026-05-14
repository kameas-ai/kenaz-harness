package export

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// RedactionMatch records a single substitution made by RedactValue.
type RedactionMatch struct {
	// ProfileID is the matcher name that fired (e.g. "anthropic-key",
	// "openai-key", "bearer-token").
	ProfileID string
	// Offset is the byte offset of the substitution in the original string.
	Offset int
}

// credMatcher couples a human-readable profile id to a compiled regex.
// For matchers with a capture group (SubmatchIndex > 0) only the captured
// substring is replaced; for whole-match matchers (SubmatchIndex == 0)
// the entire match is replaced.
type credMatcher struct {
	profileID     string
	re            *regexp.Regexp
	submatchIndex int
}

// builtinMatchers is the canonical set of credential patterns RedactValue
// applies. Mirrors core/event/redact.defaultMatchers so the export
// package is a standalone entry point without a circular import.
var builtinMatchers = []credMatcher{
	{
		profileID: "aws-access-key-id",
		re:        regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`),
	},
	{
		profileID:     "aws-secret-key",
		re:            regexp.MustCompile(`(?i)(?:aws[_-]?secret[_-]?access[_-]?key|secret[_-]?key)\s*[:=]\s*["']?([A-Za-z0-9/+]{40})["']?`),
		submatchIndex: 1,
	},
	{
		profileID: "jwt",
		re:        regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	},
	{
		profileID:     "bearer-token",
		re:            regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9_\-.~+/=]{20,})`),
		submatchIndex: 1,
	},
	{
		profileID:     "basic-auth",
		re:            regexp.MustCompile(`(?i)\bBasic\s+([A-Za-z0-9+/=]{16,})`),
		submatchIndex: 1,
	},
	{
		profileID: "pem-block",
		re:        regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
	},
	{
		profileID:     "generic-password-querystring",
		re:            regexp.MustCompile(`(?i)(?:password|secret|apikey|api_key|api-key|token)\s*[:=]\s*["']?([^"'&\s]+)`),
		submatchIndex: 1,
	},
	{
		profileID: "github-token",
		re:        regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`),
	},
	{
		profileID: "openai-key",
		re:        regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
	},
	{
		profileID: "anthropic-key",
		re:        regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}\b`),
	},
}

// RedactValue scans s for credential substrings using the built-in
// matcher catalog and returns the redacted string plus a slice of match
// records. Matched substrings are replaced with the explicit literal
// <REDACTED:profile_id> so the substitution is visually obvious in the
// exported transcript.
//
// This is the canonical redaction entry point for the export package.
// It is intentionally stateless and allocation-light for the simple
// string-scan case. It does NOT use the HMAC pipeline from
// core/event/redact — that pipeline is for audit-log payloads; export
// redaction uses the human-readable <REDACTED:…> form specified by
// FR-005.
func RedactValue(s string) (string, []RedactionMatch) {
	var matches []RedactionMatch
	out := s
	for _, m := range builtinMatchers {
		changed := false
		out = m.re.ReplaceAllStringFunc(out, func(hit string) string {
			if m.submatchIndex == 0 {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				changed = true
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			groups := m.re.FindStringSubmatchIndex(hit)
			if groups == nil || m.submatchIndex*2+1 >= len(groups) {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				changed = true
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			start, end := groups[m.submatchIndex*2], groups[m.submatchIndex*2+1]
			if start < 0 || end < 0 {
				matches = append(matches, RedactionMatch{ProfileID: m.profileID})
				changed = true
				return fmt.Sprintf("<REDACTED:%s>", m.profileID)
			}
			_ = changed
			matches = append(matches, RedactionMatch{ProfileID: m.profileID, Offset: start})
			return hit[:start] + fmt.Sprintf("<REDACTED:%s>", m.profileID) + hit[end:]
		})
		_ = changed
	}
	return out, matches
}

// redactMessages applies RedactValue to every string field in every
// message and returns a new slice of redacted copies.
func redactMessages(msgs []session.Message) []session.Message {
	out := make([]session.Message, len(msgs))
	for i, m := range msgs {
		m.Content, _ = RedactValue(m.Content)
		// Redact tool call arguments (Name is not content; Arguments values are).
		redactedCalls := make([]session.ToolCall, len(m.ToolCalls))
		for j, tc := range m.ToolCalls {
			redacted := make(map[string]any, len(tc.Arguments))
			for k, v := range tc.Arguments {
				if sv, ok := v.(string); ok {
					rv, _ := RedactValue(sv)
					redacted[k] = rv
				} else {
					redacted[k] = v
				}
			}
			tc.Arguments = redacted
			tc.Result, _ = RedactValue(tc.Result)
			redactedCalls[j] = tc
		}
		m.ToolCalls = redactedCalls
		out[i] = m
	}
	return out
}

// sanitiseTitle converts a session title into a filesystem-safe slug.
// Spaces become hyphens; non-printable and non-alphanumeric chars (except
// hyphen and underscore) are dropped. The result is lowercased and capped
// at 64 chars to keep filenames sane.
func sanitiseTitle(title string) string {
	var sb strings.Builder
	for _, r := range title {
		switch {
		case r == ' ' || r == '\t':
			sb.WriteByte('-')
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			sb.WriteRune(unicode.ToLower(r))
		}
	}
	slug := sb.String()
	// Collapse multiple consecutive hyphens.
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 64 {
		slug = slug[:64]
	}
	return slug
}
