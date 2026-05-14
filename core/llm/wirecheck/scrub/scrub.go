// Package scrub provides a deterministic scrubber for wire-golden fixtures.
//
// The scrubber normalises SSE fixture files and JSON request bodies so that
// two consecutive -update runs against the same upstream produce byte-identical
// output. It strips provider-side ids, timestamps, anti-abuse headers, and
// trace fields, replacing them with deterministic placeholders.
//
// Scrubber is called from:
//   - The -update mode path (wirecheck.UpdateEnabled = true) after recording
//     a fresh response from the real API.
//   - The nightly cron auto-PR generator (wire-golden-live.yml) before writing
//     updated response.sse files.
//
// Rules applied (in order):
//  1. JSON id normalisation: id, request_id, response_id, tool_use_id,
//     message_id, system_fingerprint → wire-golden-id-<n>.
//  2. Timestamp zeroing: created, timestamp, created_at → 0 (number) or
//     "1970-01-01T00:00:00Z" (string).
//  3. Anti-abuse / provider-trace stripping: x-request-id, cf-ray,
//     x-amzn-trace-id, openai-organization, anthropic-organization-id
//     dropped from JSON objects.
//  4. Unicode normalisation: NFC.
//  5. Trailing-newline normalisation: file ends with a single \n.
//  6. Size guard: rejects fixtures > 4 KiB with a clear error.
package scrub

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const maxFixtureBytes = 4 * 1024 // 4 KiB per fixture

// ErrFixtureTooLarge is returned by ScrubSSE / ScrubJSON when the input
// exceeds maxFixtureBytes. Truncate scenarios to ≤ 4 KiB SSE; long-tail
// scenarios use a "first 20 events + finish" pattern.
var ErrFixtureTooLarge = errors.New("scrub: fixture too large (> 4 KiB) — truncate the SSE trace to ≤ 20 events + finish before scrubbing")

// idFields is the set of JSON object keys whose values are replaced with
// wire-golden-id-<n>. Matched case-insensitively by lower-cased key.
var idFields = map[string]bool{
	"id":              true,
	"request_id":      true,
	"response_id":     true,
	"tool_use_id":     true,
	"message_id":      true,
	"system_fingerprint": true,
}

// tsFields is the set of JSON object keys whose values are zeroed.
var tsFields = map[string]bool{
	"created":    true,
	"timestamp":  true,
	"created_at": true,
}

// antiAbuseFields are stripped entirely from JSON objects.
var antiAbuseFields = map[string]bool{
	"x-request-id":             true,
	"cf-ray":                   true,
	"x-amzn-trace-id":          true,
	"openai-organization":      true,
	"anthropic-organization-id": true,
}

// ScrubSSE scrubs a wire-golden SSE fixture (recorded upstream response).
// Each data: line's JSON payload has id normalisation, timestamp zeroing, and
// anti-abuse field stripping applied. The resulting bytes are unicode-NFC
// normalised and end with exactly one newline.
//
// Returns ErrFixtureTooLarge if len(input) > 4 KiB.
func ScrubSSE(input []byte) ([]byte, error) {
	if len(input) > maxFixtureBytes {
		return nil, ErrFixtureTooLarge
	}
	counter := &idCounter{}
	lines := strings.Split(string(input), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		scrubbed, err := scrubSSELine(line, counter)
		if err != nil {
			return nil, fmt.Errorf("scrub: line %q: %w", line, err)
		}
		out = append(out, scrubbed)
	}
	result := strings.Join(out, "\n")
	// Trailing-newline normalisation: exactly one trailing \n.
	result = strings.TrimRight(result, "\n") + "\n"
	// Unicode NFC normalisation.
	result = norm.NFC.String(result)
	if !utf8.ValidString(result) {
		return nil, fmt.Errorf("scrub: result is not valid UTF-8")
	}
	return []byte(result), nil
}

// ScrubJSON scrubs a wire-golden JSON fixture (hand-authored request body or
// captured JSON). Applies id normalisation, timestamp zeroing, and anti-abuse
// stripping. Returns normalised JSON (compact, single trailing newline).
//
// Returns ErrFixtureTooLarge if len(input) > 4 KiB.
func ScrubJSON(input []byte) ([]byte, error) {
	if len(input) > maxFixtureBytes {
		return nil, ErrFixtureTooLarge
	}
	counter := &idCounter{}
	var doc any
	if err := json.Unmarshal(input, &doc); err != nil {
		return nil, fmt.Errorf("scrub: JSON unmarshal: %w", err)
	}
	scrubValue(doc, counter)
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("scrub: JSON marshal: %w", err)
	}
	result := norm.NFC.String(string(out))
	return []byte(result + "\n"), nil
}

// ---- internal helpers ----

// idCounter tracks how many id-field replacements have been emitted so far,
// producing deterministic wire-golden-id-<n> values per fixture.
type idCounter struct{ n int }

func (c *idCounter) next() string {
	c.n++
	return fmt.Sprintf("wire-golden-id-%d", c.n)
}

// scrubSSELine processes one line of an SSE fixture. Lines starting with
// "data: " have their JSON payload scrubbed; all other lines are passed
// through unchanged.
func scrubSSELine(line string, counter *idCounter) (string, error) {
	const prefix = "data: "
	if !strings.HasPrefix(line, prefix) {
		return line, nil
	}
	payload := line[len(prefix):]
	if payload == "[DONE]" {
		return line, nil
	}
	var doc any
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		// Non-JSON data: line (e.g. heartbeat) — pass through.
		return line, nil
	}
	scrubValue(doc, counter)
	out, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("scrub SSE line re-marshal: %w", err)
	}
	return prefix + string(out), nil
}

// scrubValue recursively applies all scrub rules to a decoded JSON value.
func scrubValue(v any, counter *idCounter) {
	switch typed := v.(type) {
	case map[string]any:
		scrubObject(typed, counter)
	case []any:
		for _, elem := range typed {
			scrubValue(elem, counter)
		}
	}
}

// scrubObject applies id normalisation, timestamp zeroing, anti-abuse stripping,
// and recursively descends into nested objects and arrays.
// Keys are processed in sorted order so the id counter assignments are
// deterministic across multiple runs on the same input.
func scrubObject(obj map[string]any, counter *idCounter) {
	// Collect and sort keys for deterministic processing order.
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lower := strings.ToLower(k)
		if antiAbuseFields[lower] {
			delete(obj, k)
			continue
		}
		v := obj[k]
		if idFields[lower] {
			// Replace with deterministic placeholder, preserving the key.
			obj[k] = counter.next()
			continue
		}
		if tsFields[lower] {
			// Zero the timestamp value; preserve the type shape.
			switch v.(type) {
			case float64:
				obj[k] = float64(0)
			case string:
				obj[k] = "1970-01-01T00:00:00Z"
			}
			continue
		}
		// Recurse into nested structures.
		scrubValue(v, counter)
	}
}
