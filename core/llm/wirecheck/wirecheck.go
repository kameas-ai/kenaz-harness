// Package wirecheck provides shared helpers for per-adapter wire-shape
// contract tests.
//
// # Motivation
//
// Silent-drop bugs — where an adapter serialises a GenerationRequest
// field but the wrong key name, or parses a streamed tool_call but
// discards it — are invisible to unit tests that mock the LLM boundary.
// wirecheck helpers close that gap by letting adapter tests declare the
// expected wire shape in a concise table rather than 30 LOC per field.
//
// # AssertSerialized
//
// Use AssertSerialized when you need to check that specific JSON fields
// appear in the request body an adapter writes to the wire:
//
//	body, _ := adapter.BuildRequestBody(req, prof)
//	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
//	    {Pointer: "/tools/0/name",        WantString: "read_file"},
//	    {Pointer: "/tools/0/input_schema", WantPresent: true},
//	    {Pointer: "/stream",              WantBool: boolPtr(true)},
//	})
//
// # AssertParsedFromWire
//
// Use AssertParsedFromWire when you need to check that an SSE byte
// slice is correctly parsed by the adapter's stream pump into the
// expected sequence of corellm.StreamEvent values:
//
//	wirecheck.AssertParsedFromWire(t, sseFixture, []wirecheck.EventExpectation{
//	    {Kind: "tool", WantToolName: "read_file"},
//	    {Kind: "finish", WantFinish: "tool_use"},
//	})
//
// # Registry and completeness check
//
// See [LoadRegistry] and [AssertRegistryComplete] for the YAML-driven
// reflection check that enforces coverage on every adapter × field pair.
package wirecheck

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// FieldExpectation describes one JSON field assertion on a serialised
// request body. Use JSON Pointer syntax (RFC 6901) for Pointer — e.g.
// "/tools/0/name" or "/messages/0/content/0/type".
type FieldExpectation struct {
	// Pointer is the RFC 6901 JSON pointer into the parsed request body.
	// "/" means the root object.
	Pointer string

	// WantPresent asserts the pointer resolves to any non-null value.
	// Mutually exclusive with WantAbsent.
	WantPresent bool

	// WantAbsent asserts the pointer resolves to nothing (key absent or
	// null). Mutually exclusive with WantPresent.
	WantAbsent bool

	// WantString asserts the resolved value equals this string. Implies
	// WantPresent.
	WantString string

	// WantBool asserts the resolved value equals *WantBool. Implies
	// WantPresent. Nil means "don't check bool value".
	WantBool *bool

	// WantNumber asserts the resolved value equals *WantNumber. Implies
	// WantPresent. Nil means "don't check number value".
	WantNumber *float64

	// WantArrayLen asserts the resolved array value has exactly this
	// length. Only checked when WantArrayLenSet is true. Use the
	// ArrayLen helper to set both fields together.
	WantArrayLen    int
	WantArrayLenSet bool

	// Label is an optional human-readable description surfaced in
	// failure messages. When empty, Pointer is used as the label.
	Label string
}

// ArrayLen returns a copy of e with WantArrayLen set to n and
// WantArrayLenSet set to true. Implies WantPresent.
func (e FieldExpectation) ArrayLen(n int) FieldExpectation {
	e.WantArrayLen = n
	e.WantArrayLenSet = true
	return e
}

// label returns the display label for the expectation.
func (e FieldExpectation) label() string {
	if e.Label != "" {
		return e.Label
	}
	return e.Pointer
}

// AssertSerialized parses body as JSON and asserts each FieldExpectation
// against it. Failures are reported via t.Errorf (non-fatal) so all
// expectations are checked in a single call.
//
// Example:
//
//	body, _ := adapter.BuildRequestBody(req, prof)
//	wirecheck.AssertSerialized(t, body, []wirecheck.FieldExpectation{
//	    {Pointer: "/tools/0/name", WantString: "read_file"},
//	})
func AssertSerialized(t *testing.T, body []byte, wants []FieldExpectation) {
	t.Helper()
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("wirecheck: cannot unmarshal request body: %v\nbody: %s", err, body)
		return
	}
	for _, want := range wants {
		assertField(t, root, want)
	}
}

