package slashcmd

import (
	"errors"
	"fmt"
	"strings"
)

// ErrMissingRequiredVar is returned when a required {{var}} substitution
// has no value in the args map.
var ErrMissingRequiredVar = errors.New("slashcmd: missing required template variable")

// ErrUnknownBuiltinVar is returned when {{var}} references an unknown
// built-in that could not be resolved.
var ErrUnknownBuiltinVar = errors.New("slashcmd: unknown built-in template variable")

// TemplateVars carries the per-execution context variables resolved
// by the dispatch layer. Built-in vars (§plan §Argument template grammar):
//
//   - selection — current text selection in the editor (may be empty)
//   - cwd       — current working directory
//   - date      — ISO-8601 date (YYYY-MM-DD)
//   - session_id — active session ID
//   - project_id — active project ID (may be empty)
type TemplateVars struct {
	// UserArgs are key=value pairs the user typed at invocation time.
	// The keys are the input.Name values declared in UserCommand.Inputs.
	UserArgs map[string]string

	// Built-in resolved vars.
	Selection string
	CWD       string
	Date      string
	SessionID string
	ProjectID string
}

// builtinValue returns the resolved value for a built-in variable name.
// Returns ("", false) when the name is not a built-in.
func (v TemplateVars) builtinValue(name string) (string, bool) {
	switch name {
	case "selection":
		return v.Selection, true
	case "cwd":
		return v.CWD, true
	case "date":
		return v.Date, true
	case "session_id":
		return v.SessionID, true
	case "project_id":
		return v.ProjectID, true
	default:
		return "", false
	}
}

// resolve returns the value for varName from user args or built-ins.
// Returns ("", false) when the variable has no value.
func (v TemplateVars) resolve(name string) (string, bool) {
	if val, ok := v.UserArgs[name]; ok {
		return val, true
	}
	return v.builtinValue(name)
}

// Render applies the substitution grammar to tmpl and returns the
// rendered string. Grammar:
//
//   - {{var}}         — required; error if missing.
//   - {{?var}}        — optional; empty string if missing.
//   - {{#var}}…{{/var}} — conditional block; omitted when var is empty.
//
// Built-in vars (selection, cwd, date, session_id, project_id) are
// resolved from vars. User-declared args are resolved from vars.UserArgs.
//
// Tool dispatch callers split the rendered string into a []string via
// SplitArgs before passing it to exec.Cmd — never as a shell string.
func Render(tmpl string, vars TemplateVars) (string, error) {
	// Process conditional blocks first ({{#var}}…{{/var}}).
	tmpl, err := renderConditionals(tmpl, vars)
	if err != nil {
		return "", err
	}
	// Then required and optional substitutions.
	return renderSubstitutions(tmpl, vars)
}

// renderConditionals processes {{#var}}…{{/var}} blocks. Blocks are
// rendered when var is non-empty; omitted entirely otherwise.
func renderConditionals(tmpl string, vars TemplateVars) (string, error) {
	var buf strings.Builder
	remaining := tmpl
	for {
		openTag := strings.Index(remaining, "{{#")
		if openTag < 0 {
			buf.WriteString(remaining)
			break
		}
		buf.WriteString(remaining[:openTag])
		rest := remaining[openTag+3:]

		closeDelim := strings.Index(rest, "}}")
		if closeDelim < 0 {
			return "", fmt.Errorf("slashcmd: unclosed {{# block in template")
		}
		varName := rest[:closeDelim]
		rest = rest[closeDelim+2:]

		closeTag := "{{/" + varName + "}}"
		closeIdx := strings.Index(rest, closeTag)
		if closeIdx < 0 {
			return "", fmt.Errorf("slashcmd: missing {{/%s}} closing tag", varName)
		}
		blockContent := rest[:closeIdx]
		remaining = rest[closeIdx+len(closeTag):]

		val, _ := vars.resolve(varName)
		if val != "" {
			// Recursively render the block content.
			rendered, err := renderConditionals(blockContent, vars)
			if err != nil {
				return "", err
			}
			buf.WriteString(rendered)
		}
	}
	return buf.String(), nil
}

// renderSubstitutions processes {{var}} (required) and {{?var}} (optional).
func renderSubstitutions(tmpl string, vars TemplateVars) (string, error) {
	var buf strings.Builder
	remaining := tmpl
	for {
		openIdx := strings.Index(remaining, "{{")
		if openIdx < 0 {
			buf.WriteString(remaining)
			break
		}
		buf.WriteString(remaining[:openIdx])
		rest := remaining[openIdx+2:]

		closeIdx := strings.Index(rest, "}}")
		if closeIdx < 0 {
			return "", fmt.Errorf("slashcmd: unclosed {{ in template")
		}
		inner := rest[:closeIdx]
		remaining = rest[closeIdx+2:]

		optional := false
		varName := inner
		if strings.HasPrefix(inner, "?") {
			optional = true
			varName = inner[1:]
		}

		val, ok := vars.resolve(varName)
		if !ok {
			if optional {
				// Optional: substitute empty string.
				continue
			}
			return "", fmt.Errorf("%w: %q", ErrMissingRequiredVar, varName)
		}
		buf.WriteString(val)
	}
	return buf.String(), nil
}

// SplitArgs splits a rendered tool_args_template into a []string for
// use with exec.Cmd. The split is whitespace-based (fields). This is
// intentionally not shell-parsing — tool dispatch passes args as a
// structured array, never as a single shell string, to prevent injection.
func SplitArgs(rendered string) []string {
	return strings.Fields(rendered)
}
