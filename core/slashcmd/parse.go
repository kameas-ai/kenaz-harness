package slashcmd

import (
	"errors"
	"strings"
)

// ErrEmptyInput is returned by parseRaw when the input is empty after
// trimming whitespace.
var ErrEmptyInput = errors.New("slashcmd: empty input")

// ErrMissingSlash is returned by parseRaw when the trimmed input does
// not begin with a leading "/". The composer guards this client-side,
// but the RPC view re-validates so a stray dispatch with a non-slash
// payload returns a well-typed error rather than treating the entire
// string as a command name.
var ErrMissingSlash = errors.New("slashcmd: input must begin with /")

// ErrMissingName is returned when the input is exactly "/" with
// nothing after it.
var ErrMissingName = errors.New("slashcmd: missing command name")

// parseRaw splits "/foo a b c" into ("foo", []{"a", "b", "c"}, nil).
// Whitespace is the only separator in v1; quoted-string handling is
// deferred until a v1 command actually needs an arg with spaces.
//
// Validation:
//   - Empty trimmed input → ErrEmptyInput.
//   - No leading slash    → ErrMissingSlash.
//   - "/" with nothing    → ErrMissingName.
func parseRaw(raw string) (name string, args []string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil, ErrEmptyInput
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", nil, ErrMissingSlash
	}
	body := strings.TrimPrefix(trimmed, "/")
	if body == "" {
		return "", nil, ErrMissingName
	}
	tokens := strings.Fields(body)
	if len(tokens) == 0 {
		return "", nil, ErrMissingName
	}
	name = tokens[0]
	if len(tokens) > 1 {
		args = tokens[1:]
	}
	return name, args, nil
}
