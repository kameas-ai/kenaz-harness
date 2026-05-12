package structured

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidationError carries a structured validation failure, including the
// JSON-path at which the violation occurred and a human-readable message.
type ValidationError struct {
	Field   string // JSON pointer path (e.g. ".name", ".items[0].age")
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

// Validate validates data against schema using a practical draft-07 subset.
//
// Supported keywords: type, properties, required, enum, items,
// additionalProperties, minLength, maxLength, minimum, maximum, pattern (ignored).
// Unknown keywords are silently accepted (pass-through), not rejected.
//
// Returns nil when data is valid, or *ValidationError on the first violation
// found. When schema is nil or empty, any valid JSON passes.
func Validate(schema json.RawMessage, data []byte) *ValidationError {
	if len(schema) == 0 {
		// No schema means any JSON is acceptable. Ensure data is valid JSON.
		if !json.Valid(data) {
			return &ValidationError{Message: "response is not valid JSON"}
		}
		return nil
	}
	if !json.Valid(data) {
		return &ValidationError{Message: "response is not valid JSON"}
	}

	var schemaDoc map[string]any
	if err := json.Unmarshal(schema, &schemaDoc); err != nil {
		// If schema itself can't be parsed, skip validation (programmer error).
		return nil
	}

	var dataVal any
	if err := json.Unmarshal(data, &dataVal); err != nil {
		return &ValidationError{Message: "response JSON parse failed: " + err.Error()}
	}

	return validateValue(schemaDoc, dataVal, "")
}

func validateValue(schema map[string]any, val any, path string) *ValidationError {
	// type check
	if typeKw, ok := schema["type"]; ok {
		if verr := checkType(typeKw, val, path); verr != nil {
			return verr
		}
	}

	// enum check
	if enumKw, ok := schema["enum"]; ok {
		if verr := checkEnum(enumKw, val, path); verr != nil {
			return verr
		}
	}

	switch v := val.(type) {
	case map[string]any:
		if verr := validateObject(schema, v, path); verr != nil {
			return verr
		}
	case []any:
		if verr := validateArray(schema, v, path); verr != nil {
			return verr
		}
	case string:
		if verr := validateString(schema, v, path); verr != nil {
			return verr
		}
	case float64:
		if verr := validateNumber(schema, v, path); verr != nil {
			return verr
		}
	}

	return nil
}

func validateObject(schema map[string]any, obj map[string]any, path string) *ValidationError {
	// required
	if reqKw, ok := schema["required"]; ok {
		if reqArr, ok := reqKw.([]any); ok {
			for _, r := range reqArr {
				if rStr, ok := r.(string); ok {
					if _, exists := obj[rStr]; !exists {
						return &ValidationError{
							Field:   fieldPath(path, rStr),
							Message: "required field missing",
						}
					}
				}
			}
		}
	}

	// properties
	propsKw, hasProps := schema["properties"]
	if hasProps {
		if propsMap, ok := propsKw.(map[string]any); ok {
			for propName, propSchemaRaw := range propsMap {
				propSchema, ok := propSchemaRaw.(map[string]any)
				if !ok {
					continue
				}
				propVal, exists := obj[propName]
				if !exists {
					continue // not required, skip
				}
				if verr := validateValue(propSchema, propVal, fieldPath(path, propName)); verr != nil {
					return verr
				}
			}
		}
	}

	// additionalProperties: false
	if addlKw, ok := schema["additionalProperties"]; ok {
		if addlBool, ok := addlKw.(bool); ok && !addlBool {
			if hasProps {
				if propsMap, ok := propsKw.(map[string]any); ok {
					for key := range obj {
						if _, defined := propsMap[key]; !defined {
							return &ValidationError{
								Field:   fieldPath(path, key),
								Message: "additional property not allowed",
							}
						}
					}
				}
			}
		}
	}

	return nil
}

func validateArray(schema map[string]any, arr []any, path string) *ValidationError {
	itemsKw, hasItems := schema["items"]
	if !hasItems {
		return nil
	}
	itemSchema, ok := itemsKw.(map[string]any)
	if !ok {
		return nil
	}
	for i, item := range arr {
		if verr := validateValue(itemSchema, item, fmt.Sprintf("%s[%d]", path, i)); verr != nil {
			return verr
		}
	}
	return nil
}

func validateString(schema map[string]any, s string, path string) *ValidationError {
	if minLen, ok := numFromAny(schema["minLength"]); ok {
		if len([]rune(s)) < int(minLen) {
			return &ValidationError{Field: path, Message: fmt.Sprintf("string length %d is less than minLength %d", len([]rune(s)), int(minLen))}
		}
	}
	if maxLen, ok := numFromAny(schema["maxLength"]); ok {
		if len([]rune(s)) > int(maxLen) {
			return &ValidationError{Field: path, Message: fmt.Sprintf("string length %d exceeds maxLength %d", len([]rune(s)), int(maxLen))}
		}
	}
	return nil
}

func validateNumber(schema map[string]any, n float64, path string) *ValidationError {
	if min, ok := numFromAny(schema["minimum"]); ok {
		if n < min {
			return &ValidationError{Field: path, Message: fmt.Sprintf("value %v is less than minimum %v", n, min)}
		}
	}
	if max, ok := numFromAny(schema["maximum"]); ok {
		if n > max {
			return &ValidationError{Field: path, Message: fmt.Sprintf("value %v exceeds maximum %v", n, max)}
		}
	}
	return nil
}

func checkType(typeKw any, val any, path string) *ValidationError {
	var types []string
	switch t := typeKw.(type) {
	case string:
		types = []string{t}
	case []any:
		for _, tv := range t {
			if s, ok := tv.(string); ok {
				types = append(types, s)
			}
		}
	}
	if len(types) == 0 {
		return nil
	}
	actual := jsonType(val)
	for _, expected := range types {
		if actual == expected {
			return nil
		}
		// JSON "integer" type: a number with no fractional part
		if expected == "integer" && actual == "number" {
			if f, ok := val.(float64); ok && f == float64(int64(f)) {
				return nil
			}
		}
	}
	return &ValidationError{
		Field:   path,
		Message: fmt.Sprintf("expected type %s but got %s", strings.Join(types, "|"), actual),
	}
}

func checkEnum(enumKw any, val any, path string) *ValidationError {
	enumArr, ok := enumKw.([]any)
	if !ok {
		return nil
	}
	valJSON, _ := json.Marshal(val)
	for _, option := range enumArr {
		optJSON, _ := json.Marshal(option)
		if string(valJSON) == string(optJSON) {
			return nil
		}
	}
	return &ValidationError{Field: path, Message: fmt.Sprintf("value not in enum")}
}

func jsonType(val any) string {
	switch val.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func fieldPath(parent, field string) string {
	if parent == "" {
		return "." + field
	}
	return parent + "." + field
}

func numFromAny(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}