// assertField evaluates one FieldExpectation against the parsed JSON
// root and calls t.Errorf on failure.
func assertField(t *testing.T, root any, want FieldExpectation) {
	t.Helper()
	got, ok := jsonPtr(root, want.Pointer)

	if want.WantAbsent {
		if ok && got != nil {
			t.Errorf("wirecheck[%s]: expected absent/null, got %v", want.label(), got)
		}
		return
	}

	needsPresent := want.WantPresent || want.WantString != "" || want.WantBool != nil || want.WantNumber != nil || want.WantArrayLenSet
	if needsPresent {
		if !ok || got == nil {
			t.Errorf("wirecheck[%s]: expected present, got absent/null", want.label())
			return
		}
	}

	if want.WantString != "" {
		s, ok2 := got.(string)
		if !ok2 {
			t.Errorf("wirecheck[%s]: expected string %q, got %T(%v)", want.label(), want.WantString, got, got)
			return
		}
		if s != want.WantString {
			t.Errorf("wirecheck[%s]: expected string %q, got %q", want.label(), want.WantString, s)
		}
		return
	}

	if want.WantBool != nil {
		b, ok2 := got.(bool)
		if !ok2 {
			t.Errorf("wirecheck[%s]: expected bool %v, got %T(%v)", want.label(), *want.WantBool, got, got)
			return
		}
		if b != *want.WantBool {
			t.Errorf("wirecheck[%s]: expected bool %v, got %v", want.label(), *want.WantBool, b)
		}
		return
	}

	if want.WantNumber != nil {
		n, ok2 := got.(float64)
		if !ok2 {
			t.Errorf("wirecheck[%s]: expected number %v, got %T(%v)", want.label(), *want.WantNumber, got, got)
			return
		}
		if n != *want.WantNumber {
			t.Errorf("wirecheck[%s]: expected number %v, got %v", want.label(), *want.WantNumber, n)
		}
		return
	}

	if want.WantArrayLenSet {
		arr, ok2 := got.([]any)
		if !ok2 {
			t.Errorf("wirecheck[%s]: expected array of len %d, got %T(%v)", want.label(), want.WantArrayLen, got, got)
			return
		}
		if len(arr) != want.WantArrayLen {
			t.Errorf("wirecheck[%s]: expected array len %d, got %d", want.label(), want.WantArrayLen, len(arr))
		}
	}
}

// EventExpectation describes one corellm.StreamEvent assertion for
// AssertParsedFromWire.
type EventExpectation struct {
	// Kind must match StreamEvent.Kind (text, tool, reasoning, usage,
	// finish, error). An empty string matches any event kind.
	Kind llm.StreamEventKind

	// Index is the 0-based position in the event slice this expectation
	// applies to. -1 means "find the first matching kind".
	Index int

	// WantText asserts StreamEvent.Text == WantText (non-empty only).
	WantText string

	// WantToolName asserts StreamEvent.Tool.Name == WantToolName.
	WantToolName string

	// WantToolID asserts StreamEvent.Tool.ID == WantToolID.
	WantToolID string

	// WantFinish asserts StreamEvent.Finish == WantFinish.
	WantFinish string

	// WantErrContains asserts StreamEvent.Err contains WantErrContains.
	WantErrContains string

	// Label is an optional description for failure messages.
	Label string
}

// label returns a display label for the expectation.
func (e EventExpectation) label() string {
	if e.Label != "" {
		return e.Label
	}
	if e.Kind != "" {
		return fmt.Sprintf("event[kind=%s,index=%d]", e.Kind, e.Index)
	}
	return fmt.Sprintf("event[index=%d]", e.Index)
}

// AssertParsedFromWire takes a collected slice of StreamEvents (typically
// from draining an adapter's Stream) and asserts each EventExpectation.
//
// Example:
//
//	events := collectEvents(t, stream)
//	wirecheck.AssertParsedFromWire(t, events, []wirecheck.EventExpectation{
//	    {Kind: "tool", Index: -1, WantToolName: "read_file"},
//	    {Kind: "finish", Index: -1, WantFinish: "tool_use"},
//	})
func AssertParsedFromWire(t *testing.T, events []llm.StreamEvent, wants []EventExpectation) {
	t.Helper()
	for _, want := range wants {
		assertEvent(t, events, want)
	}
}

