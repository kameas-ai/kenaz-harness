package nodes_test

import (
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
	"gopkg.in/yaml.v3"
)

// TestManifestRoundTrip asserts a hand-written archetype manifest YAML
// parses into the expected Go shape and re-marshals back to a
// semantically-equivalent document. The round-trip catches any field-
// name drift between the YAML schema (FR-002 / FR-003) and the Go
// `Manifest` struct.
func TestManifestRoundTrip(t *testing.T) {
	t.Parallel()
	src := `schema_version: "1"
archetype: compute
category: compute
description: "Reasoning/generation work that consumes LLM/tool budget."
callable: false
ports:
  inputs:
    - {name: input, type: messages}
  outputs:
    - {name: output, type: messages}
attrs:
  provider:
    type: string
  model:
    type: string
  max_tokens:
    type: int
    min: 0
  temperature:
    type: float
    min: 0.0
    max: 2.0
budget: llm
budget_consumes: [tokens, llm_calls, cost_usd]
`
	var m nodes.Manifest
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Archetype != "compute" {
		t.Errorf("archetype: got %q, want %q", m.Archetype, "compute")
	}
	if m.Category != nodes.CategoryCompute {
		t.Errorf("category: got %q, want %q", m.Category, nodes.CategoryCompute)
	}
	if m.IsCallable() {
		t.Error("expected archetype to be non-callable")
	}
	if m.Budget != nodes.BudgetLLM {
		t.Errorf("budget: got %q", m.Budget)
	}
	if got, want := len(m.Ports.Inputs), 1; got != want {
		t.Fatalf("inputs: got %d, want %d", got, want)
	}
	if m.Ports.Inputs[0].Name != "input" || m.Ports.Inputs[0].Type != "messages" {
		t.Errorf("inputs[0]: got %+v", m.Ports.Inputs[0])
	}
	if got, want := len(m.Attrs), 4; got != want {
		t.Fatalf("attrs: got %d, want %d", got, want)
	}
	temp := m.Attrs["temperature"]
	if temp.Type != nodes.AttrTypeFloat {
		t.Errorf("temperature.type: got %q, want %q", temp.Type, nodes.AttrTypeFloat)
	}
	if temp.Min == nil || *temp.Min != 0 {
		t.Errorf("temperature.min: got %v, want 0", temp.Min)
	}
	if temp.Max == nil || *temp.Max != 2 {
		t.Errorf("temperature.max: got %v, want 2", temp.Max)
	}
	if got, want := len(m.BudgetConsumes), 3; got != want {
		t.Fatalf("budget_consumes: got %d, want %d", got, want)
	}

	// Re-marshal and assert key fields survive.
	out, err := yaml.Marshal(&m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// We don't compare byte-for-byte; we assert the round-trip
	// preserves the load-bearing fields.
	if !strings.Contains(string(out), "archetype: compute") {
		t.Errorf("round-trip lost archetype: %s", out)
	}
	if !strings.Contains(string(out), "category: compute") {
		t.Errorf("round-trip lost category: %s", out)
	}
	if !strings.Contains(string(out), "budget: llm") {
		t.Errorf("round-trip lost budget: %s", out)
	}
}

// TestAttrTypeIsKnown ensures every documented AttrType is recognised
// by IsKnown(). Catches drift if a new type is added to the const block
// but forgotten from AllAttrTypes.
func TestAttrTypeIsKnown(t *testing.T) {
	t.Parallel()
	got := map[nodes.AttrType]bool{}
	for _, tt := range nodes.AllAttrTypes() {
		if got[tt] {
			t.Errorf("AttrType %q listed twice in AllAttrTypes", tt)
		}
		got[tt] = true
		if !tt.IsKnown() {
			t.Errorf("AttrType %q not recognised by IsKnown", tt)
		}
	}
	if nodes.AttrType("integer").IsKnown() {
		t.Errorf("AttrType 'integer' should not be known (it's a typo for 'int')")
	}
}

// TestCategoryIsKnown checks the three built-in categories.
func TestCategoryIsKnown(t *testing.T) {
	t.Parallel()
	for _, c := range nodes.AllCategories() {
		if !c.IsKnown() {
			t.Errorf("Category %q not recognised", c)
		}
	}
	if nodes.Category("data").IsKnown() {
		t.Error("Category 'data' should not be known")
	}
}

// TestBudgetIsKnown checks the four budget signatures.
func TestBudgetIsKnown(t *testing.T) {
	t.Parallel()
	for _, b := range nodes.AllBudgetSignatures() {
		if !b.IsKnown() {
			t.Errorf("BudgetSignature %q not recognised", b)
		}
	}
	if nodes.BudgetSignature("ml").IsKnown() {
		t.Error("BudgetSignature 'ml' should not be known")
	}
}

// TestManifestIsCallable verifies the default rules:
//   - archetype manifest → not callable
//   - kind manifest with no callable: → callable
//   - kind manifest with callable: false → not callable
func TestManifestIsCallable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "archetype defaults to non-callable",
			src:  "archetype: read\ncategory: state\n",
			want: false,
		},
		{
			name: "kind defaults to callable",
			src:  "id: model\nextends: compute\nexecutor: agentgraph.ExecModel\n",
			want: true,
		},
		{
			name: "kind with explicit callable: false",
			src:  "id: stub\nextends: compute\ncallable: false\n",
			want: false,
		},
	}
	for _, tc := range cases {
		var m nodes.Manifest
		if err := yaml.Unmarshal([]byte(tc.src), &m); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.name, err)
		}
		if got := m.IsCallable(); got != tc.want {
			t.Errorf("%s: IsCallable=%v, want %v", tc.name, got, tc.want)
		}
	}
}
