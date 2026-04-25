// Package schema declares the canonical YAML/JSON document shape for
// a PolicyArtifact and provides round-trip parsing helpers. The
// schema is the operator-facing authoring surface: each clause has a
// stable shape regardless of control kind, and per-kind validation is
// dispatched to the registered ControlKind.ValidateParams.
//
// Architectural integrity: this package contains *no* control-kind-
// specific logic (DIRECTIVE_001 / spec C-001). Per-kind logic lives
// in core/policy/clauses/<kind>/.
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/sigil-tech/kaneaz-harness/core/policy"
)

// Document is the canonical YAML/JSON document the lowering pipeline
// (and the bundle handler in WP05) parses. The shape mirrors
// data-model.md §PolicyArtifact verbatim.
type Document struct {
	APIVersion string         `json:"api_version" yaml:"api_version"`
	Kind       string         `json:"kind"        yaml:"kind"`
	PolicyID   string         `json:"policy_id"   yaml:"policy_id"`
	Name       string         `json:"name"        yaml:"name"`
	Version    string         `json:"version"     yaml:"version"`
	Layer      string         `json:"layer"       yaml:"layer"`
	NotBefore  *time.Time     `json:"not_before,omitempty" yaml:"not_before,omitempty"`
	NotAfter   *time.Time     `json:"not_after,omitempty"  yaml:"not_after,omitempty"`
	Clauses    []ClauseDoc    `json:"clauses"     yaml:"clauses"`
	Metadata   map[string]any `json:"metadata,omitempty"   yaml:"metadata,omitempty"`
}

// ClauseDoc is one clause in document form.
type ClauseDoc struct {
	ClauseID       string         `json:"clause_id"       yaml:"clause_id"`
	Kind           string         `json:"kind"            yaml:"kind"`
	Params         map[string]any `json:"params"          yaml:"params"`
	FailurePosture string         `json:"failure_posture,omitempty" yaml:"failure_posture,omitempty"`
}

// CurrentAPIVersion is the api_version every v1 PolicyArtifact must
// declare.
const (
	CurrentAPIVersion = "kaneaz.policy/v1"
	CurrentKind       = "policy"
)

// Errors returned by Parse / ToArtifact.
var (
	ErrEmptyDocument         = errors.New("schema: empty document")
	ErrAPIVersionUnsupported = errors.New("schema: unsupported api_version")
	ErrKindMismatch          = errors.New("schema: document kind is not 'policy'")
	ErrLayerInvalid          = errors.New("schema: layer must be one of org|team|personal")
	ErrPolicyIDRequired      = errors.New("schema: policy_id is required")
	ErrNameRequired          = errors.New("schema: name is required")
	ErrVersionRequired       = errors.New("schema: version is required")
	ErrClausesRequired       = errors.New("schema: at least one clause is required")
)

// ParseYAML parses a YAML body into a Document. The decoder is
// strict: unknown top-level fields fail loudly so operators see
// typos rather than silent drops (FR-017 spirit at the schema layer).
func ParseYAML(body []byte) (Document, error) {
	if len(body) == 0 {
		return Document{}, ErrEmptyDocument
	}
	var d Document
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("schema: yaml decode: %w", err)
	}
	return d, nil
}

// ParseJSON parses a JSON body into a Document.
func ParseJSON(body []byte) (Document, error) {
	if len(body) == 0 {
		return Document{}, ErrEmptyDocument
	}
	var d Document
	if err := json.Unmarshal(body, &d); err != nil {
		return Document{}, fmt.Errorf("schema: json decode: %w", err)
	}
	return d, nil
}

// MarshalYAML returns the canonical YAML form of the document.
func (d Document) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d)
}

