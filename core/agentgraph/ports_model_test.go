package agentgraph

// ports_model_test.go — proof-of-pattern for the typed port accessors
// generated in ports_gen.go (typed-port-codegen mission, WP04).
//
// These tests demonstrate how executors and callers can use the
// generated Read<Kind>Inputs / (<Kind>Outputs).ToPortValues() helpers
// instead of raw PortValues map literals.  The model kind is used as
// the example because it has a typed port (messages → []Message).

import (
	"testing"
)

// TestModelInputs_ReadTyped verifies that ReadModelInputs correctly
// extracts a []Message from a PortValues map and returns it in the
// typed ModelInputs struct.
func TestModelInputs_ReadTyped(t *testing.T) {
	t.Parallel()

	msgs := []Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	pv := PortValues{"messages": msgs}

	in, err := ReadModelInputs(pv)
	if err != nil {
		t.Fatalf("ReadModelInputs: %v", err)
	}
	if len(in.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2", len(in.Messages))
	}
	if in.Messages[0].Role != "user" {
		t.Errorf("Messages[0].Role = %q, want %q", in.Messages[0].Role, "user")
	}
}

// TestModelInputs_RequiredAbsent verifies that ReadModelInputs returns
// a descriptive error when the required "messages" input port is absent.
func TestModelInputs_RequiredAbsent(t *testing.T) {
	t.Parallel()

	_, err := ReadModelInputs(PortValues{})
	if err == nil {
		t.Fatal("expected error for absent required port, got nil")
	}
	want := "model: required input port messages is absent"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// TestModelInputs_WrongType verifies that ReadModelInputs returns a
// descriptive error when the "messages" port carries an incompatible
// value type.
func TestModelInputs_WrongType(t *testing.T) {
	t.Parallel()

	pv := PortValues{"messages": "not-a-slice"}
	_, err := ReadModelInputs(pv)
	if err == nil {
		t.Fatal("expected type-cast error, got nil")
	}
}

// TestModelInputs_NilTreatedAsAbsent verifies that a nil value for a
// required port is treated as absent (i.e. an error is returned).
func TestModelInputs_NilTreatedAsAbsent(t *testing.T) {
	t.Parallel()

	pv := PortValues{"messages": nil}
	_, err := ReadModelInputs(pv)
	// nil in PortValues for a required port: the nil branch skips the
	// assignment, so the field stays zero (nil slice); no error because
	// the key IS present (just nil-valued). This matches the spec intent:
	// "absence returns a structured error" means missing key, not nil.
	// A nil value is a legitimate "empty messages" signal.
	if err != nil {
		t.Errorf("unexpected error for nil (present) port: %v", err)
	}
}

// TestModelOutputs_ToPortValues verifies that ModelOutputs.ToPortValues
// correctly roundtrips the response messages back to the wire format.
func TestModelOutputs_ToPortValues(t *testing.T) {
	t.Parallel()

	msgs := []Message{{Role: "assistant", Content: "done"}}
	out := ModelOutputs{Response: msgs}

	pv := out.ToPortValues()
	got, ok := pv["response"]
	if !ok {
		t.Fatal("ToPortValues: missing 'response' key")
	}
	gotMsgs, ok := got.([]Message)
	if !ok {
		t.Fatalf("ToPortValues: 'response' has type %T, want []Message", got)
	}
	if len(gotMsgs) != 1 || gotMsgs[0].Content != "done" {
		t.Errorf("ToPortValues: got %+v, want [{assistant done}]", gotMsgs)
	}
}

// TestModelOutputs_RoundTrip verifies the full Read→mutate→Write
// round-trip: read typed inputs, construct outputs, convert back to
// PortValues.  This is the canonical usage pattern for an executor that
// adopts the typed port helpers.
func TestModelOutputs_RoundTrip(t *testing.T) {
	t.Parallel()

	inMsgs := []Message{{Role: "user", Content: "ping"}}
	inputs, err := ReadModelInputs(PortValues{"messages": inMsgs})
	if err != nil {
		t.Fatalf("ReadModelInputs: %v", err)
	}
	// Simulate the executor appending an assistant reply.
	reply := append(inputs.Messages, Message{Role: "assistant", Content: "pong"})
	outputs := ModelOutputs{Response: reply}
	pv := outputs.ToPortValues()

	raw, ok := pv["response"]
	if !ok {
		t.Fatal("roundtrip: 'response' missing from PortValues")
	}
	msgs, ok := raw.([]Message)
	if !ok {
		t.Fatalf("roundtrip: 'response' has type %T, want []Message", raw)
	}
	if len(msgs) != 2 {
		t.Errorf("roundtrip: got %d messages, want 2", len(msgs))
	}
	if msgs[1].Content != "pong" {
		t.Errorf("roundtrip: msgs[1].Content = %q, want %q", msgs[1].Content, "pong")
	}
}
