package chain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Canonical encodes v as a stable, byte-deterministic JSON
// representation suitable for hashing.
//
// Rules:
//   - Object keys sorted lexicographically (Unicode codepoint order on
//     UTF-8 bytes; matches RFC 8785 JCS sort).
//   - No insignificant whitespace.
//   - Strings UTF-8; same escaping rules as encoding/json's compact
//     output.
//   - Numbers serialized via strconv.FormatFloat(_, 'f', -1, 64) so
//     1, 1.0, and 1e0 collapse to "1"; integers keep integer form.
//   - Booleans, null, arrays — straightforward.
//
// Inputs that cannot be serialized to canonical JSON return an error.
// Note: per plan §9 we document this as a "documented superset" of
// RFC 8785; stricter conformance can be added once auditors push back.
func Canonical(v any) ([]byte, error) {
	// Round-trip through json.Marshal first to handle struct tags,
	// time.Time, json.Marshaler, etc.
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("chain: marshal: %w", err)
	}
	var anyVal any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&anyVal); err != nil {
		return nil, fmt.Errorf("chain: re-decode: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, anyVal); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if x {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		writeString(buf, x)
	case json.Number:
		// json.Number preserves the literal form, but we want a
		// canonical form. Try int first, then float.
		if i, err := x.Int64(); err == nil {
			buf.WriteString(strconv.FormatInt(i, 10))
			return nil
		}
		if f, err := x.Float64(); err == nil {
			buf.WriteString(strconv.FormatFloat(f, 'f', -1, 64))
			return nil
		}
		// Fallback: raw literal.
		buf.WriteString(x.String())
	case float64:
		buf.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
	case int:
		buf.WriteString(strconv.FormatInt(int64(x), 10))
	case int64:
		buf.WriteString(strconv.FormatInt(x, 10))
	case []any:
		buf.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, x[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("chain: unsupported canonical type %T", v)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) {
	// Reuse encoding/json's escaping.
	enc, err := json.Marshal(s)
	if err != nil {
		// json.Marshal of a string can only fail on unrepresentable
		// runes; in that case fall back to a raw quote.
		buf.WriteByte('"')
		buf.WriteString(s)
		buf.WriteByte('"')
		return
	}
	buf.Write(enc)
}
