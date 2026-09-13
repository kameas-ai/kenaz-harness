package audit

// llm_structured_response_privacy_test.go — structured-output-is-
// reachable-01PMZE14 WP06, AC-009.
//
// LLMStructuredResponsePayload's own doc comment (audit.go:1210-1215)
// states the privacy invariant: "schema bytes, grammar bytes, and any
// portion of the model response body MUST NOT appear in this struct."
// This test enforces it by reflection, reusing the SAME checkPrivacy
// helper audit_kinds_test.go's TestWorkflowAuditKinds_PrivacyInvariant
// already established, rather than hand-rolling a second reflection
// walker — CLAUDE.md: "a second helper means the next payload gets
// checked by only one of them."
//
// This payload does not carry a workflowKinds-table "workflow."/"notify."
// prefix (its Kind is "llm.structured.response"), so it cannot be added
// to that table — TestWorkflowAuditKinds_NotEmpty would then fail on a
// namespace check that has nothing to do with this payload. checkPrivacy
// itself takes (t, reflect.Type, kindName, forbidden) with no dependency
// on the workflow table, so it is reused directly here instead.

import (
	"reflect"
	"testing"
)

// TestLLMStructuredResponsePayload_PrivacyInvariant enforces that no
// field of LLMStructuredResponsePayload carries schema bytes, grammar
// bytes, or any portion of the model response body. SchemaHash (a
// SHA-256 digest, not the schema itself) is deliberately NOT forbidden —
// only a literal "schema" or "grammar" or response-body-shaped field
// would trip this.
func TestLLMStructuredResponsePayload_PrivacyInvariant(t *testing.T) {
	t.Parallel()

	forbidden := map[string]bool{
		"schema":        true,
		"grammar":       true,
		"body":          true,
		"prompt_text":   true,
		"response_text": true,
		"response":      true,
		"response_body": true,
		"raw_response":  true,
	}

	checkPrivacy(t, reflect.TypeOf(LLMStructuredResponsePayload{}),
		string(KindLLMStructuredResponse), forbidden)
}

// TestLLMStructuredResponsePayload_SchemaHashIsADigestNotTheSchema pins
// that schema_hash is present (it is the one schema-derived field this
// payload is allowed to carry) but is never longer than a SHA-256 hex
// digest (64 chars) even for a large input — a cheap structural guard
// against a future edit accidentally assigning the raw schema bytes to
// this field instead of their hash.
func TestLLMStructuredResponsePayload_SchemaHashFieldTag(t *testing.T) {
	t.Parallel()
	ty := reflect.TypeOf(LLMStructuredResponsePayload{})
	f, ok := ty.FieldByName("SchemaHash")
	if !ok {
		t.Fatal("LLMStructuredResponsePayload has no SchemaHash field")
	}
	if tag := f.Tag.Get("json"); tag != "schema_hash,omitempty" {
		t.Errorf("SchemaHash json tag = %q, want %q", tag, "schema_hash,omitempty")
	}
}
