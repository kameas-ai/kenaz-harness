package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// CanonicalizeSQL reduces an SQL string to its canonical form for content
// hashing per package doc.go. The transformation is:
//
//   - Normalize "\r\n" and "\r" to "\n".
//   - Strip "/* ... */" block comments (non-nesting).
//   - Strip "--" line comments (from "--" through end of line).
//   - Collapse all whitespace runs (space, tab, newline) to a single ASCII space.
//   - Trim leading/trailing whitespace.
//
// Whitespace adjacent to structural punctuation (parens, commas,
// semicolons) is intentionally preserved here so authoring style stays
// representable in the canonical form. HashSQL applies an additional
// squeeze step before hashing so two stylistic variants — "( id INT )"
// vs "(id INT)" — produce the same content hash.
//
// The function is intentionally simple — it does not understand quoted
// strings, so a "--" embedded inside a string literal will be treated as
// a comment. This is a known and documented constraint: migrations
// should not embed "--" inside strings. If they must, register a
// pre-computed ContentHash and rely on UpSource being supplied as
// already-canonicalized.
func CanonicalizeSQL(src string) string {
	// 1. Normalize line endings.
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")

	// 2. Strip block comments first (so a "/* ... -- ... */" doesn't get
	// half-recognized as a line comment).
	src = stripBlockComments(src)

	// 3. Strip line comments.
	src = stripLineComments(src)

	// 4. Collapse whitespace.
	src = collapseWhitespace(src)

	// 5. Trim.
	return strings.TrimSpace(src)
}

func stripBlockComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			// find closing */
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				// unterminated; treat the rest as comment
				return b.String()
			}
			i = i + 2 + end + 2
			b.WriteByte(' ') // replace comment with single space to preserve token boundary
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

func stripLineComments(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '-' && src[i+1] == '-' {
			// skip to end of line (or end of input)
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				// rest of input is the comment
				b.WriteByte(' ')
				return b.String()
			}
			b.WriteByte(' ') // preserve token boundary
			i += end         // leave the '\n' for the loop body to write
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

func collapseWhitespace(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	prevSpace := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteByte(c)
		prevSpace = false
	}
	return b.String()
}

// squeezePunctuation removes whitespace inside the visible bounds of
// structural SQL grouping: a space immediately after "(" or immediately
// before ")", and a space immediately before "," or ";". External
// spacing (e.g. "foo (id INT)") is preserved so authoring style remains
// representable in the canonical form.
func squeezePunctuation(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ' ' {
			// Drop a space before ")", ",", or ";".
			if i+1 < len(src) {
				next := src[i+1]
				if next == ')' || next == ',' || next == ';' {
					continue
				}
			}
			// Drop a space immediately after "(".
			if b.Len() > 0 && b.String()[b.Len()-1] == '(' {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// HashSQL canonicalizes src, additionally squeezes whitespace adjacent
// to structural punctuation, and returns the lowercase hex SHA-256
// digest. The extra squeeze is applied only at hashing time so the
// human-readable canonical form returned by CanonicalizeSQL retains
// authoring style, while content equality remains stable across the
// stylistic variants the squeeze normalises away.
func HashSQL(src string) string {
	canonical := squeezePunctuation(CanonicalizeSQL(src))
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
