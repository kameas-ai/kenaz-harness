package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// =============================================================================
// aggregate runner tests
// =============================================================================

func TestAggregate_Merge_TwoObjectParents(t *testing.T) {
	// Two transform steps produce JSON objects; aggregate merges them.
	wf := Workflow{
		ID: "a", Name: "a", Version: 1,
		Steps: []Step{
			{Name: "s1", Kind: StepKindTransform, Template: `{"a":1}`},
			{Name: "s2", Kind: StepKindTransform, Template: `{"b":2}`},
			{
				Name:       "fan-in",
				Kind:       StepKindAggregate,
				Strategy:   "merge",
				InputsFrom: []string{"s1", "s2"},
			},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	out := run.Steps[2].Output
	var payload struct {
		Result       map[string]any `json:"result"`
		ParentCount  int            `json:"parent_count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\nraw: %s", err, out)
	}
	if payload.ParentCount != 2 {
		t.Errorf("parent_count: got %d want 2", payload.ParentCount)
	}
	if payload.Result["a"] == nil || payload.Result["b"] == nil {
		t.Errorf("merged keys missing: result=%v", payload.Result)
	}
}

func TestAggregate_Merge_ConflictingKeys_LastWins(t *testing.T) {
	// Both parents have key "x"; s2 should win.
	rc := &RunContext{
		Inputs:      map[string]TypedValue{},
		StepOutputs: map[string]TypedValue{
			"p1": {Type: ValueTypeJSON, JSON: map[string]any{"x": "first"},  Text: `{"x":"first"}`},
			"p2": {Type: ValueTypeJSON, JSON: map[string]any{"x": "second"}, Text: `{"x":"second"}`},
		},
	}
	st := Step{
		Name:       "fan",
		Kind:       StepKindAggregate,
		Strategy:   "merge",
		InputsFrom: []string{"p1", "p2"},
	}
	out, err := aggregateRunner{}.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("expected JSON map, got %T", out.JSON)
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not a map: %T", payload["result"])
	}
	if result["x"] != "second" {
		t.Errorf("last-wins: got %v want second", result["x"])
	}
	// Conflict warning should be present.
	if _, ok := payload["merge_warnings"]; !ok {
		t.Errorf("expected merge_warnings in output")
	}
}

func TestAggregate_Array_ThreeParents_OrderPreserved(t *testing.T) {
	wf := Workflow{
		ID: "a", Name: "a", Version: 1,
		Steps: []Step{
			{Name: "s1", Kind: StepKindTransform, Template: "alpha"},
			{Name: "s2", Kind: StepKindTransform, Template: "beta"},
			{Name: "s3", Kind: StepKindTransform, Template: "gamma"},
			{
				Name:       "collect",
				Kind:       StepKindAggregate,
				Strategy:   "array",
				InputsFrom: []string{"s1", "s2", "s3"},
			},
		},
	}
	run, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("status: %s err=%s", run.Status, run.Err)
	}
	out := run.Steps[3].Output
	var payload struct {
		Result      []any `json:"result"`
		ParentCount int   `json:"parent_count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if payload.ParentCount != 3 {
		t.Errorf("parent_count: got %d want 3", payload.ParentCount)
	}
	if len(payload.Result) != 3 {
		t.Errorf("result len: got %d want 3", len(payload.Result))
	}
	// Order must be preserved.
	want := []string{"alpha", "beta", "gamma"}
	for i, v := range payload.Result {
		if v != want[i] {
			t.Errorf("result[%d]: got %v want %s", i, v, want[i])
		}
	}
}

func TestAggregate_Concat_DefaultSeparator(t *testing.T) {
	rc := &RunContext{
		Inputs: map[string]TypedValue{},
		StepOutputs: map[string]TypedValue{
			"a": {Type: ValueTypeText, Text: "foo"},
			"b": {Type: ValueTypeText, Text: "bar"},
			"c": {Type: ValueTypeText, Text: "baz"},
		},
	}
	st := Step{
		Name:       "join",
		Kind:       StepKindAggregate,
		Strategy:   "concat",
		InputsFrom: []string{"a", "b", "c"},
		// Separator omitted → default ","
	}
	out, err := aggregateRunner{}.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	payload, ok := out.JSON.(map[string]any)
	if !ok {
		t.Fatalf("expected JSON map, got %T", out.JSON)
	}
	if payload["result"] != "foo,bar,baz" {
		t.Errorf("result: got %v want foo,bar,baz", payload["result"])
	}
	if payload["parent_count"].(int) != 3 {
		t.Errorf("parent_count: got %v want 3", payload["parent_count"])
	}
}

func TestAggregate_Concat_CustomSeparator(t *testing.T) {
	rc := &RunContext{
		Inputs: map[string]TypedValue{},
		StepOutputs: map[string]TypedValue{
			"a": {Type: ValueTypeText, Text: "x"},
			"b": {Type: ValueTypeText, Text: "y"},
		},
	}
	st := Step{
		Name:       "join",
		Kind:       StepKindAggregate,
		Strategy:   "concat",
		InputsFrom: []string{"a", "b"},
		Separator:  " | ",
	}
	out, err := aggregateRunner{}.Run(context.Background(), st, rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	payload := out.JSON.(map[string]any)
	if payload["result"] != "x | y" {
		t.Errorf("result: got %v want 'x | y'", payload["result"])
	}
}

func TestAggregate_ValidationRejectsMissingStrategy(t *testing.T) {
	wf := Workflow{
		ID: "a", Name: "a", Version: 1,
		Steps: []Step{
			{Name: "s1", Kind: StepKindTransform, Template: "x"},
			{Name: "fan", Kind: StepKindAggregate, InputsFrom: []string{"s1"}},
		},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "strategy") {
		t.Fatalf("expected strategy validation error, got: %v", err)
	}
}

func TestAggregate_ValidationRejectsMissingInputsFrom(t *testing.T) {
	wf := Workflow{
		ID: "a", Name: "a", Version: 1,
		Steps: []Step{
			{Name: "fan", Kind: StepKindAggregate, Strategy: "array"},
		},
	}
	_, err := NewEngine().Run(context.Background(), wf, nil, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "inputs_from") {
		t.Fatalf("expected inputs_from validation error, got: %v", err)
	}
}

func TestAggregate_MissingParentOutput_Errors(t *testing.T) {
	rc := &RunContext{
		Inputs:      map[string]TypedValue{},
		StepOutputs: map[string]TypedValue{}, // no parent outputs
	}
	st := Step{
		Name:       "fan",
		Kind:       StepKindAggregate,
		Strategy:   "array",
		InputsFrom: []string{"missing-step"},
	}
	_, err := aggregateRunner{}.Run(context.Background(), st, rc)
	if err == nil || !strings.Contains(err.Error(), "no output") {
		t.Fatalf("expected missing-parent error, got: %v", err)
	}
}
