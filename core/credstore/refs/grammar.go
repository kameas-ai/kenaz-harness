package refs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Sentinel errors returned by Parse.
var (
	// ErrInvalidReference is returned when the input does not match
	// the @secret:<locator>(#<field>)? grammar.
	ErrInvalidReference = errors.New("refs: invalid secret reference")

	// ErrEmptyLocator is returned when the locator segment is present
	// but contains no characters after the "@secret:" prefix.
	ErrEmptyLocator = errors.New("refs: empty locator in secret reference")
)

// Reference is a parsed @secret:<locator>(#<field>)? token.
// Both fields are guaranteed non-empty for the Locator after a successful
// Parse. Field is empty when no "#<field>" segment was present.
type Reference struct {
	// Locator is the keychain locator, e.g. "user:example-api-token" or
	// "provider:bedrock-prod". Same namespace as core/secrets/ref.
	Locator string
	// Field, when non-empty, names a sub-field of a structured credential,
	// e.g. "aws_session_token" from "@secret:provider:bedrock-prod#aws_session_token".
	Field string
}

// Token returns the canonical @secret: reference token for this Reference.
// Useful for redaction placeholders.
func (r Reference) Token() string {
	if r.Field != "" {
		return "@secret:" + r.Locator + "#" + r.Field
	}
	return "@secret:" + r.Locator
}

// Match is one occurrence of a reference within a string, including the
// byte offsets so callers can splice the replacement back in.
type Match struct {
	Reference Reference
	// Start is the byte offset of the '@' in the source string.
	Start int
	// End is the byte offset immediately after the last byte of the token.
	End int
	// Raw is the literal matched text (identical to Reference.Token()).
	Raw string
}

// locatorSegRe matches a locator character class:
// lowercase/uppercase letters, digits, hyphens, underscores, and colons.
// Colons are valid inside locators (e.g. "provider:bedrock-prod").
var locatorSegRe = regexp.MustCompile(`^[A-Za-z0-9:_-]+$`)

// fieldSegRe matches a field segment: letters, digits, underscores, hyphens.
var fieldSegRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// refPattern is the compiled regexp for finding @secret: references.
// It matches the full token including an optional #field.
//
// Group 1: locator
// Group 2: field (without the '#', may be empty/absent)
var refPattern = regexp.MustCompile(`@secret:([A-Za-z0-9:_-]+)(?:#([A-Za-z0-9_-]+))?`)

// Parse parses a single secret reference token. The input must be
// exactly one token — no surrounding text. Use FindAll for scanning
// within larger strings.
//
// Accepted forms:
//
//	@secret:user:example-api-token
//	@secret:provider:bedrock-prod#aws_session_token
//
// Rejected:
//   - anything not starting with the literal "@secret:" prefix
//   - empty locator after the prefix
//   - locator containing NUL or control characters
//   - field containing NUL or control characters
func Parse(s string) (Reference, error) {
	if !strings.HasPrefix(s, "@secret:") {
		return Reference{}, fmt.Errorf("%w: must start with @secret:", ErrInvalidReference)
	}
	rest := s[len("@secret:"):]
	if rest == "" {
		return Reference{}, ErrEmptyLocator
	}
	// Reject NUL bytes and ASCII control characters before any regex.
	for _, b := range []byte(rest) {
		if b == 0 || b < 0x20 {
			return Reference{}, fmt.Errorf("%w: locator contains control character (0x%02x)", ErrInvalidReference, b)
		}
	}
	var locator, field string
	idx := strings.IndexByte(rest, '#')
	if idx != -1 {
		locator = rest[:idx]
		field = rest[idx+1:]
	} else {
		locator = rest
	}
	if locator == "" {
		return Reference{}, ErrEmptyLocator
	}
	if !locatorSegRe.MatchString(locator) {
		return Reference{}, fmt.Errorf("%w: locator %q contains invalid characters", ErrInvalidReference, locator)
	}
	if idx != -1 && field == "" {
		// '#' was present but the field segment is empty.
		return Reference{}, fmt.Errorf("%w: empty field after '#' in %q", ErrInvalidReference, s)
	}
	if field != "" && !fieldSegRe.MatchString(field) {
		return Reference{}, fmt.Errorf("%w: field %q contains invalid characters", ErrInvalidReference, field)
	}
	return Reference{Locator: locator, Field: field}, nil
}

// IsReferenceToken reports whether s is a syntactically valid
// @secret: reference token (with or without a field). This is a
// cheaper check than Parse when the caller only needs a yes/no answer.
func IsReferenceToken(s string) bool {
	_, err := Parse(s)
	return err == nil
}

// FindAll scans src for all @secret: reference tokens and returns them
// in input order. Overlapping matches are not possible given the grammar;
// the scan is left-to-right and non-overlapping.
//
// Byte offsets in each Match are relative to the start of src.
// Malformed tokens that start with "@secret:" but fail the grammar are
// silently skipped — the caller can detect them via a separate lint pass.
func FindAll(src string) []Match {
	rawMatches := refPattern.FindAllStringIndex(src, -1)
	if len(rawMatches) == 0 {
		return nil
	}
	out := make([]Match, 0, len(rawMatches))
	for _, loc := range rawMatches {
		start, end := loc[0], loc[1]
		raw := src[start:end]
		ref, err := Parse(raw)
		if err != nil {
			// Syntactically invalid despite matching the broad regex — skip.
			continue
		}
		out = append(out, Match{
			Reference: ref,
			Start:     start,
			End:       end,
			Raw:       raw,
		})
	}
	return out
}
