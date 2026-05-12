package structured_test

// Integration tests for the structured-output pipeline end-to-end.
// Each scenario exercises the public API of the structured package:
// Validate, WithRetry, SchemaHash, and InjectAdditionalProperties.
//
// These are unit-level integration tests — they do NOT make live wire
// calls to any LLM provider. The "integration" label means each test
// exercises multiple components of the structured package together
// rather than in isolation.
//
// Scenarios:
//   1. Happy path — valid response, no retry needed
//   2. First-fail retry — first call returns invalid JSON, second is valid
//   3. Double-fail strict — StrictValidation=true, fails immediately
//   4. Grammar gate — Mode="grammar" path returns ErrUnsupportedFormat
//      (gated at request level by the capability checker)
//   5. Nil ResponseFormat — no-op, free-form text unchanged
//
// (structured-output-and-grammar-01KX5R8A WP06)

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/structured"
)

// ── scenario 1: happy path ─────────────────────────────────────────────────

// TestIntegration_HappyPath verifies that when the first call returns
// a valid JSON response that conforms to the schema, WithRetry returns
// it immediately without invoking retryCall.
func TestIntegration_HappyPath(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"score": {"type": "number"}
		},
		"required": ["name", "score"]
	}`)

	validResponse := []byte(`{"name": "Alice", "score": 9.5}`)
	retryCalled := false

	call := func(_ context.Context) ([]byte, error) {
		return validResponse, nil
	}
	retryCall := func(_ context.Context, hint string) ([]byte, error) {
		retryCalled = true
		return validResponse, nil
	}

	result, err := structured.WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if retryCalled {
		t.Fatal("retryCall should not have been invoked on a valid response")
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if got["name"] != "Alice" {
		t.Fatalf("unexpected name: %v", got["name"])
	}
}

// ── scenario 2: first-fail retry ──────────────────────────────────────────

// TestIntegration_FirstFailRetry verifies that when the first call returns
// invalid JSON and StrictValidation=false, WithRetry invokes retryCall once
// and returns the valid response from the second attempt.
func TestIntegration_FirstFailRetry(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"status": {"type": "string"}},
		"required": ["status"]
	}`)

	firstCall := true
	validResponse := []byte(`{"status": "ok"}`)

	call := func(_ context.Context) ([]byte, error) {
		return []byte(`not json at all`), nil
	}
	retryCall := func(_ context.Context, hint string) ([]byte, error) {
		firstCall = false
		// hint should contain corrective context
		if hint == "" {
			t.Error("retryCall hint should not be empty")
		}
		return validResponse, nil
	}

	result, err := structured.WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if firstCall {
		t.Fatal("retryCall should have been invoked")
	}
	var got map[string]any
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if got["status"] != "ok" {
		t.Fatalf("unexpected status: %v", got["status"])
	}
}

// ── scenario 3: double-fail strict ────────────────────────────────────────

// TestIntegration_DoubleFailStrict verifies that when StrictValidation=true
// and the first call returns invalid output, WithRetry immediately returns
// ErrResponseValidationFailed without invoking retryCall.
func TestIntegration_DoubleFailStrict(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"type": "integer"}},
		"required": ["id"]
	}`)

	retryCalled := false

	call := func(_ context.Context) ([]byte, error) {
		// Returns valid JSON but missing required "id".
		return []byte(`{"not_id": "wrong"}`), nil
	}
	retryCall := func(_ context.Context, hint string) ([]byte, error) {
		retryCalled = true
		return []byte(`{"id": 1}`), nil
	}

	_, err := structured.WithRetry(context.Background(), schema, call, retryCall, "json_schema", true, 1)
	if err == nil {
		t.Fatal("expected ErrResponseValidationFailed, got nil")
	}
	var vf *llm.ErrResponseValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T: %v", err, err)
	}
	if retryCalled {
		t.Fatal("retryCall must not be called in strict mode")
	}
}

// TestIntegration_DoubleFailNonStrict verifies that when StrictValidation=false
// and BOTH calls return invalid output, WithRetry returns ErrResponseValidationFailed.
func TestIntegration_DoubleFailNonStrict(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {"value": {"type": "string"}},
		"required": ["value"]
	}`)

	retryCalled := false

	call := func(_ context.Context) ([]byte, error) {
		return []byte(`{}`), nil // missing "value"
	}
	retryCall := func(_ context.Context, hint string) ([]byte, error) {
		retryCalled = true
		return []byte(`{}`), nil // still wrong
	}

	_, err := structured.WithRetry(context.Background(), schema, call, retryCall, "json_schema", false, 1)
	if err == nil {
		t.Fatal("expected ErrResponseValidationFailed after double fail, got nil")
	}
	var vf *llm.ErrResponseValidationFailed
	if !errors.As(err, &vf) {
		t.Fatalf("expected *llm.ErrResponseValidationFailed, got %T: %v", err, err)
	}
	if !retryCalled {
		t.Fatal("retryCall should have been invoked before double-fail")
	}
}

// ── scenario 4: grammar gate ──────────────────────────────────────────────

