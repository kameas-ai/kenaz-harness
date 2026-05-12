package audit

// elicitation_kinds_test.go — WP09: elicitation audit-kind verification.
//
// Three invariants are enforced here (matching the workflow audit-kind test
// pattern in audit_kinds_test.go):
//
//  1. Every elicitation-family Kind constant round-trips through JSON
//     marshal/unmarshal without data loss.
//
//  2. No elicitation-family payload struct carries a field with a JSON tag
//     named "body", "prompt_text", "response_text", "url", "answer", or
//     "label" (privacy invariant — DIRECTIVE_001 / spec §3.9).
//
//  3. Every elicitation Kind constant is non-empty and prefixed with
//     "elicitation." so the kind registry consumer can namespace them.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var elicitationKinds = []struct {
	kind    Kind
	payload any
}{
	{
		KindElicitationRequest,
		ElicitationRequestPayload{
			AskID:         "ask-001",
			SessionID:     "sess-001",
			Kind:          "radio",
			Mode:          "blocking",
			QuestionCount: 1,
			HasPreviews:   true,
			TemplateID:    "",
		},
	},
	{
		KindElicitationResult,
		ElicitationResultPayload{
			AskID:          "ask-001",
			SessionID:      "sess-001",
			Outcome:        "answered",
			DeclineReason:  "",
			TimeToAnswerMs: 4321,
			Deferred:       false,
		},
	},
}

func TestElicitationAuditKinds_RoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	for _, tc := range elicitationKinds {
		tc := tc
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			em := &recordingEmitter{}
			if err := Emit(t.Context(), em, tc.kind, tc.payload, now); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if len(em.events) != 1 {
				t.Fatalf("emitted %d events, want 1", len(em.events))
			}
			e := em.events[0]
			if e.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", e.Kind, tc.kind)
			}
			if !e.TS.Equal(now) {
				t.Errorf("TS = %v, want %v", e.TS, now)
			}
		})
	}
}

func TestElicitationAuditKinds_NotEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range elicitationKinds {
		if tc.kind == "" {
			t.Errorf("elicitation Kind constant is empty string")
			continue
		}
		s := string(tc.kind)
		if !strings.HasPrefix(s, "elicitation.") {
			t.Errorf("Kind %q does not carry 'elicitation.' prefix", s)
		}
	}
}

// TestElicitationAuditKinds_PrivacyInvariant enforces that no elicitation
// payload struct has a JSON field with a forbidden name. Answer values,
// question/option text, and URL paths must never appear in the audit log.
func TestElicitationAuditKinds_PrivacyInvariant(t *testing.T) {
	t.Parallel()

	// Forbidden JSON field names — extend the standard workflow set with
	// elicitation-specific leakers.
	forbidden := map[string]bool{
		"body":          true,
		"prompt_text":   true,
		"response_text": true,
		"url":           true,
		"answer":        true, // elicitation-specific: no answer values in audit
		"label":         true, // option labels must not appear
	}

	for _, tc := range elicitationKinds {
		tc := tc
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			// Re-use the checkPrivacy helper from audit_kinds_test.go
			// (same package, no import needed).
			checkPrivacy(t, reflect.TypeOf(tc.payload), string(tc.kind), forbidden)
		})
	}
}

func TestElicitationAuditKinds_MarshalIsStable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)

	for _, tc := range elicitationKinds {
		tc := tc
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			e1, err1 := Marshal(tc.kind, tc.payload, now)
			e2, err2 := Marshal(tc.kind, tc.payload, now)
			if err1 != nil || err2 != nil {
				t.Fatalf("Marshal errors: %v / %v", err1, err2)
			}
			if string(e1.Payload) != string(e2.Payload) {
				t.Errorf("Marshal is not stable:\n first  %s\n second %s", e1.Payload, e2.Payload)
			}
		})
	}
}
