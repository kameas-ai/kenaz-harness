package llm

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"strings"
)

// ExtractEmbeddedToolCalls scans assistant text for JSON-shaped tool
// call envelopes that Llama-family models (and some other instruction-
// tuned models) emit inside their text instead of using the provider's
// structured tool-call channel. Returns the extracted calls and the
// residual text with the envelopes removed.
//
// Pattern matched:
//
//	{"type": "function", "name": "<tool>", "parameters": {...}}
//
// Also tolerates `arguments` instead of `parameters` (OpenAI-shape
// leakage), and the same envelope wrapped in a ```json fence.
//
// Conservative: only top-level objects with the exact
// {type:"function", name, parameters|arguments} shape are treated as
// tool envelopes. Plain JSON (a model showing the user a JSON example)
// stays as text.
//
// Lifted from core/llm/bedrock/bearer.go (WP01 of
// provider-implementation-uniformity-01KQ8V4F).
func ExtractEmbeddedToolCalls(text string) (calls []ToolUse, residual string) {
	if text == "" || !strings.Contains(text, `"type"`) || !strings.Contains(text, `"function"`) {
		return nil, text
	}
	var residualBuf strings.Builder
	i := 0
	for i < len(text) {
		open := strings.IndexByte(text[i:], '{')
		if open < 0 {
			residualBuf.WriteString(text[i:])
			break
		}
		open += i
		residualBuf.WriteString(text[i:open])
		end := matchBraceEnd(text, open)
		if end < 0 {
			residualBuf.WriteString(text[open:])
			break
		}
		candidate := text[open : end+1]
		if call, ok := parseEmbeddedToolEnvelope(candidate); ok {
			calls = append(calls, call)
			i = end + 1
			continue
		}
		// Not a tool envelope — keep the brace and advance one char.
		residualBuf.WriteByte(text[open])
		i = open + 1
	}
	return calls, residualBuf.String()
}

// matchBraceEnd returns the index of the `}` that closes the `{` at
// position open, respecting nested objects and strings. Returns -1 if
// the object is unterminated.
func matchBraceEnd(s string, open int) int {
	depth := 0
	inStr := false
	esc := false
	for i := open; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseEmbeddedToolEnvelope decodes a candidate JSON object as a
// tool-call envelope. Returns the extracted ToolUse and ok=true on
// match; (zero, false) otherwise.
func parseEmbeddedToolEnvelope(candidate string) (ToolUse, bool) {
	var env struct {
		Type       string          `json:"type"`
		Name       string          `json:"name"`
		ID         string          `json:"id,omitempty"`
		Parameters json.RawMessage `json:"parameters,omitempty"`
		Arguments  json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal([]byte(candidate), &env); err != nil {
		return ToolUse{}, false
	}
	if env.Type != "function" || env.Name == "" {
		return ToolUse{}, false
	}
	input := env.Parameters
	if len(input) == 0 {
		input = env.Arguments
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	id := env.ID
	if id == "" {
		// Synthesise a deterministic id so the kernel's tool_dispatch
		// can pair this with a result message; using a content hash keeps
		// it stable across replays.
		id = "synthetic_" + syntheticToolID(env.Name + string(input))
	}
	return ToolUse{ID: id, Name: env.Name, Input: input}, true
}

// syntheticToolID returns a short hex digest suitable for synthesised
// tool call ids. Not cryptographic — just stable + collision-resistant
// for the per-turn tool-call set.
func syntheticToolID(s string) string {
	h := crc32.ChecksumIEEE([]byte(s))
	return fmt.Sprintf("%08x", h)
}
