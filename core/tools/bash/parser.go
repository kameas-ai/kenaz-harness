// Package bash provides a sandboxed wrapper around os/exec for the
// built-in kaneaz__bash tool. The package never spawns a shell — input
// command strings are split into argv via Parse (a hand-rolled
// shlex-style state machine) and dispatched directly through
// exec.CommandContext. No globbing, no variable expansion, no command
// substitution, no pipes.
package bash

import "errors"

// ErrUnterminatedQuote is returned by Parse when the input ends while
// still inside a single- or double-quoted segment.
var ErrUnterminatedQuote = errors.New("bash: unterminated quote")

// Parse splits a shell-like command string into argv tokens.
//
// Quoting rules deliberately mirror POSIX sh:
//   - Single quotes preserve every byte literally; no escapes apply.
//     A backslash inside single quotes is a literal backslash.
//   - Double quotes preserve every byte literally except that a
//     backslash escapes ", \, $, and ` (the four characters that would
//     otherwise have special meaning inside double quotes).
//   - Outside quotes a backslash escapes the next byte literally.
//   - Whitespace (space + tab) separates tokens.
//
// Parse never expands shell variables, globs, command substitutions,
// pipes, or redirects. Metacharacters such as |, >, <, &, ; pass
// through as ordinary token bytes; the allowlist downstream rejects
// any argv whose program isn't a known-safe binary.
func Parse(cmd string) ([]string, error) {
	var (
		argv    []string
		current []byte
		inToken bool
	)
	const (
		stateNormal = iota
		stateSingle
		stateDouble
	)
	state := stateNormal
	i := 0
	for i < len(cmd) {
		c := cmd[i]
		switch state {
		case stateNormal:
			switch {
			case c == ' ' || c == '\t':
				if inToken {
					argv = append(argv, string(current))
					current = current[:0]
					inToken = false
				}
				i++
			case c == '\'':
				inToken = true
				state = stateSingle
				i++
			case c == '"':
				inToken = true
				state = stateDouble
				i++
			case c == '\\':
				// Backslash escapes the next byte literally. A
				// trailing backslash with no successor passes
				// through as a literal backslash.
				if i+1 < len(cmd) {
					current = append(current, cmd[i+1])
					i += 2
				} else {
					current = append(current, '\\')
					i++
				}
				inToken = true
			default:
				current = append(current, c)
				inToken = true
				i++
			}
		case stateSingle:
			if c == '\'' {
				state = stateNormal
				i++
				continue
			}
			current = append(current, c)
			i++
		case stateDouble:
			switch {
			case c == '"':
				state = stateNormal
				i++
			case c == '\\' && i+1 < len(cmd) && isDoubleQuoteEscape(cmd[i+1]):
				current = append(current, cmd[i+1])
				i += 2
			default:
				current = append(current, c)
				i++
			}
		}
	}
	if state != stateNormal {
		return nil, ErrUnterminatedQuote
	}
	if inToken {
		argv = append(argv, string(current))
	}
	return argv, nil
}

// isDoubleQuoteEscape reports whether b is one of the four characters
// that backslash escapes inside double quotes per POSIX rules: ", \,
// $, and `. Any other byte after a backslash inside double quotes is
// preserved as-is including the backslash.
func isDoubleQuoteEscape(b byte) bool {
	return b == '"' || b == '\\' || b == '$' || b == '`'
}
