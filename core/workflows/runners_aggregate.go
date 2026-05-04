package workflows

// runners_aggregate.go — `aggregate` step runner (WP06).
//
// Fan-in node that collects outputs from multiple parent steps (listed in
// InputsFrom) into a single value using one of three strategies:
//
//   merge  — shallow-merge object (map[string]any) outputs; last-wins on key
//             conflicts (a warning is surfaced in the result payload).
//   array  — collect raw outputs into a JSON array in declaration order.
//   concat — join parent output Text values with a separator (default ",").
//
// Output shape: {result: <value>, parent_count: N}.

import (
	"encoding/json"
	"fmt"
	"strings"

	"context"
)

type aggregateRunner struct{}

func (aggregateRunner) Validate(st Step) error {
	switch st.Strategy {
	case "merge", "array", "concat":
	case "":
		return fmt.Errorf("aggregate step %q: strategy required (merge|array|concat)", st.Name)
	default:
		return fmt.Errorf("aggregate step %q: unknown strategy %q (valid: merge, array, concat)", st.Name, st.Strategy)
	}
	if len(st.InputsFrom) == 0 {
		return fmt.Errorf("aggregate step %q: inputs_from must list at least one parent step", st.Name)
	}
	return nil
}

func (aggregateRunner) Run(_ context.Context, st Step, rc *RunContext) (TypedValue, error) {
	// Collect parent outputs in declaration order.
	parents := make([]TypedValue, 0, len(st.InputsFrom))
	for _, name := range st.InputsFrom {
		v, ok := rc.StepOutputs[name]
		if !ok {
			return TypedValue{Type: ValueTypeError},
				fmt.Errorf("aggregate step %q: parent step %q has no output", st.Name, name)
		}
		parents = append(parents, v)
	}

	var result any
	var conflicts []string

	switch st.Strategy {
	case "merge":
		merged := make(map[string]any)
		for i, p := range parents {
			obj := toObject(p)
			for k, v := range obj {
				if _, exists := merged[k]; exists {
					conflicts = append(conflicts, fmt.Sprintf("key %q overwritten by parent %s", k, st.InputsFrom[i]))
				}
				merged[k] = v
			}
		}
		result = merged

	case "array":
		arr := make([]any, len(parents))
		for i, p := range parents {
			arr[i] = toScalar(p)
		}
		result = arr

	case "concat":
		sep := st.Separator
		if sep == "" {
			sep = ","
		}
		parts := make([]string, len(parents))
		for i, p := range parents {
			parts[i] = p.Text
		}
		result = strings.Join(parts, sep)
	}

	payload := map[string]any{
		"result":       result,
		"parent_count": len(parents),
	}
	if len(conflicts) > 0 {
		payload["merge_warnings"] = conflicts
	}
	encoded, _ := json.Marshal(payload)
	return TypedValue{Type: ValueTypeJSON, JSON: payload, Text: string(encoded)}, nil
}

// toObject coerces a TypedValue into map[string]any for merge strategy.
// If the value is already JSON with an object at the top level, that is
// used. Otherwise the Text field is wrapped under a single "value" key.
func toObject(v TypedValue) map[string]any {
	if v.JSON != nil {
		if m, ok := v.JSON.(map[string]any); ok {
			return m
		}
	}
	// Try to unmarshal from Text.
	if v.Text != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(v.Text), &m); err == nil {
			return m
		}
	}
	return map[string]any{"value": v.Text}
}

// toScalar returns a canonical Go value suitable for JSON array elements.
func toScalar(v TypedValue) any {
	if v.JSON != nil {
		return v.JSON
	}
	return v.Text
}
