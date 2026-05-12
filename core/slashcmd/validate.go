package slashcmd

import (
	"errors"
	"fmt"
	"regexp"
)

// nameRe is the pattern every user command name must match.
// Accepts only lowercase alpha-numeric, hyphens, and underscores;
// must start with a lowercase letter; max 31 chars total.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,30}$`)

// ErrValidation is the sentinel wrapped by every Validate error so
// callers can use errors.Is(err, ErrValidation) to detect the class.
var ErrValidation = errors.New("slashcmd: validation error")

// validationErr wraps a human-readable message with ErrValidation.
func validationErr(msg string) error {
	return fmt.Errorf("%w: %s", ErrValidation, msg)
}

// Validate returns the first constraint violation found in cmd. Nil
// means the command is ready to persist.
//
// Checked constraints (in order):
//  1. Name matches ^[a-z][a-z0-9_-]{0,30}$.
//  2. Scope is "global" or "project".
//  3. ProjectID is present when Scope == "project".
//  4. ProjectID is absent when Scope == "global".
//  5. Kind is "text", "tool", or "prompt".
//  6. kind:tool requires non-empty Tool and ToolArgsTemplate.
//  7. kind:text and kind:prompt require non-empty Body.
//  8. Every Input has a non-empty Name and a valid Kind.
//  9. Enum inputs must have at least one enum_value and a valid Default
//     (if provided).
func Validate(cmd UserCommand) error {
	if !nameRe.MatchString(cmd.Name) {
		return validationErr(fmt.Sprintf(
			"name %q must match ^[a-z][a-z0-9_-]{0,30}$", cmd.Name))
	}

	switch cmd.Scope {
	case ScopeGlobal, ScopeProject:
		// OK
	case "":
		return validationErr("scope is required (\"global\" or \"project\")")
	default:
		return validationErr(fmt.Sprintf("scope %q is invalid; must be \"global\" or \"project\"", cmd.Scope))
	}

	if cmd.Scope == ScopeProject && cmd.ProjectID == "" {
		return validationErr("project_id is required when scope is \"project\"")
	}
	if cmd.Scope == ScopeGlobal && cmd.ProjectID != "" {
		return validationErr("project_id must be empty when scope is \"global\"")
	}

	switch cmd.Kind {
	case KindText, KindTool, KindPrompt:
		// OK
	case "":
		return validationErr("kind is required (\"text\", \"tool\", or \"prompt\")")
	default:
		return validationErr(fmt.Sprintf("kind %q is invalid; must be \"text\", \"tool\", or \"prompt\"", cmd.Kind))
	}

	switch cmd.Kind {
	case KindTool:
		if cmd.Tool == "" {
			return validationErr("kind:tool requires a non-empty tool field")
		}
		if cmd.ToolArgsTemplate == "" {
			return validationErr("kind:tool requires a non-empty tool_args_template field")
		}
	case KindText:
		if cmd.Body == "" {
			return validationErr("kind:text requires a non-empty body")
		}
	case KindPrompt:
		if cmd.Body == "" {
			return validationErr("kind:prompt requires a non-empty body (the prompt template)")
		}
	}

	for i, inp := range cmd.Inputs {
		if inp.Name == "" {
			return validationErr(fmt.Sprintf("input[%d]: name is required", i))
		}
		switch inp.Kind {
		case InputKindText, InputKindEnum, InputKindNumber,
			InputKindFile, InputKindArtifactRef, InputKindProjectRef:
			// OK
		case "":
			return validationErr(fmt.Sprintf("input[%d] %q: type is required", i, inp.Name))
		default:
			return validationErr(fmt.Sprintf("input[%d] %q: unknown type %q", i, inp.Name, inp.Kind))
		}
		if inp.Kind == InputKindEnum {
			if len(inp.EnumValues) == 0 {
				return validationErr(fmt.Sprintf("input[%d] %q: enum type requires at least one enum_value", i, inp.Name))
			}
			if inp.Default != "" && !containsString(inp.EnumValues, inp.Default) {
				return validationErr(fmt.Sprintf(
					"input[%d] %q: default value %q is not in enum_values", i, inp.Name, inp.Default))
			}
		}
	}

	return nil
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
