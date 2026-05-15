package log

import (
	"encoding/json"
	"testing"
)

// ── table-driven migration chain tests (WP03) ───────────────────────────

// fakeKind is a synthetic event kind used exclusively in schema_test.go
// to exercise the migration registry without colliding with real kind
// registrations (which happen in init() via RegisterKindSchema).
const fakeKindV3 = "test.schema.v3"

func init() {
	// Register a synthetic kind with two migrations (v1→v2, v2→v3) to
	// exercise the full migration chain in tests.
	RegisterKindSchema(fakeKindV3, KindSchema{
		CurrentVersion: 3,
		Migrations: []MigrateFunc{
			// v1 → v2: add "v2_field": true
			func(raw []byte) ([]byte, error) {
				var m map[string]interface{}
				if err := json.Unmarshal(raw, &m); err != nil {
					return nil, err
				}
				m["v2_field"] = true
				return json.Marshal(m)
			},
			// v2 → v3: add "v3_field": 42
			func(raw []byte) ([]byte, error) {
				var m map[string]interface{}
				if err := json.Unmarshal(raw, &m); err != nil {
					return nil, err
				}
				m["v3_field"] = 42
				return json.Marshal(m)
			},
		},
	})
}

func TestSchemaVersion_Constant(t *testing.T) {
	if CurrentSchemaVersion < 1 {
		t.Fatalf("CurrentSchemaVersion must be >= 1, got %d", CurrentSchemaVersion)
	}
}

func TestMigratePayload_UnknownKind_Passthrough(t *testing.T) {
	raw := []byte(`{"x":1}`)
	got, ver, err := MigratePayload("unknown.kind.zzz", 1, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("expected passthrough, got %q", got)
	}
	if ver != 1 {
		t.Errorf("expected version 1, got %d", ver)
	}
}

func TestMigratePayload_AlreadyCurrent_Passthrough(t *testing.T) {
	raw := []byte(`{"already":"current"}`)
	got, ver, err := MigratePayload(fakeKindV3, 3, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(raw) {
		t.Errorf("expected passthrough, got %q", got)
	}
	if ver != 3 {
		t.Errorf("expected version 3, got %d", ver)
	}
}

func TestMigratePayload_V1_To_V3(t *testing.T) {
	raw := []byte(`{"orig":1}`)
	got, ver, err := MigratePayload(fakeKindV3, 1, raw)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if ver != 3 {
		t.Errorf("expected final version 3, got %d", ver)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if m["orig"] == nil {
		t.Error("original field lost after migration")
	}
	if m["v2_field"] != true {
		t.Errorf("v2_field not set: %v", m["v2_field"])
	}
	if v, ok := m["v3_field"].(float64); !ok || v != 42 {
		t.Errorf("v3_field not set correctly: %v", m["v3_field"])
	}
}

func TestMigratePayload_V2_To_V3(t *testing.T) {
	raw := []byte(`{"orig":1,"v2_field":true}`)
	got, ver, err := MigratePayload(fakeKindV3, 2, raw)
	if err != nil {
		t.Fatalf("migration error: %v", err)
	}
	if ver != 3 {
		t.Errorf("expected final version 3, got %d", ver)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if v, ok := m["v3_field"].(float64); !ok || v != 42 {
		t.Errorf("v3_field not set correctly: %v", m["v3_field"])
	}
}

func TestEnsureValidJSON(t *testing.T) {
	if err := EnsureValidJSON([]byte(`{"a":1}`)); err != nil {
		t.Errorf("valid JSON rejected: %v", err)
	}
	if err := EnsureValidJSON([]byte(`not json`)); err == nil {
		t.Error("invalid JSON not rejected")
	}
}
