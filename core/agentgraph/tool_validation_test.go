package agentgraph

import (
	"strings"
	"testing"
)

// TestValidateToolArgs_WellFormed verifies that valid JSON object args pass.
func TestValidateToolArgs_WellFormed(t *testing.T) {
	tests := []struct {
		name   string
		args   string
		schema []byte
	}{
		{"empty args no schema", "", nil},
		{"empty obj no schema", "{}", nil},
		{"valid args no schema", `{"cmd":"ls"}`, nil},
		{"null args", "null", nil},
		{"valid with required met", `{"path":"/tmp","mode":"read"}`,
			[]byte(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"mode":{"type":"string"}}}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := validateToolArgs(tc.args, "test_tool", tc.schema)
			if msg != "" {
				t.Errorf("expected no validation error, got: %q", msg)
			}
		})
	}
}

// TestValidateToolArgs_NonJSON verifies that non-JSON args return a
// model-readable correction (FR-006: missing/typed-wrong parameter phrasing).
func TestValidateToolArgs_NonJSON(t *testing.T) {
	msg := validateToolArgs("not json at all", "bash", nil)
	if msg == "" {
		t.Fatal("expected validation error for non-JSON args")
	}
	if !strings.Contains(msg, "bash") {
		t.Errorf("error should mention tool name; got: %q", msg)
	}
	if !strings.Contains(msg, "JSON") {
		t.Errorf("error should mention JSON; got: %q", msg)
	}
}

// TestValidateToolArgs_MissingRequired verifies that missing required
// properties produce a model-readable error (FR-006).
func TestValidateToolArgs_MissingRequired(t *testing.T) {
	schema := []byte(`{"type":"object","required":["cmd","timeout"],"properties":{"cmd":{"type":"string"},"timeout":{"type":"number"}}}`)
	// Provide cmd but not timeout.
	msg := validateToolArgs(`{"cmd":"echo hello"}`, "bash", schema)
	if msg == "" {
		t.Fatal("expected validation error for missing required field")
	}
	if !strings.Contains(msg, "timeout") {
		t.Errorf("error should mention missing field 'timeout'; got: %q", msg)
	}
	if !strings.Contains(msg, "bash") {
		t.Errorf("error should mention tool name; got: %q", msg)
	}
}

// TestValidateToolArgs_WrongType verifies that a type mismatch produces
// a model-readable correction (FR-006).
func TestValidateToolArgs_WrongType(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"count":{"type":"integer"}}}`)
	// Provide count as a string instead of a number.
	msg := validateToolArgs(`{"count":"five"}`, "counter", schema)
	if msg == "" {
		t.Fatal("expected type-mismatch validation error")
	}
	if !strings.Contains(msg, "count") {
		t.Errorf("error should mention field name 'count'; got: %q", msg)
	}
	if !strings.Contains(msg, "integer") {
		t.Errorf("error should mention expected type; got: %q", msg)
	}
}

// TestValidateToolArgs_CorrectType verifies that a correctly-typed
// argument does not produce an error.
func TestValidateToolArgs_CorrectType(t *testing.T) {
	schema := []byte(`{"type":"object","properties":{"count":{"type":"number"},"label":{"type":"string"}}}`)
	msg := validateToolArgs(`{"count":42,"label":"hello"}`, "counter", schema)
	if msg != "" {
		t.Errorf("expected no error for correct types, got: %q", msg)
	}
}

// TestValidateToolArgs_UnparseableSchema verifies that a schema we
// cannot parse is silently ignored (defensive degradation).
func TestValidateToolArgs_UnparseableSchema(t *testing.T) {
	msg := validateToolArgs(`{"key":"value"}`, "tool", []byte("not-json"))
	if msg != "" {
		t.Errorf("expected no error when schema is unparseable, got: %q", msg)
	}
}
