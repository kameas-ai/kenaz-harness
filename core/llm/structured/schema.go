package structured

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SchemaHash returns the SHA-256 hex digest of schema bytes. Returns ""
// when schema is nil or empty (used in audit payloads where no schema
// was supplied — Mode="json" or Mode="grammar").
//
// The hash is computed over the raw JSON bytes as supplied; callers must
// not canonicalize before passing to ensure consistent hashing per call site.
func SchemaHash(schema json.RawMessage) string {
	if len(schema) == 0 {
		return ""
	}
	sum := sha256.Sum256(schema)
	return hex.EncodeToString(sum[:])
}

// InjectAdditionalProperties returns a copy of schema with
// "additionalProperties": false injected at the top level when the key is
// absent. OpenAI's structured-output API requires this flag when strict=true;
// without it the API returns a 400 (structured-output-and-grammar-01KX5R8A §2.6).
//
// Only the top-level object is modified; nested properties are left as-is.
// If the schema cannot be parsed as a JSON object, it is returned unchanged.
func InjectAdditionalProperties(schema json.RawMessage) (json.RawMessage, error) {
	if len(schema) == 0 {
		return schema, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(schema, &doc); err != nil {
		// Not an object schema — return unchanged (e.g. a $ref or array schema).
		return schema, nil
	}
	if _, exists := doc["additionalProperties"]; exists {
		// Already set — respect caller's value.
		return schema, nil
	}
	doc["additionalProperties"] = false
	out, err := json.Marshal(doc)
	if err != nil {
		return schema, err
	}
	return out, nil
}
