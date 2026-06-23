package schema_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy"
	"github.com/kameas-ai/kenaz-harness/core/policy/lower/schema"
)

const goldenYAML = `api_version: kenaz.policy/v1
kind: policy
policy_id: 01HZX8VR0YJ91A2C3D4E5F6G7H
name: org-baseline
version: 1.0.0
layer: org
clauses:
  - clause_id: provider-allowlist
    kind: provider_allowlist
    params:
      allow:
        - anthropic
        - openrouter
  - clause_id: cost-ceiling-day
    kind: cost_ceiling
    params:
      window: per_day
      currency: USD
      max: 25.00
    failure_posture: fail_closed
`

func TestParseYAMLRoundTrip(t *testing.T) {
	d, err := schema.ParseYAML([]byte(goldenYAML))
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	if d.APIVersion != schema.CurrentAPIVersion {
		t.Fatalf("APIVersion = %q, want %q", d.APIVersion, schema.CurrentAPIVersion)
	}
	if d.PolicyID != "01HZX8VR0YJ91A2C3D4E5F6G7H" {
		t.Fatalf("PolicyID mismatch: %q", d.PolicyID)
	}
	if len(d.Clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(d.Clauses))
	}
	pa, err := d.ToArtifact()
	if err != nil {
		t.Fatalf("ToArtifact: %v", err)
	}
	if pa.Layer != policy.LayerOrg {
		t.Fatalf("Layer = %q, want org", pa.Layer)
	}
	if pa.ContentHash == "" {
		t.Fatalf("ContentHash should be filled by ToArtifact")
	}

	// Round-trip through FromArtifact + Marshal.
	d2 := schema.FromArtifact(pa)
	out, err := d2.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	d3, err := schema.ParseYAML(out)
	if err != nil {
		t.Fatalf("ParseYAML round-trip: %v", err)
	}
	pa2, err := d3.ToArtifact()
	if err != nil {
		t.Fatalf("ToArtifact round-trip: %v", err)
	}
	if pa.ContentHash != pa2.ContentHash {
		t.Fatalf("content hash drift on round-trip\n  pre=%s\n post=%s", pa.ContentHash, pa2.ContentHash)
	}
}

func TestParseYAMLRejectsUnknownTopLevelField(t *testing.T) {
	body := goldenYAML + "\nbogus_field: 42\n"
	_, err := schema.ParseYAML([]byte(body))
	if err == nil {
		t.Fatalf("expected unknown-field error")
	}
}

func TestParseYAMLEmptyDocument(t *testing.T) {
	if _, err := schema.ParseYAML(nil); !errors.Is(err, schema.ErrEmptyDocument) {
		t.Fatalf("expected ErrEmptyDocument, got %v", err)
	}
}

func TestToArtifactRejectsBadAPIVersion(t *testing.T) {
	d := schema.Document{
		APIVersion: "kenaz.policy/v0",
		Kind:       "policy",
		PolicyID:   "p1",
		Name:       "n",
		Version:    "1",
		Layer:      "org",
		Clauses:    []schema.ClauseDoc{{ClauseID: "c", Kind: "k", Params: map[string]any{}}},
	}
	_, err := d.ToArtifact()
	if !errors.Is(err, schema.ErrAPIVersionUnsupported) {
		t.Fatalf("expected ErrAPIVersionUnsupported, got %v", err)
	}
}

func TestToArtifactRejectsBadLayer(t *testing.T) {
	d := schema.Document{
		APIVersion: schema.CurrentAPIVersion,
		Kind:       "policy",
		PolicyID:   "p1",
		Name:       "n",
		Version:    "1",
		Layer:      "department",
		Clauses:    []schema.ClauseDoc{{ClauseID: "c", Kind: "k", Params: map[string]any{}}},
	}
	_, err := d.ToArtifact()
	if !errors.Is(err, schema.ErrLayerInvalid) {
		t.Fatalf("expected ErrLayerInvalid, got %v", err)
	}
}

func TestToArtifactRequiresClauses(t *testing.T) {
	d := schema.Document{
		APIVersion: schema.CurrentAPIVersion,
		Kind:       "policy",
		PolicyID:   "p1",
		Name:       "n",
		Version:    "1",
		Layer:      "org",
	}
	_, err := d.ToArtifact()
	if !errors.Is(err, schema.ErrClausesRequired) {
		t.Fatalf("expected ErrClausesRequired, got %v", err)
	}
}

func TestToArtifactRejectsDuplicateClauseID(t *testing.T) {
	d := schema.Document{
		APIVersion: schema.CurrentAPIVersion,
		Kind:       "policy",
		PolicyID:   "p1",
		Name:       "n",
		Version:    "1",
		Layer:      "org",
		Clauses: []schema.ClauseDoc{
			{ClauseID: "x", Kind: "k", Params: map[string]any{}},
			{ClauseID: "x", Kind: "k", Params: map[string]any{}},
		},
	}
	_, err := d.ToArtifact()
	if err == nil || !strings.Contains(err.Error(), "duplicate clause_id") {
		t.Fatalf("expected duplicate-clause error, got %v", err)
	}
}

func TestToArtifactRejectsBadFailurePosture(t *testing.T) {
	d := schema.Document{
		APIVersion: schema.CurrentAPIVersion,
		Kind:       "policy",
		PolicyID:   "p1",
		Name:       "n",
		Version:    "1",
		Layer:      "org",
		Clauses: []schema.ClauseDoc{{
			ClauseID:       "c",
			Kind:           "k",
			Params:         map[string]any{},
			FailurePosture: "maybe_closed",
		}},
	}
	_, err := d.ToArtifact()
	if err == nil || !strings.Contains(err.Error(), "invalid failure_posture") {
		t.Fatalf("expected invalid failure_posture error, got %v", err)
	}
}

func TestParseJSON(t *testing.T) {
	body := `{
  "api_version": "kenaz.policy/v1",
  "kind": "policy",
  "policy_id": "p",
  "name": "n",
  "version": "1",
  "layer": "team",
  "clauses": [
    {"clause_id": "c1", "kind": "k", "params": {}}
  ]
}`
	d, err := schema.ParseJSON([]byte(body))
	if err != nil {
		t.Fatalf("ParseJSON: %v", err)
	}
	pa, err := d.ToArtifact()
	if err != nil {
		t.Fatalf("ToArtifact: %v", err)
	}
	if pa.Layer != policy.LayerTeam {
		t.Fatalf("Layer = %q, want team", pa.Layer)
	}
}

func TestJSONSchemaDocumentNonEmpty(t *testing.T) {
	if !strings.Contains(schema.JSONSchemaDocument, "kenaz.policy/v1") {
		t.Fatalf("JSONSchemaDocument should reference api_version")
	}
}
