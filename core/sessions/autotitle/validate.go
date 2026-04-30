package autotitle

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// ErrTitleTooShort is returned by Sanitize when the cleaned output has
// fewer than 3 runes. This signals a model non-answer ("ok", ".", "")
// that should not be stored as a session title.
var ErrTitleTooShort = errors.New("autotitle: title too short (< 3 runes)")

// ErrModelRefused is returned by Sanitize when the model output starts
// with a refusal prefix such as "Sorry", "I can't", or "I cannot".
// The session title should be left as the placeholder in this case.
var ErrModelRefused = errors.New("autotitle: model refused to generate a title")

// maxRunes is the enforced rune-count ceiling (plan §2.6 / spec FR-007).
const maxRunes = 50

// minRunes is the enforced rune-count floor.
const minRunes = 3

// refusalRE matches the leading refusal prefixes the plan identifies
// (plan §3 risk register, "Sorry|I can't|I cannot"). The match is
// case-insensitive and anchored to the start of the sanitised text.
var refusalRE = regexp.MustCompile(`(?i)^(sorry\b|i can'?t\b|i cannot\b)`)

// titlePrefixRE strips a leading "Title:" label the model sometimes
// emits despite instructions (plan §2.6).
var titlePrefixRE = regexp.MustCompile(`(?i)^title\s*:\s*`)

// Sanitize cleans raw model output into a usable session title.
//
// Rules applied in order (plan §2.6):
//  1. Replace control characters (< 0x20, except space) with a space.
//  2. Trim leading/trailing ASCII whitespace.
//  3. Strip matched outer single or double quotes.
//  4. Strip a leading "Title:"-style prefix (case-insensitive).
//  5. Take only the first non-empty line when the string contains a newline.
//  6. Trim whitespace again after the above transformations.
//  7. Rune-count < 3  → ErrTitleTooShort.
//  8. Refusal prefix  → ErrModelRefused.
//  9. Rune-count > 50 → truncate to 49 runes + "…".
func Sanitize(raw string) (string, error) {
	// Step 1: replace control characters with space.
	s := replaceControlChars(raw)

	// Step 2: trim whitespace.
	s = strings.TrimSpace(s)

	// Step 3: strip matched outer quotes.
	s = stripOuterQuotes(s)

	// Step 4: strip leading "Title:" prefix.
	s = titlePrefixRE.ReplaceAllString(s, "")

	// Step 5: first non-empty line only.
	s = firstNonEmptyLine(s)

	// Step 6: trim whitespace again.
	s = strings.TrimSpace(s)

	// Step 7: too short.
	if len([]rune(s)) < minRunes {
		return "", ErrTitleTooShort
	}

	// Step 8: refusal check.
	if refusalRE.MatchString(s) {
		return "", ErrModelRefused
	}

	// Step 9: truncate oversize titles.
	runes := []rune(s)
	if len(runes) > maxRunes {
		s = string(runes[:maxRunes-1]) + "…"
	}

	return s, nil
}

// replaceControlChars replaces every control character (code point < 0x20,
// excluding space 0x20 itself, plus DEL 0x7F) with a plain ASCII space.
// Newlines are preserved so that firstNonEmptyLine can still work.
func replaceControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return r // preserve newline / CR for first-line extraction
		}
		if r < 0x20 || r == 0x7F || (r != ' ' && unicode.IsControl(r)) {
			return ' '
		}
		return r
	}, s)
}

// stripOuterQuotes removes a matched pair of outer single or double quotes
// from s, if and only if both the opening and closing character are the
// same quote type and the string has at least 2 characters.
func stripOuterQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first := rune(s[0])
	if first != '"' && first != '\'' {
		return s
	}
	runes := []rune(s)
	if runes[len(runes)-1] == first {
		return string(runes[1 : len(runes)-1])
	}
	return s
}

// firstNonEmptyLine returns the first non-empty line of s, trimming
// leading/trailing whitespace from that line. If s contains no newline,
// the string is returned unchanged.
func firstNonEmptyLine(s string) string {
	if !strings.ContainsAny(s, "\n\r") {
		return s
	}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, "\r") // handle \r\n
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return s
}
