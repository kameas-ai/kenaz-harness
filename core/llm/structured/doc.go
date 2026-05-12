// Package structured provides JSON-schema validation and retry logic for
// structured LLM output (structured-output-and-grammar-01KX5R8A WP02).
//
// The validator implements a practical draft-07 subset (type, properties,
// required, enum, items, additionalProperties). Unsupported keywords are
// silently accepted so schemas authored against wider specifications still
// validate. The retry wrapper fires at most one additional call on validation
// failure when StrictValidation is false.
//
// No external dependencies: only stdlib encoding/json and crypto/sha256.
package structured
