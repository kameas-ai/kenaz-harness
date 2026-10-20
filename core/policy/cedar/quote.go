package cedar

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	cedar "github.com/cedar-policy/cedar-go"
)

// QuoteString returns s as a complete Cedar string literal, surrounding
// quotes included, for interpolating into generated policy text such as
// `resource == BashCommand::` + QuoteString(id).
//
// Every writer that puts a user- or model-influenced string into .cedar
// text must go through this (or QuoteLikePrefix). Raw concatenation is
// two bugs at once: a newline or a trailing backslash leaves the literal
// unterminated, which makes the whole policy directory fail to parse and
// the engine fail closed for every tool call; and a `"` closes the
// literal early, so the rest of the string is parsed as policy — a
// command named `x"); permit(principal, action, resource); //` would
// grant everything to everyone.
//
// The escaping is cedar-go's own (cedar.String.MarshalCedar), the same
// encoder EntityUID.String uses, so the literal parses back to exactly s
// under the vendored parser. Go's strconv.Quote is not a substitute: its
// \x.. and \u.... forms are not Cedar escapes.
func QuoteString(s string) string {
	return string(cedar.String(s).MarshalCedar())
}

// QuoteLikePrefix returns a Cedar `like` pattern literal (quotes
// included) matching prefix followed by anything. Besides the string
// escapes QuoteString applies, a literal `*` in prefix is escaped to
// `\*` so a directory named `*` cannot widen the grant to every path.
func QuoteLikePrefix(prefix string) string {
	return string(cedar.NewPattern(prefix, cedar.Wildcard{}).MarshalCedar())
}

// PersistableFamilyID reports whether id may be written into a durable
// "allow always" grant for one of the validated resource families
// (BashCommand, Credential, FilesystemOp). It is false when the
// request-side constructor (BashCommandUID and friends) would replace id
// with "invalid" — a grant on the raw id could then never match — and
// false for the literal "invalid" itself, because a permit on
// BashCommand::"invalid" would match every malformed pattern at once.
//
// It also requires SafeLiteralText: a string the vendored parser cannot
// read back must never reach a policy file.
func PersistableFamilyID(id string) bool {
	return validateFamilyID(id) && id != invalidUIDID && SafeLiteralText(id)
}

// SafeLiteralText reports whether s survives a round trip through a
// Cedar string literal under the vendored cedar-go parser. It must be
// valid UTF-8 and must not contain U+FFFD: cedar-go v1.6.0's unquoter
// rejects every utf8.RuneError ("bad unicode rune") — including a
// correctly encoded U+FFFD, which its escaper emits raw — and ranging
// over invalid UTF-8 yields U+FFFD. Either one in a generated snippet
// makes the file unparseable and the engine fail closed. Linux file
// names, argv[0] and MCP tool names are all arbitrary bytes, so this is
// reachable input.
func SafeLiteralText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, utf8.RuneError)
}

// ErrInvalidGeneratedSnippet is returned by ValidateGeneratedSnippet.
var ErrInvalidGeneratedSnippet = errors.New("cedar: generated policy snippet rejected")

// ValidateGeneratedSnippet parses body with the vendored cedar-go parser
// and requires it to contain exactly one policy. Every writer of a
// generated grant runs this before the file reaches the policy
// directory, so an escaping gap in some future writer degrades to "the
// grant was not saved" instead of "the user policy failed to load and
// every gated action fails closed" — and an injection that smuggles a
// second policy is refused even if it parses.
func ValidateGeneratedSnippet(body []byte) error {
	list, err := cedar.NewPolicyListFromBytes("", body)
	if err != nil {
		return fmt.Errorf("%w: does not parse: %v", ErrInvalidGeneratedSnippet, err)
	}
	if len(list) != 1 {
		return fmt.Errorf("%w: contains %d policies, want exactly 1", ErrInvalidGeneratedSnippet, len(list))
	}
	return nil
}

// ResourceScopeID parses body as Cedar policy text and returns the
// entity id of the first policy whose resource scope is
// `resource == <entityType>::"<id>"`. It is the inverse of a writer that
// built the scope with QuoteString: escapes are decoded by the real
// parser, so an id containing a quote or backslash round-trips exactly
// instead of being cut at the first `"` a string search would find.
// Returns false when body does not parse or no policy has that scope.
func ResourceScopeID(body []byte, entityType string) (string, bool) {
	list, err := cedar.NewPolicyListFromBytes("", body)
	if err != nil {
		return "", false
	}
	for _, p := range list {
		raw, err := p.MarshalJSON()
		if err != nil {
			continue
		}
		var doc struct {
			Resource struct {
				Op     string `json:"op"`
				Entity *struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"entity"`
			} `json:"resource"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		if doc.Resource.Op == "==" && doc.Resource.Entity != nil && doc.Resource.Entity.Type == entityType {
			return doc.Resource.Entity.ID, true
		}
	}
	return "", false
}
