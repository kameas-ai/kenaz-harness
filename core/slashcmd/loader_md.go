package slashcmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrMalformedFrontmatter is returned when the markdown file's
// frontmatter block cannot be parsed.
var ErrMalformedFrontmatter = errors.New("slashcmd: malformed frontmatter")

// frontmatterDelimiter is the YAML front-matter fence.
const frontmatterDelimiter = "---"

// ParseMarkdown splits a markdown-with-frontmatter byte slice into
// a UserCommand (frontmatter fields) + body (markdown prose below
// the closing --- fence). The returned UserCommand's Body field is
// set from the markdown portion.
//
// Format expected:
//
//	---
//	name: deploy
//	scope: project
//	...
//	---
//	body content here
//
// An empty frontmatter block ("---\n---\n") is valid but yields an
// empty UserCommand with only Body populated. Files without any
// frontmatter delimiter are rejected with ErrMalformedFrontmatter.
func ParseMarkdown(data []byte) (UserCommand, error) {
	s := string(data)

	// Normalize CRLF.
	s = strings.ReplaceAll(s, "\r\n", "\n")

	// Require leading "---\n" (or "---" at start of file).
	if !strings.HasPrefix(s, frontmatterDelimiter) {
		return UserCommand{}, fmt.Errorf("%w: file must begin with ---", ErrMalformedFrontmatter)
	}

	// Strip the leading "---" and find the closing "---".
	rest := s[len(frontmatterDelimiter):]
	// Allow optional newline after opening fence.
	rest = strings.TrimLeft(rest, "\n")

	closingIdx := strings.Index(rest, "\n"+frontmatterDelimiter)
	if closingIdx < 0 {
		// Also try "---" at start (empty frontmatter with no leading newline).
		if strings.HasPrefix(rest, frontmatterDelimiter) {
			closingIdx = 0
		} else {
			return UserCommand{}, fmt.Errorf("%w: missing closing ---", ErrMalformedFrontmatter)
		}
	}

	var fmRaw string
	var bodyRaw string

	if strings.HasPrefix(rest, frontmatterDelimiter) {
		// Empty frontmatter: "---\n---\n".
		fmRaw = ""
		afterClose := rest[len(frontmatterDelimiter):]
		bodyRaw = strings.TrimPrefix(afterClose, "\n")
	} else {
		fmRaw = rest[:closingIdx]
		afterFence := rest[closingIdx+1+len(frontmatterDelimiter):]
		bodyRaw = strings.TrimPrefix(afterFence, "\n")
	}

	var cmd UserCommand
	if fmRaw != "" {
		dec := yaml.NewDecoder(bytes.NewBufferString(fmRaw))
		dec.KnownFields(false) // unknown fields are allowed (forward compat)
		if err := dec.Decode(&cmd); err != nil {
			return UserCommand{}, fmt.Errorf("%w: %v", ErrMalformedFrontmatter, err)
		}
	}
	cmd.Body = strings.TrimLeft(strings.TrimRight(bodyRaw, "\n"), "\n")
	return cmd, nil
}

// MarshalMarkdown serialises cmd back into its markdown-with-frontmatter
// form. The round-trip ParseMarkdown(MarshalMarkdown(cmd)) must produce
// an equivalent UserCommand (modulo zero-value omitted fields).
func MarshalMarkdown(cmd UserCommand) ([]byte, error) {
	// Marshal only the frontmatter fields (Body, PayloadPath, timestamps
	// are excluded via yaml:"-" tags).
	fmBytes, err := yaml.Marshal(&cmd)
	if err != nil {
		return nil, fmt.Errorf("slashcmd: marshal frontmatter: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(fmBytes)
	buf.WriteString("---\n")
	if cmd.Body != "" {
		buf.WriteString("\n")
		buf.WriteString(cmd.Body)
		buf.WriteString("\n")
	}
	return buf.Bytes(), nil
}
