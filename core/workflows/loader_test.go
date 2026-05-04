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
