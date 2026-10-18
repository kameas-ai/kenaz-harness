package workflows

// runtime_enum_input_test.go — automation-actually-runs-01PMZ404
// UNIT-14, server-side half (AC-015). A-11 requires that the enum
// input-kind constraint be enforced by the ENGINE, not merely by the
// run form's <select> widget — any caller that is not the run form
// (the /wf slash-command gateway builds a raw map[string]string with
// no input-kind awareness of its own) must have the same value rejected.
// This test drives Engine.Run directly, the single choke point every
// production caller (RunWithOptions, InlineRun, the /wf gateway) passes
// through.

import (
	"context"
	"strings"
	"testing"
)

const enumInputProbeYAML = `
id: zz-enum-input-probe
name: "enum input probe"
version: 1
inputs:
  - name: mode
    kind: enum
    options: ["fast", "thorough"]
    default: "fast"
steps:
  - name: greet
    kind: transform
    template: "hello ${input.mode}"
`

func TestEngineRun_EnumInput_RejectsOutOfSetValue(t *testing.T) {
	wf, err := LoadYAML([]byte(enumInputProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	engine := NewEngineWithDeps(Deps{})

	run, err := engine.Run(context.Background(), wf, map[string]TypedValue{
		"mode": {Type: ValueTypeText, Text: "sloppy"},
	}, RunOptions{})
	if err == nil {
		t.Fatal("expected an error for an out-of-set enum value, got nil")
	}
	if !strings.Contains(err.Error(), "mode") || !strings.Contains(err.Error(), "sloppy") {
		t.Errorf("err = %v, want it to name the input (mode) and the rejected value (sloppy)", err)
	}
	if run.Status != "failed" {
		t.Errorf("run.Status = %q, want failed", run.Status)
	}
}

func TestEngineRun_EnumInput_AcceptsDeclaredValue(t *testing.T) {
	wf, err := LoadYAML([]byte(enumInputProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	engine := NewEngineWithDeps(Deps{})

	run, err := engine.Run(context.Background(), wf, map[string]TypedValue{
		"mode": {Type: ValueTypeText, Text: "thorough"},
	}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: unexpected error for a declared enum value: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed (err=%q)", run.Status, run.Err)
	}
}

func TestEngineRun_EnumInput_OmittedValueDoesNotBlock(t *testing.T) {
	wf, err := LoadYAML([]byte(enumInputProbeYAML))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	engine := NewEngineWithDeps(Deps{})

	// No "mode" key supplied at all — validateEnumInputs must not treat
	// an absent value as a violation (mergeInputDefaults is the layer
	// that decides what an omitted optional input becomes).
	run, err := engine.Run(context.Background(), wf, map[string]TypedValue{}, RunOptions{})
	if err != nil {
		t.Fatalf("Run: unexpected error for an omitted enum input: %v", err)
	}
	if run.Status != "completed" {
		t.Fatalf("run.Status = %q, want completed (err=%q)", run.Status, run.Err)
	}
}
