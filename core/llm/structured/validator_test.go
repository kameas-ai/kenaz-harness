package structured

import (
	"encoding/json"
	"testing"
)

func TestValidate_NilSchema_AcceptsAnyJSON(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool // true = valid (nil error)
	}{
		{"object", `{"foo":"bar"}`, true},
		{"array", `[1,2,3]`, true},
		{"string", `"hello"`, true},
		{"number", `42`, true},
		{"null", `null`, true},
		{"invalid_json", `{bad}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Validate(nil, []byte(tc.data))
			if tc.want && got != nil {
				t.Fatalf("expected nil err, got %v", got)
			}
			if !tc.want && got == nil {
				t.Fatalf("expected non-nil err for invalid JSON")
			}
		})
	}
}

func TestValidate_TypeChecks(t *testing.T) {
	cases := []struct {
		name    string
		schema  string
		data    string
		wantErr bool
	}{
		{"string_ok", `{"type":"string"}`, `"hello"`, false},
		{"string_fail", `{"type":"string"}`, `42`, true},
		{"number_ok", `{"type":"number"}`, `3.14`, false},
		{"number_fail", `{"type":"number"}`, `"pi"`, true},
		{"integer_ok", `{"type":"integer"}`, `7`, false},
		{"integer_fail", `{"type":"integer"}`, `3.5`, true},
		{"boolean_ok", `{"type":"boolean"}`, `true`, false},
		{"boolean_fail", `{"type":"boolean"}`, `"yes"`, true},
		{"null_ok", `{"type":"null"}`, `null`, false},
		{"null_fail", `{"type":"null"}`, `0`, true},
		{"object_ok", `{"type":"object"}`, `{"a":1}`, false},
		{"array_ok", `{"type":"array"}`, `[1,2]`, false},
		{"multi_type_ok", `{"type":["string","null"]}`, `null`, false},
		{"multi_type_ok2", `{"type":["string","null"]}`, `"hi"`, false},
		{"multi_type_fail", `{"type":["string","null"]}`, `42`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Validate(json.RawMessage(tc.schema), []byte(tc.data))
			if tc.wantErr && got == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && got != nil {
				t.Fatalf("unexpected error: %v", got)
			}
		})
	}
}

func TestValidate_Required(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "number"}
		},
		"required": ["name", "age"]
	}`)

	t.Run("all_required_present", func(t *testing.T) {
		if got := Validate(schema, []byte(`{"name":"Alice","age":30}`)); got != nil {
			t.Fatalf("unexpected error: %v", got)
		}
	})
	t.Run("missing_required", func(t *testing.T) {
		got := Validate(schema, []byte(`{"name":"Alice"}`))
		if got == nil {
			t.Fatal("expected error for missing required field")
		}
		if got.Field == "" {
			t.Fatal("expected non-empty Field in ValidationError")
		}
	})
}

func TestValidate_Enum(t *testing.T) {
	schema := json.RawMessage(`{"enum":["low","medium","high"]}`)
	if got := Validate(schema, []byte(`"low"`)); got != nil {
		t.Fatalf("unexpected error for valid enum: %v", got)
	}
	if got := Validate(schema, []byte(`"critical"`)); got == nil {
		t.Fatal("expected error for invalid enum value")
	}
}

func TestValidate_NestedObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"summary": {"type": "string"},
			"risk": {"type": "string", "enum": ["low","medium","high"]},
			"meta": {
				"type": "object",
				"properties": {"score": {"type": "number"}},
				"required": ["score"]
			}
		},
		"required": ["summary", "risk"]
	}`)

	t.Run("nested_ok", func(t *testing.T) {
		data := []byte(`{"summary":"test","risk":"low","meta":{"score":9.5}}`)
		if got := Validate(schema, data); got != nil {
			t.Fatalf("unexpected error: %v", got)
		}
	})
	t.Run("nested_wrong_type", func(t *testing.T) {
		data := []byte(`{"summary":"test","risk":"low","meta":{"score":"bad"}}`)
		if got := Validate(schema, data); got == nil {
			t.Fatal("expected error for nested wrong type")
		}
	})
	t.Run("nested_missing_required", func(t *testing.T) {
		data := []byte(`{"summary":"test","risk":"low","meta":{}}`)
		if got := Validate(schema, data); got == nil {
			t.Fatal("expected error for nested missing required field")
		}
	})
}

func TestValidate_Items(t *testing.T) {
	schema := json.RawMessage(`{"type":"array","items":{"type":"string"}}`)
	if got := Validate(schema, []byte(`["a","b","c"]`)); got != nil {
		t.Fatalf("unexpected error: %v", got)
	}
	if got := Validate(schema, []byte(`["a",42,"c"]`)); got == nil {
		t.Fatal("expected error for wrong item type")
	}
}

// TestValidate_UnknownKeywords verifies that unsupported schema keywords
// are silently accepted (pass-through, not rejected).
func TestValidate_UnknownKeywords(t *testing.T) {
	// $ref, allOf, $schema, and other unsupported keywords must not cause failure
	schema := json.RawMessage(`{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"],
		"$comment": "test schema",
		"title": "My Schema"
	}`)
	data := []byte(`{"name":"Alice"}`)
	if got := Validate(schema, data); got != nil {
		t.Fatalf("unknown keywords caused validation failure: %v", got)
	}
}

func TestValidate_AdditionalProperties_False(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{"name":{"type":"string"}},
		"additionalProperties":false
	}`)
	t.Run("ok", func(t *testing.T) {
		if got := Validate(schema, []byte(`{"name":"Alice"}`)); got != nil {
			t.Fatalf("unexpected error: %v", got)
		}
	})
	t.Run("extra_field", func(t *testing.T) {
		if got := Validate(schema, []byte(`{"name":"Alice","extra":"field"}`)); got == nil {
			t.Fatal("expected error for additional property")
		}
	})
}

func TestValidate_MinMaxLength(t *testing.T) {
	schema := json.RawMessage(`{"type":"string","minLength":3,"maxLength":10}`)
	if got := Validate(schema, []byte(`"hello"`)); got != nil {
		t.Fatalf("unexpected error: %v", got)
	}
	if got := Validate(schema, []byte(`"hi"`)); got == nil {
		t.Fatal("expected error for too short string")
	}
	if got := Validate(schema, []byte(`"this is too long string"`)); got == nil {
		t.Fatal("expected error for too long string")
	}
}

func TestValidate_MinMaxNumber(t *testing.T) {
	schema := json.RawMessage(`{"type":"number","minimum":0,"maximum":100}`)
	if got := Validate(schema, []byte(`50`)); got != nil {
		t.Fatalf("unexpected error: %v", got)
	}
	if got := Validate(schema, []byte(`-1`)); got == nil {
		t.Fatal("expected error for below minimum")
	}
	if got := Validate(schema, []byte(`101`)); got == nil {
		t.Fatal("expected error for above maximum")
	}
}
