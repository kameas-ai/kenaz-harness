package scrub_test

import (
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/llm/wirecheck/scrub"
)

// ── ScrubSSE golden tests ─────────────────────────────────────────────────────

// TestScrubSSE_IDNormalisation verifies that id, request_id, and message_id
// fields in SSE data lines are replaced with deterministic wire-golden-id-<n>
// placeholders.
func TestScrubSSE_IDNormalisation(t *testing.T) {
	input := `data: {"type":"message_start","message":{"id":"msg_abc123","role":"assistant","model":"claude-3","usage":{"input_tokens":10,"output_tokens":1}}}

data: {"type":"message_stop"}
`
	got, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("ScrubSSE: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "msg_abc123") {
		t.Error("ScrubSSE: id 'msg_abc123' not scrubbed")
	}
	if !strings.Contains(s, "wire-golden-id-1") {
		t.Errorf("ScrubSSE: expected wire-golden-id-1, got:\n%s", s)
	}
}

// TestScrubSSE_TimestampZeroing verifies that created and timestamp fields are
// replaced with zero values.
func TestScrubSSE_TimestampZeroing(t *testing.T) {
	input := `data: {"type":"chunk","created":1735000000,"timestamp":"2025-01-01T12:00:00Z"}
`
	got, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("ScrubSSE: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "1735000000") {
		t.Error("ScrubSSE: numeric timestamp not zeroed")
	}
	if strings.Contains(s, "2025-01-01T12:00:00Z") {
		t.Error("ScrubSSE: string timestamp not zeroed")
	}
	if !strings.Contains(s, `"created":0`) {
		t.Errorf("ScrubSSE: expected \"created\":0, got:\n%s", s)
	}
	if !strings.Contains(s, "1970-01-01T00:00:00Z") {
		t.Errorf("ScrubSSE: expected 1970-01-01T00:00:00Z, got:\n%s", s)
	}
}

// TestScrubSSE_AntiAbuseStripping verifies that x-request-id, cf-ray, and
// similar fields are removed entirely from SSE data payloads.
func TestScrubSSE_AntiAbuseStripping(t *testing.T) {
	input := `data: {"type":"chunk","text":"hello","x-request-id":"req-xyz","cf-ray":"abc123"}
`
	got, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("ScrubSSE: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "x-request-id") {
		t.Error("ScrubSSE: x-request-id not stripped")
	}
	if strings.Contains(s, "cf-ray") {
		t.Error("ScrubSSE: cf-ray not stripped")
	}
	if strings.Contains(s, "req-xyz") {
		t.Error("ScrubSSE: x-request-id value not stripped")
	}
}

// TestScrubSSE_NonDataLinesPassThrough verifies that non-data lines (event:,
// blank lines) are passed through unchanged.
func TestScrubSSE_NonDataLinesPassThrough(t *testing.T) {
	input := `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}

`
	got, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("ScrubSSE: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, "event: content_block_delta") {
		t.Error("ScrubSSE: event: line dropped")
	}
	if !strings.Contains(s, `"text":"hello"`) {
		t.Error("ScrubSSE: text value scrubbed when it should not have been")
	}
}

// TestScrubSSE_DoneLinePassThrough verifies that data: [DONE] lines are not
// JSON-parsed (they are not JSON).
func TestScrubSSE_DoneLinePassThrough(t *testing.T) {
	input := `data: {"type":"chunk"}

data: [DONE]
`
	got, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("ScrubSSE: %v", err)
	}
	if !strings.Contains(string(got), "data: [DONE]") {
		t.Error("ScrubSSE: data: [DONE] line altered")
	}
}

// TestScrubSSE_TrailingNewline verifies that the output always ends with
// exactly one trailing newline regardless of the input.
func TestScrubSSE_TrailingNewline(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"no trailing newline", `data: {"type":"chunk"}`},
		{"one trailing newline", "data: {\"type\":\"chunk\"}\n"},
		{"multiple trailing newlines", "data: {\"type\":\"chunk\"}\n\n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scrub.ScrubSSE([]byte(tc.input))
			if err != nil {
				t.Fatalf("ScrubSSE: %v", err)
			}
			s := string(got)
			if !strings.HasSuffix(s, "\n") {
				t.Errorf("output does not end with \\n: %q", s)
			}
			trimmed := strings.TrimRight(s, "\n")
			if strings.HasSuffix(trimmed, "\n") {
				t.Errorf("output ends with more than one \\n: %q", s)
			}
		})
	}
}

// TestScrubSSE_Determinism verifies that two consecutive calls produce identical
// output (the core requirement: deterministic IDs).
func TestScrubSSE_Determinism(t *testing.T) {
	input := `data: {"id":"msg_abc","type":"message_start","message":{"id":"inner_xyz","role":"assistant"}}

data: {"type":"message_stop"}
`
	got1, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("first ScrubSSE: %v", err)
	}
	got2, err := scrub.ScrubSSE([]byte(input))
	if err != nil {
		t.Fatalf("second ScrubSSE: %v", err)
	}
	if string(got1) != string(got2) {
		t.Errorf("ScrubSSE is not deterministic:\nfirst:  %q\nsecond: %q", got1, got2)
	}
}

