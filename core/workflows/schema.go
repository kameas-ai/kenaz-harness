package workflows

import (
	"fmt"
	"strings"
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

	// Secrets block validation (model-secret-references-01KW7M5A WP12).
	// Each entry must have a non-empty, whitespace-free locator and must
	// not start with @secret: (the prefix is added by the resolver).
	seenLocators := make(map[string]bool, len(w.Secrets))
	for _, s := range w.Secrets {
		if strings.TrimSpace(s.Locator) == "" {
			return fmt.Errorf("workflows: secrets entry has empty locator")
		}
		if strings.ContainsAny(s.Locator, " \t\n\r") {
			return fmt.Errorf("workflows: secrets locator %q must not contain whitespace", s.Locator)
		}
		if strings.HasPrefix(s.Locator, "@secret:") {
			return fmt.Errorf("workflows: secrets locator %q must not start with @secret: (use bare locator, e.g. user:my-key)", s.Locator)
		}
		if seenLocators[s.Locator] {
			return fmt.Errorf("workflows: duplicate secrets locator %q", s.Locator)
		}
		seenLocators[s.Locator] = true
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

	// Determine whether ANY step uses inputs_from. When none do, the
	// workflow is pure-linear and the old implicit-ordering semantics
	// apply (back-compat). When at least one step uses inputs_from the
	// DAG validator runs.
	hasDAG := false
	for _, st := range w.Steps {
		if len(st.InputsFrom) > 0 {
			hasDAG = true
			break
		}
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

		// Validate inputs_from entries reference known step names.
		// We check against allSteps (not stepNames which grows
		// iteratively) because DAG order is determined by the graph,
		// not declaration order.
		for _, ref := range st.InputsFrom {
			if !allSteps[ref] {
				return fmt.Errorf("%w: step %q inputs_from unknown step %q",
					ErrUnknownReference, st.Name, ref)
			}
			if ref == st.Name {
				return fmt.Errorf("%w: step %q inputs_from itself",
					ErrWorkflowCycle, st.Name)
			}
		}

		// Validate ${...} refs in user-text fields.
		// For linear workflows (no inputs_from) enforce the strict
		// forward-ref check — references must point at earlier steps.
		// For DAG workflows the execution order is determined by the
		// graph, so we only verify that referenced step names exist
		// (the topological sort in the loader will catch bad orderings).
		if !hasDAG {
			for _, field := range collectRefBearingFields(st) {
				if err := validateRefs(field, inputSet, stepNames, allSteps); err != nil {
					return fmt.Errorf("step %q: %w", st.Name, err)
				}
			}
		} else {
			for _, field := range collectRefBearingFields(st) {
				if err := validateRefsDAG(field, inputSet, allSteps); err != nil {
					return fmt.Errorf("step %q: %w", st.Name, err)
				}
			}
		}
		stepNames[st.Name] = true
	}

	// Run the topological sort for DAG workflows to catch cycles early.
	if hasDAG {
		if _, err := topoSort(w.Steps); err != nil {
			return err
		}
	}

	return nil
}

// validateRefsDAG is like validateRefs but relaxed for DAG workflows:
// it only verifies that step references name a real step (not that the
// step has already been declared). Execution order is guaranteed by the
// topological sort.
func validateRefsDAG(s string, inputs map[string]bool, allSteps map[string]bool) error {
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch != '$' || i+1 >= len(s) || s[i+1] != '{' {
			i++
			continue
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return nil
		}
		expr := strings.TrimSpace(s[i+2 : i+2+end])
		parts := strings.Split(expr, ".")
		if len(parts) >= 2 {
			switch parts[0] {
			case "input":
				if !inputs[parts[1]] {
					return fmt.Errorf("%w: input %q", ErrUnknownReference, parts[1])
				}
			case "step":
				name := parts[1]
				if !allSteps[name] {
					return fmt.Errorf("%w: step %q", ErrUnknownReference, name)
				}
			}
		}
		i += 2 + end + 1
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
	case StepKindWebFetch:
		if st.URL == "" {
			return fmt.Errorf("step %q: web_fetch requires url", st.Name)
		}
	case StepKindWebScrape:
		if st.URL == "" {
			return fmt.Errorf("step %q: web_scrape requires url", st.Name)
		}
		// mode must be empty/"css" or "llm"
		switch st.Mode {
		case "", "css", "llm":
		default:
			return fmt.Errorf("step %q: web_scrape mode must be css or llm (got %q)", st.Name, st.Mode)
		}
		if st.Mode == "llm" || st.ExtractWithModel != "" {
			if st.ExtractPrompt == "" {
				return fmt.Errorf("step %q: web_scrape llm mode requires extract_prompt", st.Name)
			}
		}
	case StepKindNotify:
		if st.NotifyTitle == "" {
			return fmt.Errorf("step %q: notify requires notify_title", st.Name)
		}
		if len(st.Surface) == 0 {
			return fmt.Errorf("step %q: notify requires surface", st.Name)
		}
	case StepKindWaitUntil:
		set := 0
		if st.Until != "" {
			set++
		}
		if st.WaitDuration != "" {
			set++
		}
		if st.Condition != "" {
			set++
		}
		if set == 0 {
			return fmt.Errorf("step %q: wait_until requires until, duration, or condition", st.Name)
		}
		if set > 1 {
			return fmt.Errorf("step %q: wait_until: only one of until, duration, condition allowed", st.Name)
		}
	case StepKindAggregate:
		if st.Strategy == "" {
			return fmt.Errorf("step %q: aggregate requires strategy", st.Name)
		}
		if len(st.InputsFrom) == 0 {
			return fmt.Errorf("step %q: aggregate requires inputs_from", st.Name)
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
