package workflows

import (
	"fmt"
	"strings"
	"unicode"
)

// expandRefs walks the input string and replaces every ${...} token
// with its resolved value pulled from rc. Unknown references return
// ErrUnknownReference. The grammar accepted is intentionally narrow:
//
//   ${input.<name>}
//   ${step.<name>.output}
//
// (.output suffix is optional on `step` references; both forms work.)
//
// Tokens that aren't ${...} are emitted verbatim. A literal $ that is
// not followed by `{` is also emitted verbatim.
func expandRefs(s string, rc *RunContext) (string, error) {
	var out strings.Builder
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch != '$' || i+1 >= len(s) || s[i+1] != '{' {
			out.WriteByte(ch)
			i++
			continue
		}
		// scan to matching }
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			out.WriteString(s[i:])
			break
		}
		expr := s[i+2 : i+2+end]
		val, err := resolveRef(expr, rc)
		if err != nil {
			return "", err
		}
		out.WriteString(val)
		i += 2 + end + 1
	}
	return out.String(), nil
}

// resolveRef parses one ref expression (without the ${ }) and returns
// the textual value it points at.
func resolveRef(expr string, rc *RunContext) (string, error) {
	expr = strings.TrimSpace(expr)
	parts := strings.Split(expr, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("%w: %q", ErrUnknownReference, expr)
	}
	switch parts[0] {
	case "input":
		name := parts[1]
		v, ok := rc.Inputs[name]
		if !ok {
			return "", fmt.Errorf("%w: input %q not provided", ErrUnknownReference, name)
		}
		return v.Text, nil
	case "step":
		if len(parts) < 2 {
			return "", fmt.Errorf("%w: step ref missing name", ErrUnknownReference)
		}
		name := parts[1]
		v, ok := rc.StepOutputs[name]
		if !ok {
			return "", fmt.Errorf("%w: step %q has no output (yet)", ErrUnknownReference, name)
		}
		// .output suffix is permitted but ignored — there's only one
		// output channel per step in beta.
		return v.Text, nil
	default:
		return "", fmt.Errorf("%w: unknown root %q", ErrUnknownReference, parts[0])
	}
}

// validateRefs walks every ${...} token in s and confirms it points
// either at an input or at an earlier step. Used by the loader to
// catch forward references at parse time.
func validateRefs(s string, inputs map[string]bool, priorSteps map[string]bool, allSteps map[string]bool) error {
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
				if !priorSteps[name] {
					if allSteps[name] {
						return fmt.Errorf("%w: %q", ErrForwardStepRef, name)
					}
					return fmt.Errorf("%w: step %q", ErrUnknownReference, name)
				}
			}
		}
		i += 2 + end + 1
	}
	return nil
}

// isKebab returns true when s matches `^[a-z][a-z0-9_-]{0,63}$`.
func isKebab(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r >= 'a' && r <= 'z') {
			return false
		}
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
		if unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
