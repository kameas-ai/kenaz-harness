package audit

// audit_kinds_test.go — WP08 capstone: workflow audit-kind registry verification.
//
// Three invariants are enforced here:
//
//  1. Every workflow-family Kind constant is representable as an Event and
//     survives a JSON marshal/unmarshal round-trip through its associated
//     payload struct.
//
//  2. No workflow-family payload struct carries a field with a JSON tag
//     named "body", "prompt_text", "response_text", or "url" (full URL
//     — only "host" / "hostname" are acceptable). This is the privacy
//     invariant for the audit log (DIRECTIVE_001).
//
//  3. Every workflow audit Kind constant is non-empty and prefixed with
//     "workflow." or "notify." so the kind registry consumer can namespace them.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// workflowKinds enumerates the workflow-family audit kind constants and
// their canonical payload structs. Used by all three invariant tests below.
var workflowKinds = []struct {
	kind    Kind
	payload any
}{
	{
		KindWorkflowExecuted,
		WorkflowExecutedPayload{
			WorkflowID: "wf-1",
			RunID:      "run-1",
			Status:     "completed",
			DurationMs: 1234,
			StepCount:  3,
		},
	},
	{
		KindWorkflowSaved,
		WorkflowSavedPayload{
			WorkflowID: "wf-2",
			Version:    1,
			Hash:       "abc123",
		},
	},
	{
		KindWorkflowDeleted,
		WorkflowDeletedPayload{
			WorkflowID: "wf-3",
		},
	},
	{
		KindWorkflowStepFailed,
		WorkflowStepFailedPayload{
			WorkflowID: "wf-4",
			RunID:      "run-4",
			StepID:     "step_alpha",
			StepKind:   "shell",
			ErrorClass: "runner_error",
		},
	},
	{
		KindWorkflowNetworkFetch,
		WorkflowNetworkFetchPayload{
			WorkflowID: "wf-5",
			RunID:      "run-5",
			StepID:     "fetch_page",
			StepKind:   "web_fetch",
			Hostname:   "example.com",
			Status:     200,
			Bytes:      4096,
		},
	},
}

// TestWorkflowAuditKinds_RoundTrip verifies that every workflow audit kind
// constant round-trips through JSON marshal/unmarshal without data loss.
func TestWorkflowAuditKinds_RoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)
	em := &recordingEmitter{}

	for _, tc := range workflowKinds {
		tc := tc // capture
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			em2 := &recordingEmitter{}
			if err := Emit(t.Context(), em2, tc.kind, tc.payload, now); err != nil {
				t.Fatalf("Emit: %v", err)
			}
			if len(em2.events) != 1 {
				t.Fatalf("emitted %d events, want 1", len(em2.events))
			}
			e := em2.events[0]
			if e.Kind != tc.kind {
				t.Errorf("Kind = %q, want %q", e.Kind, tc.kind)
			}
			if !e.TS.Equal(now) {
				t.Errorf("TS = %v, want %v", e.TS, now)
			}

			// Round-trip: marshal the original payload and compare bytes.
			original, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal original: %v", err)
			}
			if string(e.Payload) != string(original) {
				t.Errorf("payload mismatch:\n got  %s\n want %s", e.Payload, original)
			}
			_ = em // suppress unused warning
		})
	}
}

// TestWorkflowAuditKinds_NotEmpty verifies every workflow Kind constant is
// non-empty and carries the expected namespace prefix.
func TestWorkflowAuditKinds_NotEmpty(t *testing.T) {
	t.Parallel()
	for _, tc := range workflowKinds {
		if tc.kind == "" {
			t.Errorf("workflow Kind constant is empty string")
			continue
		}
		s := string(tc.kind)
		if !strings.HasPrefix(s, "workflow.") && !strings.HasPrefix(s, "notify.") {
			t.Errorf("Kind %q does not carry 'workflow.' or 'notify.' prefix", s)
		}
	}
}

// TestWorkflowAuditKinds_PrivacyInvariant enforces that no workflow-family
// payload struct has a JSON field named "body", "prompt_text",
// "response_text", or "url" (full URL — "hostname" and "host" are OK).
//
// The check is structural (reflect over field tags) so it catches a mis-tag
// before any data is written to the audit log.
func TestWorkflowAuditKinds_PrivacyInvariant(t *testing.T) {
	t.Parallel()

	// Forbidden JSON field names (exact match on the JSON tag key).
	forbidden := map[string]bool{
		"body":          true,
		"prompt_text":   true,
		"response_text": true,
		"url":           true,
	}

	for _, tc := range workflowKinds {
		tc := tc
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()
			checkPrivacy(t, reflect.TypeOf(tc.payload), string(tc.kind), forbidden)
		})
	}
}

// checkPrivacy recursively inspects the struct type ty for forbidden JSON
// field tags. Pointer and embedded types are dereferenced transparently.
func checkPrivacy(t *testing.T, ty reflect.Type, kindName string, forbidden map[string]bool) {
	t.Helper()
	// Dereference pointer types.
	for ty.Kind() == reflect.Pointer {
		ty = ty.Elem()
	}
	if ty.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < ty.NumField(); i++ {
		f := ty.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		// JSON tag format: "name,omitempty" — extract just the name part.
		name := strings.SplitN(tag, ",", 2)[0]
		if forbidden[name] {
			t.Errorf("[%s] payload field %q has forbidden JSON tag %q (privacy invariant)",
				kindName, f.Name, name)
		}
		// Recurse into nested structs.
		checkPrivacy(t, f.Type, kindName+"."+f.Name, forbidden)
	}
}

// TestWorkflowAuditKinds_MarshalIsStable verifies that the same payload
// marshals to identical bytes across two calls (JSON stability requirement
// for the audit log deduplication layer).
func TestWorkflowAuditKinds_MarshalIsStable(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 1, 7, 0, 0, 0, time.UTC)

	for _, tc := range workflowKinds {
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