// assertEvent locates the target event and checks expectations.
func assertEvent(t *testing.T, events []llm.StreamEvent, want EventExpectation) {
	t.Helper()
	var ev llm.StreamEvent
	found := false

	if want.Index >= 0 {
		if want.Index >= len(events) {
			t.Errorf("wirecheck[%s]: event index %d out of range (have %d events)", want.label(), want.Index, len(events))
			return
		}
		ev = events[want.Index]
		if want.Kind != "" && ev.Kind != want.Kind {
			t.Errorf("wirecheck[%s]: expected kind %q at index %d, got %q", want.label(), want.Kind, want.Index, ev.Kind)
			return
		}
		found = true
	} else {
		// Find first matching kind.
		for _, e := range events {
			if want.Kind == "" || e.Kind == want.Kind {
				ev = e
				found = true
				break
			}
		}
	}

	if !found {
		t.Errorf("wirecheck[%s]: no event found with kind %q in %d events", want.label(), want.Kind, len(events))
		return
	}

	if want.WantText != "" && ev.Text != want.WantText {
		t.Errorf("wirecheck[%s]: Text = %q, want %q", want.label(), ev.Text, want.WantText)
	}

	if want.WantToolName != "" {
		if ev.Tool == nil {
			t.Errorf("wirecheck[%s]: WantToolName=%q but event.Tool is nil", want.label(), want.WantToolName)
		} else if ev.Tool.Name != want.WantToolName {
			t.Errorf("wirecheck[%s]: Tool.Name = %q, want %q", want.label(), ev.Tool.Name, want.WantToolName)
		}
	}

	if want.WantToolID != "" {
		if ev.Tool == nil {
			t.Errorf("wirecheck[%s]: WantToolID=%q but event.Tool is nil", want.label(), want.WantToolID)
		} else if ev.Tool.ID != want.WantToolID {
			t.Errorf("wirecheck[%s]: Tool.ID = %q, want %q", want.label(), ev.Tool.ID, want.WantToolID)
		}
	}

	if want.WantFinish != "" && ev.Finish != want.WantFinish {
		t.Errorf("wirecheck[%s]: Finish = %q, want %q", want.label(), ev.Finish, want.WantFinish)
	}

	if want.WantErrContains != "" && !strings.Contains(ev.Err, want.WantErrContains) {
		t.Errorf("wirecheck[%s]: Err = %q, want to contain %q", want.label(), ev.Err, want.WantErrContains)
	}
}

// BoolPtr is a convenience helper for building FieldExpectation.WantBool
// values without a named variable.
func BoolPtr(b bool) *bool { return &b }

// Float64Ptr is a convenience helper for building
// FieldExpectation.WantNumber values.
func Float64Ptr(f float64) *float64 { return &f }

// CollectEvents drains a Stream and returns all events as a slice. The
// returned slice is safe to pass to AssertParsedFromWire.
func CollectEvents(t *testing.T, s llm.Stream) []llm.StreamEvent {
	t.Helper()
	var out []llm.StreamEvent
	for ev := range s.Events() {
		out = append(out, ev)
	}
	return out
}

// jsonPtr traverses a parsed JSON value using an RFC 6901 pointer.
// Returns (value, true) when the pointer resolves; ("", false) when any
// segment is absent or the current node is not an object/array.
func jsonPtr(root any, ptr string) (any, bool) {
	if ptr == "" || ptr == "/" {
		return root, true
	}
	// Strip leading "/"
	parts := strings.Split(strings.TrimPrefix(ptr, "/"), "/")
	cur := root
	for _, seg := range parts {
		// Unescape RFC 6901 tokens: "~1" → "/", "~0" → "~"
		seg = strings.ReplaceAll(seg, "~1", "/")
		seg = strings.ReplaceAll(seg, "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx := 0
			for _, c := range seg {
				if c < '0' || c > '9' {
					return nil, false
				}
				idx = idx*10 + int(c-'0')
			}
			if idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}
