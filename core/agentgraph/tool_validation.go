package agentgraph

import (
	"encoding/json"
	"fmt"
	"strings"
)

// validateToolArgs validates the model-supplied argument string for
// basic structural well-formedness (FR-006 / agent-loop-robustness-parity
// WP06). It does NOT perform full JSON Schema validation — that would
// require a dependency — but it:
//
//  1. Rejects unparseable JSON (the `_raw` passthrough that previously
//     silently forwarded garbage to tool implementations is removed).
//  2. Validates required properties from a simplified schema subset:
//     when schemaJSON is non-empty and contains a "required" array, we
//     verify each listed property name is present in the args object.
//  3. Validates that top-level property types match the schema "type"
//     field when both sides provide a type annotation.
//
// Returns a non-empty string when validation fails; the string is the
// model-readable correction phrasing the model can use to self-correct
// on the next turn (mirrors reference toolErrors.ts phrasing).
//
// An empty schemaJSON or an unstructured schema (non-object top level)
// degrades to well-formedness-only checking.
func validateToolArgs(rawArgs string, toolName string, schemaJSON []byte) string {
	// Empty args are fine for no-argument tools.
	trimmed := strings.TrimSpace(rawArgs)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return "" // valid: no args
	}

	// Step 1: JSON must parse as an object.
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf(
			"Tool %q received non-JSON arguments (expected a JSON object). "+
				"Error: %s. Please retry with a valid JSON object.",
			toolName, err.Error(),
		)
	}

	if len(schemaJSON) == 0 {
		return "" // no schema to validate against
	}

	// Step 2: parse the schema lightly.
	var schema map[string]any
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return "" // unparseable schema — skip validation
	}

	// Step 3: validate required properties.
	if req, ok := schema["required"]; ok {
		switch rv := req.(type) {
		case []any:
			var missing []string
			for _, r := range rv {
				name, ok := r.(string)
				if !ok {
					continue
				}
				if _, present := args[name]; !present {
					missing = append(missing, name)
				}
			}
			if len(missing) > 0 {
				return fmt.Sprintf(
					"Tool %q is missing required parameter(s): %s. "+
						"Please include all required parameters and retry.",
					toolName, strings.Join(missing, ", "),
				)
			}
		}
	}

	// Step 4: lightweight type checking against schema "properties".
	properties, _ := schema["properties"].(map[string]any)
	for propName, propVal := range properties {
		schemaProp, ok := propVal.(map[string]any)
		if !ok {
			continue
		}
		wantType, ok := schemaProp["type"].(string)
		if !ok {
			continue
		}
		actualVal, present := args[propName]
		if !present {
			continue // missing non-required props are fine
		}
		if msg := checkJSONType(propName, wantType, actualVal, toolName); msg != "" {
			return msg
		}
	}

	return "" // all checks passed
}

// checkJSONType verifies that actualVal from JSON decode matches the
// declared JSON Schema "type". Returns a model-readable correction on
// mismatch, empty string on match or on unrecognised types.
func checkJSONType(propName, wantType string, actualVal any, toolName string) string {
	// JSON decode maps Go types: bool→bool, number→float64, string→string,
	// array→[]any, object→map[string]any, null→nil.
	ok := false
	switch wantType {
	case "string":
		_, ok = actualVal.(string)
	case "number", "integer":
		_, ok = actualVal.(float64)
	case "boolean":
		_, ok = actualVal.(bool)
	case "array":
		_, ok = actualVal.([]any)
	case "object":
		_, ok = actualVal.(map[string]any)
	case "null":
		ok = actualVal == nil
	default:
		return "" // unknown type — skip
	}
	if !ok {
		return fmt.Sprintf(
			"Tool %q parameter %q has the wrong type (expected %s). "+
				"Please correct the parameter type and retry.",
			toolName, propName, wantType,
		)
	}
	return ""
}
