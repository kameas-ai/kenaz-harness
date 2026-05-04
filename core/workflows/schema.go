package workflows

import (
	"fmt"
)

// validKinds is the set returned by AllStepKinds, indexed for O(1)
// validation.
var validKinds = func() map[StepKind]bool {
	m := make(map[StepKind]bool)
	for _, k := range AllStepKinds() {
		m[k] = true
	}
	return m
}()

var validInputKinds = map[InputKind]bool{
	InputKindString:      true,
	InputKindMultiline:   true,
	InputKindEnum:        true,
	InputKindFile:        true,
	InputKindArtifactRef: true,
	InputKindProjectRef:  true,
}

// Validate runs every load-time invariant on a Workflow. Returns the
// first error encountered. Validate does NOT mutate the Workflow.
func Validate(w Workflow) error {
	if !isKebab(w.ID) {
		return fmt.Errorf("%w: id %q", ErrInvalidID, w.ID)
	}
	if w.Name == "" {
		return fmt.Errorf("workflows: name required")
	}
	if w.Version < 1 {
		return fmt.Errorf("workflows: version must be >= 1 (got %d)", w.Version)
	}
	if len(w.Steps) == 0 {
		return fmt.Errorf("workflows: at least one step required")
	}
	switch w.RerunPolicy {
	case "", "fresh", "continue", "ask",
		// WP08 canonical aliases — kept alongside the WP01 vocabulary
		// so existing fixtures keep parsing while new authors can use
		// the names that match the rerun resolver behaviour.
		"always", "skip", "prompt":
	default:
		return fmt.Errorf("workflows: invalid rerun_policy %q", w.RerunPolicy)
	}

	// inline_run gating.
	if w.InlineRun {
		if len(w.Steps) != 1 || w.Steps[0].Kind != StepKindModelTurn {
			return ErrInlineMultiStep
		}
	}

	// Inputs map.
	inputSet := make(map[string]bool, len(w.Inputs))
	for _, in := range w.Inputs {
		if in.Name == "" {
			return fmt.Errorf("workflows: input name required")
		}
		if !validInputKinds[in.Kind] {
			return fmt.Errorf("workflows: invalid input kind %q on %q", in.Kind, in.Name)
		}
		if in.Kind == InputKindEnum && len(in.Options) == 0 {
			return fmt.Errorf("workflows: enum input %q requires options", in.Name)
		}
		inputSet[in.Name] = true
	}

	// Pre-build the all-steps set so refs can distinguish "later step"
	// from "unknown step".
	allSteps := make(map[string]bool, len(w.Steps))
	stepNames := make(map[string]bool, len(w.Steps))
	for _, st := range w.Steps {
		allSteps[st.Name] = true
	}

	for _, st := range w.Steps {
		if st.Name == "" {
			return fmt.Errorf("workflows: step name required")
		}
		if stepNames[st.Name] {
			return fmt.Errorf("workflows: duplicate step name %q", st.Name)
		}
		if !validKinds[st.Kind] {
			return fmt.Errorf("%w: %q", ErrUnknownStepKind, st.Kind)
		}
		if err := validateStepFields(st); err != nil {
			return err
		}
		// Validate references in user-text fields point at known
		// inputs / earlier steps.
		for _, field := range collectRefBearingFields(st) {
			if err := validateRefs(field, inputSet, stepNames, allSteps); err != nil {
				return fmt.Errorf("step %q: %w", st.Name, err)
			}
		}
		stepNames[st.Name] = true
	}
	return nil
}

// validateStepFields enforces required-fields-by-kind for every
// step kind the engine dispatches.
func validateStepFields(st Step) error {
	switch st.Kind {
	case StepKindModelTurn:
		if st.UserPrompt == "" {
			return fmt.Errorf("step %q: model_turn requires user_prompt", st.Name)
		}
	case StepKindShell:
		if st.Cmd == "" {
			return fmt.Errorf("step %q: shell requires cmd", st.Name)
		}
	case StepKindToolCall:
		if st.ToolName == "" {
			return fmt.Errorf("step %q: tool_call requires tool_name", st.Name)
		}
	case StepKindHTTPRequest:
		if st.URL == "" {
			return fmt.Errorf("step %q: http_request requires url", st.Name)
		}
		if st.Method == "" {
			return fmt.Errorf("step %q: http_request requires method", st.Name)
		}
	case StepKindMCPCall:
		if st.Server == "" {
			return fmt.Errorf("step %q: mcp_call requires server", st.Name)
		}
		if st.ToolName == "" {
			return fmt.Errorf("step %q: mcp_call requires tool_name", st.Name)
		}
	case StepKindReadArtifact:
		if st.ArtifactIDRef == "" {
			return fmt.Errorf("step %q: read_artifact requires artifact_id_ref", st.Name)
		}
	case StepKindWriteArtifact:
		if st.Title == "" {
			return fmt.Errorf("step %q: write_artifact requires title", st.Name)
		}
		if st.Content == "" && st.ContentRef == "" {
			return fmt.Errorf("step %q: write_artifact requires content or content_ref", st.Name)
		}
	case StepKindTransform:
		if st.Template == "" {
			return fmt.Errorf("step %q: transform requires template", st.Name)
		}
	case StepKindConditional:
		if st.If == "" {
			return fmt.Errorf("step %q: conditional requires if", st.Name)
		}
		if st.ThenStep == "" && st.ElseStep == "" {
			return fmt.Errorf("step %q: conditional requires then_step or else_step", st.Name)
		}
	}
	return nil
}

// collectRefBearingFields returns the set of user-authored strings
// that may carry ${...} expansions and need ref validation.
func collectRefBearingFields(st Step) []string {
	out := []string{
		st.UserPrompt, st.Body, st.URL, st.ContentRef, st.ArtifactIDRef,
		st.Title, st.Content, st.Template, st.If,
	}
	out = append(out, st.Args...)
	return out
}