// ToArtifact converts the document to a policy.PolicyArtifact after
// schema-level validation. Per-clause kind validation happens in the
// caller (engine.Reload or bundle handler) by dispatching to the
// registered ControlKind.ValidateParams.
func (d Document) ToArtifact() (policy.PolicyArtifact, error) {
	if d.APIVersion != CurrentAPIVersion {
		return policy.PolicyArtifact{}, fmt.Errorf("%w: %q", ErrAPIVersionUnsupported, d.APIVersion)
	}
	if d.Kind != CurrentKind {
		return policy.PolicyArtifact{}, fmt.Errorf("%w: %q", ErrKindMismatch, d.Kind)
	}
	if d.PolicyID == "" {
		return policy.PolicyArtifact{}, ErrPolicyIDRequired
	}
	if d.Name == "" {
		return policy.PolicyArtifact{}, ErrNameRequired
	}
	if d.Version == "" {
		return policy.PolicyArtifact{}, ErrVersionRequired
	}
	layer := policy.Layer(d.Layer)
	if !layer.IsKnown() {
		return policy.PolicyArtifact{}, fmt.Errorf("%w: %q", ErrLayerInvalid, d.Layer)
	}
	if len(d.Clauses) == 0 {
		return policy.PolicyArtifact{}, ErrClausesRequired
	}
	clauses := make([]policy.Clause, 0, len(d.Clauses))
	seen := make(map[string]bool, len(d.Clauses))
	for i, c := range d.Clauses {
		if c.ClauseID == "" {
			return policy.PolicyArtifact{}, fmt.Errorf("schema: clause[%d].clause_id is required", i)
		}
		if seen[c.ClauseID] {
			return policy.PolicyArtifact{}, fmt.Errorf("schema: duplicate clause_id %q", c.ClauseID)
		}
		seen[c.ClauseID] = true
		if c.Kind == "" {
			return policy.PolicyArtifact{}, fmt.Errorf("schema: clause[%d].kind is required", i)
		}
		posture := policy.FailurePosture(c.FailurePosture)
		switch posture {
		case "", policy.FailClosed, policy.FailOpen:
			// ok; "" defaults to FailClosed via Clause.EffectivePosture.
		default:
			return policy.PolicyArtifact{}, fmt.Errorf(
				"schema: clause %q has invalid failure_posture %q",
				c.ClauseID, c.FailurePosture,
			)
		}
		clauses = append(clauses, policy.Clause{
			ClauseID:       c.ClauseID,
			Kind:           c.Kind,
			Params:         c.Params,
			FailurePosture: posture,
		})
	}
	pa := policy.PolicyArtifact{
		PolicyID:  d.PolicyID,
		Name:      d.Name,
		Version:   d.Version,
		Layer:     layer,
		Clauses:   clauses,
		NotBefore: d.NotBefore,
		NotAfter:  d.NotAfter,
	}
	pa.ContentHash = policy.ContentHashOf(pa)
	return pa, nil
}

// FromArtifact converts a PolicyArtifact back to its document shape.
// Round-trip helper.
func FromArtifact(a policy.PolicyArtifact) Document {
	clauses := make([]ClauseDoc, 0, len(a.Clauses))
	for _, c := range a.Clauses {
		clauses = append(clauses, ClauseDoc{
			ClauseID:       c.ClauseID,
			Kind:           c.Kind,
			Params:         c.Params,
			FailurePosture: string(c.FailurePosture),
		})
	}
	return Document{
		APIVersion: CurrentAPIVersion,
		Kind:       CurrentKind,
		PolicyID:   a.PolicyID,
		Name:       a.Name,
		Version:    a.Version,
		Layer:      string(a.Layer),
		NotBefore:  a.NotBefore,
		NotAfter:   a.NotAfter,
		Clauses:    clauses,
	}
}

// JSONSchemaDocument is the JSON Schema (draft 2020-12) describing
// the canonical document. Authoring tools and CI hooks consume it
// for editor-side validation. The schema intentionally stays
// permissive on the per-clause `params` field: the registered
// ControlKind owns its own param shape.
const JSONSchemaDocument = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://kaneaz.io/schemas/policy/v1.json",
  "type": "object",
  "required": ["api_version", "kind", "policy_id", "name", "version", "layer", "clauses"],
  "properties": {
    "api_version": {"const": "kaneaz.policy/v1"},
    "kind":        {"const": "policy"},
    "policy_id":   {"type": "string", "minLength": 1},
    "name":        {"type": "string", "minLength": 1},
    "version":     {"type": "string", "minLength": 1},
    "layer":       {"enum": ["org", "team", "personal"]},
    "not_before":  {"type": "string", "format": "date-time"},
    "not_after":   {"type": "string", "format": "date-time"},
    "metadata":    {"type": "object"},
    "clauses": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "required": ["clause_id", "kind", "params"],
        "properties": {
          "clause_id":       {"type": "string", "minLength": 1},
          "kind":            {"type": "string", "minLength": 1},
          "params":          {"type": "object"},
          "failure_posture": {"enum": ["fail_closed", "fail_open"]}
        }
      }
    }
  }
}`