// TestIntegration_GrammarGate verifies that a grammar-mode ErrUnsupportedFormat
// is returned correctly from a provider returning the typed error. This test
// does not use WithRetry (WithRetry validates JSON schema; grammar is
// provider-level). Instead it validates the error propagation path.
func TestIntegration_GrammarGate(t *testing.T) {
	// Simulate what an adapter returns for grammar mode on a cloud provider.
	gateFn := func() error {
		return &llm.ErrUnsupportedFormat{
			Provider: "openai",
			Mode:     "grammar",
		}
	}
	err := gateFn()
	if !llm.IsUnsupportedFormat(err) {
		t.Fatalf("expected IsUnsupportedFormat=true, got false for %T: %v", err, err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "grammar") {
		t.Fatalf("error message should mention 'grammar': %q", msg)
	}
}

// ── scenario 5: nil ResponseFormat ────────────────────────────────────────

// TestIntegration_NilResponseFormat verifies that WithRetry with nil schema
// only checks for valid JSON (no schema constraints). A valid JSON response
// passes through unchanged.
func TestIntegration_NilResponseFormat(t *testing.T) {
	// When schema is nil, WithRetry still requires valid JSON but applies no
	// structural constraints. A valid JSON object passes through unchanged.
	rawOutput := []byte(`{"free_form": "any content"}`)

	call := func(_ context.Context) ([]byte, error) {
		return rawOutput, nil
	}
	retryCall := func(_ context.Context, hint string) ([]byte, error) {
		return rawOutput, nil
	}

	// WithRetry with nil schema validates only that the response is valid JSON.
	result, err := structured.WithRetry(context.Background(), nil, call, retryCall, "json_schema", false, 1)
	if err != nil {
		t.Fatalf("valid JSON with nil schema should succeed, got: %v", err)
	}
	if string(result) != string(rawOutput) {
		t.Fatalf("expected raw output %q, got %q", rawOutput, result)
	}
}

// ── schema hash determinism ──────────────────────────────────────────────

// TestIntegration_SchemaHashDeterminism verifies that SchemaHash returns
// identical hashes for byte-identical inputs and different hashes for
// distinct schemas. SchemaHash operates on raw bytes (not canonicalized
// JSON), so callers must normalize before hashing for semantically equivalent
// schemas to match.
func TestIntegration_SchemaHashDeterminism(t *testing.T) {
	s1 := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	// Byte-identical copy — same hash expected.
	s2 := json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`)
	// Different schema — different hash expected.
	s3 := json.RawMessage(`{"type":"object","properties":{"b":{"type":"string"}}}`)

	h1 := structured.SchemaHash(s1)
	h2 := structured.SchemaHash(s2)
	h3 := structured.SchemaHash(s3)

	if h1 == "" {
		t.Fatal("SchemaHash returned empty string")
	}
	if h1 != h2 {
		t.Fatalf("byte-identical schemas should produce the same hash: %q vs %q", h1, h2)
	}
	if h1 == h3 {
		t.Fatalf("distinct schemas should not produce the same hash")
	}

	// Nil schema returns empty string.
	if structured.SchemaHash(nil) != "" {
		t.Fatal("nil schema should return empty hash")
	}
}

// TestIntegration_InjectAdditionalProperties verifies that the injector
// adds "additionalProperties": false to the top-level object schema.
// Note: InjectAdditionalProperties only modifies the top-level object;
// nested object schemas are left unchanged by design (FR-002 / §2.6).
func TestIntegration_InjectAdditionalProperties(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"count": {"type": "integer"}
		}
	}`)

	injected, err := structured.InjectAdditionalProperties(schema)
	if err != nil {
		t.Fatalf("InjectAdditionalProperties error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(injected, &out); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	// Top-level should have additionalProperties=false.
	ap, ok := out["additionalProperties"]
	if !ok {
		t.Fatal("expected additionalProperties at top level")
	}
	if ap != false {
		t.Fatalf("expected additionalProperties=false at top level, got %v", ap)
	}

	// Calling a second time should be idempotent.
	injected2, err := structured.InjectAdditionalProperties(injected)
	if err != nil {
		t.Fatalf("second InjectAdditionalProperties error: %v", err)
	}
	var out2 map[string]any
	if err := json.Unmarshal(injected2, &out2); err != nil {
		t.Fatalf("second result is not valid JSON: %v", err)
	}
	if out2["additionalProperties"] != false {
		t.Fatal("idempotency: additionalProperties should still be false")
	}
	// Should not change a schema that already has additionalProperties=true.
	schemaWithTrue := json.RawMessage(`{"type":"object","additionalProperties":true}`)
	notChanged, err := structured.InjectAdditionalProperties(schemaWithTrue)
	if err != nil {
		t.Fatalf("InjectAdditionalProperties with existing true: %v", err)
	}
	var outTrue map[string]any
	_ = json.Unmarshal(notChanged, &outTrue)
	if outTrue["additionalProperties"] != true {
		t.Fatal("existing additionalProperties=true should be preserved")
	}
}