// TestScrubSSE_TooLarge verifies that ErrFixtureTooLarge is returned for
// inputs exceeding 4 KiB.
func TestScrubSSE_TooLarge(t *testing.T) {
	input := strings.Repeat("data: {\"type\":\"chunk\",\"text\":\"x\"}\n", 200) // > 4 KiB
	_, err := scrub.ScrubSSE([]byte(input))
	if err == nil {
		t.Fatal("ScrubSSE: expected ErrFixtureTooLarge, got nil")
	}
	if !strings.Contains(err.Error(), "4 KiB") {
		t.Errorf("ScrubSSE: error does not mention 4 KiB: %v", err)
	}
}

// ── ScrubJSON golden tests ────────────────────────────────────────────────────

// TestScrubJSON_IDNormalisation verifies that id fields in JSON are replaced.
func TestScrubJSON_IDNormalisation(t *testing.T) {
	input := `{"model":"gpt-4","id":"chatcmpl-abc","system_fingerprint":"fp_xyz","messages":[]}`
	got, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("ScrubJSON: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "chatcmpl-abc") {
		t.Error("ScrubJSON: id value not scrubbed")
	}
	if strings.Contains(s, "fp_xyz") {
		t.Error("ScrubJSON: system_fingerprint value not scrubbed")
	}
	if !strings.Contains(s, "wire-golden-id-") {
		t.Errorf("ScrubJSON: no wire-golden-id placeholder found: %s", s)
	}
}

// TestScrubJSON_NestedObjects verifies that scrubbing descends into nested
// objects and arrays.
func TestScrubJSON_NestedObjects(t *testing.T) {
	input := `{"choices":[{"message":{"id":"msg-1","role":"assistant","content":"hi"},"finish_reason":"stop"}],"created":1735000000}`
	got, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("ScrubJSON: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "msg-1") {
		t.Error("ScrubJSON: nested id not scrubbed")
	}
	if strings.Contains(s, "1735000000") {
		t.Error("ScrubJSON: top-level created not zeroed")
	}
	// Non-id fields should be preserved.
	if !strings.Contains(s, "assistant") {
		t.Error("ScrubJSON: role='assistant' dropped unexpectedly")
	}
	if !strings.Contains(s, `"content":"hi"`) {
		t.Error("ScrubJSON: content dropped unexpectedly")
	}
}

// TestScrubJSON_AntiAbuseStripping verifies that anti-abuse fields are removed.
func TestScrubJSON_AntiAbuseStripping(t *testing.T) {
	input := `{"model":"gpt-4","openai-organization":"org-xyz","messages":[]}`
	got, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("ScrubJSON: %v", err)
	}
	s := string(got)
	if strings.Contains(s, "openai-organization") {
		t.Error("ScrubJSON: openai-organization key not stripped")
	}
	if strings.Contains(s, "org-xyz") {
		t.Error("ScrubJSON: openai-organization value not stripped")
	}
}

// TestScrubJSON_TrailingNewline verifies the output ends with exactly one \n.
func TestScrubJSON_TrailingNewline(t *testing.T) {
	input := `{"model":"gpt-4"}`
	got, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("ScrubJSON: %v", err)
	}
	s := string(got)
	if !strings.HasSuffix(s, "\n") {
		t.Errorf("ScrubJSON output does not end with \\n: %q", s)
	}
	trimmed := strings.TrimRight(s, "\n")
	if strings.HasSuffix(trimmed, "\n") {
		t.Errorf("ScrubJSON output ends with multiple \\n: %q", s)
	}
}

// TestScrubJSON_Determinism verifies two consecutive calls produce byte-identical output.
func TestScrubJSON_Determinism(t *testing.T) {
	input := `{"id":"req-1","model":"gpt-4","choices":[{"message":{"id":"msg-2","content":"hello"}}]}`
	got1, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("first ScrubJSON: %v", err)
	}
	got2, err := scrub.ScrubJSON([]byte(input))
	if err != nil {
		t.Fatalf("second ScrubJSON: %v", err)
	}
	if string(got1) != string(got2) {
		t.Errorf("ScrubJSON is not deterministic:\nfirst:  %q\nsecond: %q", got1, got2)
	}
}

// TestScrubJSON_TooLarge verifies that ErrFixtureTooLarge is returned for
// inputs > 4 KiB.
func TestScrubJSON_TooLarge(t *testing.T) {
	input := `{"data":"` + strings.Repeat("x", 5000) + `"}`
	_, err := scrub.ScrubJSON([]byte(input))
	if err == nil {
		t.Fatal("ScrubJSON: expected ErrFixtureTooLarge, got nil")
	}
}
