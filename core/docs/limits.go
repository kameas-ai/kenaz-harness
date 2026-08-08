package docs

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// Bounds on a document body. These exist for two reasons: an unbounded
// HTML parser is a denial-of-service vector (memory, and browser-side
// layout cost on the reading end), and FR-004 requires an explicit,
// enforced per-document cap with an actionable error rather than
// truncation.
const (
	// MaxBodyBytes caps a sanitized document body at 10 MiB.
	//
	// This deliberately matches the shipped kenaz__save_artifact /
	// kenaz__update_artifact cap (core/tools/saveartifact.MaxContentBytes)
	// rather than the 25 MiB the spec floated. Two reasons: one number is
	// easier to explain to a model and to a user than two, and 10 MiB of
	// *sanitized HTML* is already an enormous document — the realistic way
	// to exceed it is data-URI images, which belong in the CAS
	// (<DataDir>/media/<sha256>) and are not counted against this cap once
	// the model layer references them from the body.
	//
	// The cap is checked on both the input and the sanitized output.
	// Sanitization can *grow* a body, because text and attribute values
	// are HTML-escaped on the way out ("<" becomes "&lt;"), so an input
	// that fits can produce an output that does not.
	MaxBodyBytes = 10 * 1024 * 1024

	// MaxNestingDepth caps open-element nesting.
	//
	// The measurement is a conservative upper bound taken from the token
	// stream, not from a parse tree: every non-void start tag increments,
	// every end tag decrements. HTML's implicit-close rules mean the real
	// tree is often shallower than this count (`<p><p><p>` is three
	// siblings to a browser but depth 3 here), so a body that passes this
	// check is guaranteed to be within the bound, never the reverse.
	//
	// 256 sits an order of magnitude above any document a human or a model
	// authors on purpose and an order of magnitude below the ~512-deep
	// point where browser parsers start clamping.
	MaxNestingDepth = 256

	// MaxTitleRunes bounds a document title, mirroring
	// core/tools/saveartifact.MaxTitleRunes. Declared here so the document
	// model (task 2.H-MODEL) has one place to read limits from; nothing in
	// this file enforces it, because titles are plain text and never pass
	// through Sanitize.
	MaxTitleRunes = 200
)

// Limit failures. These are the only errors Sanitize returns; a body that
// clears CheckLimits always sanitizes successfully.
var (
	// ErrBodyTooLarge reports a body over MaxBodyBytes, either as supplied
	// or after escaping.
	ErrBodyTooLarge = errors.New("docs: document body exceeds the maximum size")

	// ErrBodyTooDeep reports a body whose element nesting exceeds
	// MaxNestingDepth.
	ErrBodyTooDeep = errors.New("docs: document body exceeds the maximum nesting depth")

	// ErrBodyNotUTF8 reports a body that is not valid UTF-8.
	//
	// We reject rather than repair. The HTML tokenizer would happily
	// substitute U+FFFD for each bad byte, which is safe but is silent
	// corruption of the user's document — precisely what SC-003 forbids
	// elsewhere in this feature. An actionable error at the write gate is
	// the better failure.
	ErrBodyNotUTF8 = errors.New("docs: document body is not valid UTF-8")
)

// Wire error codes for the tool and RPC layers. content_too_large matches
// the string kenaz__save_artifact / kenaz__update_artifact already return,
// so a model that has learned to react to one reacts correctly to both.
const (
	CodeContentTooLarge = "content_too_large"
	CodeContentTooDeep  = "content_too_nested"
	CodeContentNotUTF8  = "content_not_utf8"
	CodeInternal        = "internal_error"
)

// ErrorCode maps a sanitizer error onto its wire code. It returns "" for a
// nil error so callers can write `if code := docs.ErrorCode(err); code != ""`.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrBodyTooLarge):
		return CodeContentTooLarge
	case errors.Is(err, ErrBodyTooDeep):
		return CodeContentTooDeep
	case errors.Is(err, ErrBodyNotUTF8):
		return CodeContentNotUTF8
	default:
		return CodeInternal
	}
}

// CheckLimits validates a body against the bounds above without
// sanitizing it. Sanitize calls this first; callers that want to reject
// early (before buffering a large upload, say) can call it directly.
func CheckLimits(body string) error {
	if len(body) > MaxBodyBytes {
		return ErrBodyTooLarge
	}
	if !utf8.ValidString(body) {
		return ErrBodyNotUTF8
	}
	if nestingDepth(body) > MaxNestingDepth {
		return ErrBodyTooDeep
	}
	return nil
}

// nestingDepth reports the maximum open-element depth in the token stream.
// See MaxNestingDepth for why this is an upper bound rather than an exact
// tree depth. It tokenizes only — no tree is built — so cost is linear in
// input size with constant memory.
func nestingDepth(body string) int {
	z := html.NewTokenizer(strings.NewReader(body))
	depth, max := 0, 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			// io.EOF or a malformed-input error; either way the stream is
			// over and max holds the answer.
			return max
		case html.StartTagToken:
			name, _ := z.TagName()
			if isVoidElement(string(name)) {
				continue
			}
			depth++
			if depth > max {
				max = depth
			}
		case html.EndTagToken:
			if depth > 0 {
				depth--
			}
		}
	}
}

// voidElements are the HTML elements that never have an end tag. A start
// tag for one of these does not open a nesting level.
//
// The list is x/net/html's own void-element set. It matters beyond depth
// counting: bluemonday's SkipElementsContent works by flipping a flag on
// the start tag and clearing it on the matching end tag, so adding a void
// element to that set would swallow the remainder of the document. See the
// AllowElementsContent("frame") call in sanitize.go.
var voidElements = map[string]struct{}{
	"area": {}, "base": {}, "basefont": {}, "bgsound": {}, "br": {},
	"col": {}, "command": {}, "embed": {}, "frame": {}, "hr": {},
	"image": {}, "img": {}, "input": {}, "isindex": {}, "keygen": {},
	"link": {}, "menuitem": {}, "meta": {}, "nextid": {}, "param": {},
	"source": {}, "spacer": {}, "track": {}, "wbr": {},
}

func isVoidElement(name string) bool {
	_, ok := voidElements[strings.ToLower(name)]
	return ok
}
