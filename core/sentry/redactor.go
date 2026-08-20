package sentry

import (
	"fmt"
	"os"
	"reflect"
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

// MaxRedactDepth bounds the recursive walk in redactValue, mirroring
// core/sessions/export/redact.go's MaxRedactDepth precedent (the export
// redactor v0.63.1 fixed for the same reason). A crash-report payload
// (event.Extra, a context map, frame.Vars) is data this app produced;
// nothing legitimate nests this deep, and an unbounded walk over a
// pathological or adversarial payload is a stack blowup in a function
// whose entire job is to run on content about to leave the process. At
// the bound the value is replaced with a marker rather than passed
// through unexamined — a redactor that gives up must fail CLOSED.
const MaxRedactDepth = 24

// redactValue recursively redacts an arbitrary value: strings go through
// RedactStringDeep, maps and slices recurse (dropping ShouldDropSlogKey
// keys at EVERY level, not just the top), pointers/interfaces are
// dereferenced, depth is bounded, cycles are guarded per-path (seen is
// cleared on return from a subtree, so a legitimately repeated SIBLING
// value is not mistaken for a cycle), and the walk fails CLOSED at the
// depth bound. Every other leaf (numbers, etc.) is formatted and scanned
// like a string, because that is what any consumer of the crash report
// will eventually see rendered — matching
// core/sessions/export/redact.go's redactStructured, whose SHAPE this
// reuses (not its rule set: that function's "a key can NAME a secret"
// forcing behaviour is specific to free-form tool-call arguments and
// does not apply to a slog-attribute-shaped crash context).
func redactValue(v any, depth int, seen map[uintptr]struct{}) any {
	if v == nil {
		return nil
	}
	if depth > MaxRedactDepth {
		return "[REDACTED:depth-limit]"
	}

	switch tv := v.(type) {
	case string:
		return RedactStringDeep(tv)
	case bool:
		return tv
	case map[string]any:
		if len(tv) > 0 {
			p := reflect.ValueOf(tv).Pointer()
			if _, cycle := seen[p]; cycle {
				return "[REDACTED:cycle]"
			}
			seen[p] = struct{}{}
			defer delete(seen, p)
		}
		out := make(map[string]any, len(tv))
		for k, val := range tv {
			if ShouldDropSlogKey(k) {
				continue
			}
			out[k] = redactValue(val, depth+1, seen)
		}
		return out
	case []any:
		if len(tv) > 0 {
			p := reflect.ValueOf(tv).Pointer()
			if _, cycle := seen[p]; cycle {
				return "[REDACTED:cycle]"
			}
			seen[p] = struct{}{}
			defer delete(seen, p)
		}
		out := make([]any, len(tv))
		for i, val := range tv {
			out[i] = redactValue(val, depth+1, seen)
		}
		return out
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		if rv.Kind() == reflect.Pointer {
			if _, cycle := seen[rv.Pointer()]; cycle {
				return "[REDACTED:cycle]"
			}
			seen[rv.Pointer()] = struct{}{}
			defer delete(seen, rv.Pointer())
		}
		return redactValue(rv.Elem().Interface(), depth+1, seen)

	case reflect.Map:
		if _, cycle := seen[rv.Pointer()]; cycle {
			return "[REDACTED:cycle]"
		}
		seen[rv.Pointer()] = struct{}{}
		defer delete(seen, rv.Pointer())
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			k := fmt.Sprintf("%v", iter.Key().Interface())
			if ShouldDropSlogKey(k) {
				continue
			}
			out[k] = redactValue(iter.Value().Interface(), depth+1, seen)
		}
		return out

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice {
			if rv.IsNil() {
				return nil
			}
			if _, cycle := seen[rv.Pointer()]; cycle {
				return "[REDACTED:cycle]"
			}
			seen[rv.Pointer()] = struct{}{}
			defer delete(seen, rv.Pointer())
		}
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = redactValue(rv.Index(i).Interface(), depth+1, seen)
		}
		return out
	}

	// Every other leaf: a number, a json.Number, a time, a Stringer.
	// Format it and scan the text; if nothing matched, return the
	// original value unchanged so its Go type is preserved.
	text := fmt.Sprintf("%v", v)
	if redacted := RedactString(text); redacted != text {
		return redacted
	}
	return v
}

// RedactMap applies redaction recursively to every value in m — nested
// maps and slices included, at every depth, not just the top level — and
// drops keys that match ShouldDropSlogKey at every level too. The
// original map is not modified; a new map is returned.
func RedactMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]any, len(m))
	seen := map[uintptr]struct{}{}
	for k, v := range m {
		if ShouldDropSlogKey(k) {
			continue
		}
		out[k] = redactValue(v, 1, seen)
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
