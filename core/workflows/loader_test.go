package workflows

import (
	"errors"
	"strings"
	"testing"
)


func TestLoadYAML_Valid(t *testing.T) {
	yamlSrc := `
id: my_flow
name: "My flow"
version: 1
inputs:
  - name: ref
    kind: string
    default: "main"
steps:
  - name: a
    kind: shell
    cmd: "echo"
    args: ["hello"]
  - name: b
    kind: model_turn
    user_prompt: |
      summarize ${step.a.output}
  - name: c
    kind: model_turn
    user_prompt: |
      finalize ${step.b.output} for ${input.ref}
`
	w, err := LoadYAML([]byte(yamlSrc))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if w.ID != "my_flow" {
		t.Errorf("id: got %q want %q", w.ID, "my_flow")
	}
	if len(w.Steps) != 3 {
		t.Errorf("steps: got %d want 3", len(w.Steps))
	}
	// Round-trip.
	out, err := MarshalYAML(w)
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	w2, err := LoadYAML(out)
	if err != nil {
		t.Fatalf("re-load: %v", err)
	}
	if w2.ID != w.ID || len(w2.Steps) != len(w.Steps) {
		t.Errorf("round-trip diverged")
	}
}

func TestLoadYAML_RejectsInvalidID(t *testing.T) {
	bad := `
id: "Bad ID"
name: "x"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "true"
`
	_, err := LoadYAML([]byte(bad))
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("want ErrInvalidID, got %v", err)
	}
}

func TestLoadYAML_RejectsInlineMultiStep(t *testing.T) {
	bad := `
id: x
name: "x"
version: 1
inline_run: true
steps:
  - name: a
    kind: model_turn
    user_prompt: "hi"
  - name: b
    kind: model_turn
    user_prompt: "hi2"
`
	_, err := LoadYAML([]byte(bad))
	if !errors.Is(err, ErrInlineMultiStep) {
		t.Errorf("want ErrInlineMultiStep, got %v", err)
	}
}

func TestLoadYAML_RejectsForwardRef(t *testing.T) {
	bad := `
id: x
name: "x"
version: 1
steps:
  - name: a
    kind: model_turn
    user_prompt: "use ${step.b.output}"
  - name: b
    kind: model_turn
    user_prompt: "later"
`
	_, err := LoadYAML([]byte(bad))
	if !errors.Is(err, ErrForwardStepRef) {
		t.Errorf("want ErrForwardStepRef, got %v", err)
	}
}

func TestLoadYAML_RejectsUnknownInput(t *testing.T) {
	bad := `
id: x
name: "x"
version: 1
steps:
  - name: a
    kind: model_turn
    user_prompt: "use ${input.missing}"
`
	_, err := LoadYAML([]byte(bad))
	if !errors.Is(err, ErrUnknownReference) {
		t.Errorf("want ErrUnknownReference, got %v", err)
	}
}

func TestLoadYAML_RejectsOversizedFile(t *testing.T) {
	huge := []byte(strings.Repeat("a", FileSizeCap+1))
	_, err := LoadYAML(huge)
	if !errors.Is(err, ErrFileTooLarge) {
		t.Errorf("want ErrFileTooLarge, got %v", err)
	}
}

func TestLoadYAML_RejectsUnknownStepKind(t *testing.T) {
	bad := `
id: x
name: "x"
version: 1
steps:
  - name: a
    kind: weird_kind
`
	_, err := LoadYAML([]byte(bad))
	if !errors.Is(err, ErrUnknownStepKind) {
		t.Errorf("want ErrUnknownStepKind, got %v", err)
	}
}

// ── DAG / inputs_from tests ────────────────────────────────────────────────

func TestLoadYAML_DAG_MultiParentHappyPath(t *testing.T) {
	yaml := `
id: fan_in
name: "Fan-in"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
  - name: b
    kind: shell
    cmd: "echo"
  - name: c
    kind: model_turn
    user_prompt: "merge ${step.a.output} and ${step.b.output}"
    inputs_from: [a, b]
`
	w, err := LoadYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if len(w.Steps) != 3 {
		t.Errorf("want 3 steps, got %d", len(w.Steps))
	}
	// Verify c has the right inputs_from.
	var c *Step
	for i := range w.Steps {
		if w.Steps[i].Name == "c" {
			c = &w.Steps[i]
		}
	}
	if c == nil {
		t.Fatal("step c not found")
	}
	if len(c.InputsFrom) != 2 {
		t.Errorf("c.InputsFrom: got %v want [a b]", c.InputsFrom)
	}
}

func TestLoadYAML_DAG_RejectsCycle(t *testing.T) {
	yaml := `
id: cyclic
name: "Cyclic"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    inputs_from: [b]
  - name: b
    kind: shell
    cmd: "echo"
    inputs_from: [a]
`
	_, err := LoadYAML([]byte(yaml))
	if !errors.Is(err, ErrWorkflowCycle) {
		t.Errorf("want ErrWorkflowCycle, got %v", err)
	}
	// The error message must include the cycle path.
	if err != nil && !strings.Contains(err.Error(), "→") {
		t.Errorf("cycle error should include path with '→', got: %v", err)
	}
}

func TestLoadYAML_DAG_RejectsMissingRef(t *testing.T) {
	yaml := `
id: missing
name: "Missing ref"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    inputs_from: [nonexistent]
`
	_, err := LoadYAML([]byte(yaml))
	if !errors.Is(err, ErrUnknownReference) {
		t.Errorf("want ErrUnknownReference, got %v", err)
	}
}

func TestLoadYAML_DAG_RejectsSelfRef(t *testing.T) {
	yaml := `
id: selfref
name: "Self ref"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    inputs_from: [a]
`
	_, err := LoadYAML([]byte(yaml))
	if !errors.Is(err, ErrWorkflowCycle) {
		t.Errorf("want ErrWorkflowCycle, got %v", err)
	}
}

func TestLoadYAML_DAG_ThreeNodeCycle(t *testing.T) {
	yaml := `
id: three_cycle
name: "Three node cycle"
version: 1
steps:
  - name: a
    kind: shell
    cmd: "echo"
    inputs_from: [c]
  - name: b
    kind: shell
    cmd: "echo"
    inputs_from: [a]
  - name: c
    kind: shell
    cmd: "echo"
    inputs_from: [b]
`
	_, err := LoadYAML([]byte(yaml))
	if !errors.Is(err, ErrWorkflowCycle) {
		t.Errorf("want ErrWorkflowCycle, got %v", err)
	}
}

func TestLoadBuiltins_LoadsExample(t *testing.T) {
	wfs, errs := LoadBuiltins()
	for _, e := range errs {
		t.Errorf("LoadBuiltins: %v", e)
	}
	if len(wfs) == 0 {
		t.Fatalf("expected at least one bundled workflow")
	}
	found := false
	for _, w := range wfs {
		if w.ID == "plan_implement_review" {
			found = true
			if len(w.Steps) != 4 {
				t.Errorf("plan_implement_review: got %d steps, want 4", len(w.Steps))
			}
		}
	}
	if !found {
		t.Errorf("plan_implement_review not in bundled set")
	}
}
