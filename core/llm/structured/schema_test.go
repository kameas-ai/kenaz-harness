package structured

import (
	"encoding/json"
	"testing"
)

func TestSchemaHash_Deterministic(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","required":["name"]}`)
	h1 := SchemaHash(schema)
	h2 := SchemaHash(schema)
	if h1 != h2 {
		t.Fatalf("SchemaHash not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("SchemaHash length = %d, want 64 (SHA-256 hex)", len(h1))
	}
}

func TestSchemaHash_NilOrEmpty(t *testing.T) {
	if got := SchemaHash(nil); got != "" {
		t.Fatalf("SchemaHash(nil) = %q, want empty", got)
	}
	if got := SchemaHash(json.RawMessage{}); got != "" {
		t.Fatalf("SchemaHash(empty) = %q, want empty", got)
	}
}

func TestSchemaHash_DifferentSchemas(t *testing.T) {
	h1 := SchemaHash(json.RawMessage(`{"type":"string"}`))
	h2 := SchemaHash(json.RawMessage(`{"type":"number"}`))
	if h1 == h2 {
		t.Fatal("different schemas should have different hashes")
	}
}

func TestInjectAdditionalProperties_InjectsWhenAbsent(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	out, err := InjectAdditionalProperties(schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if v, ok := doc["additionalProperties"]; !ok {
		t.Fatal("additionalProperties not injected")
	} else if v != false {
		t.Fatalf("additionalProperties = %v, want false", v)
	}
}

func TestInjectAdditionalProperties_PreservesExistingValue(t *testing.T) {
	// When additionalProperties is already set (true or false), it must not be overridden.
	schema := json.RawMessage(`{"type":"object","additionalProperties":true}`)
	out, err := InjectAdditionalProperties(schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if v, ok := doc["additionalProperties"]; !ok || v != true {
		t.Fatalf("additionalProperties = %v, want true (preserved)", v)
	}
}

func TestInjectAdditionalProperties_NilOrEmpty(t *testing.T) {
	out, err := InjectAdditionalProperties(nil)
	if err != nil {
		t.Fatalf("unexpected error for nil: %v", err)
	}
	if out != nil {
		t.Fatalf("nil input should return nil, got %s", out)
	}
}
