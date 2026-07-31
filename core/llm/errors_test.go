package llm

import (
	"errors"
	"strings"
	"testing"
)

func TestErrRetryBudgetExhausted_SurfacesLastCause(t *testing.T) {
	transient := &ErrTransient{Status: 429, Message: "rate limited"}
	e := &ErrRetryBudgetExhausted{Attempts: []AttemptOutcome{
		{Attempt: 1, Err: &ErrTransient{Status: 500, Message: "server error"}},
		{Attempt: 2, Err: transient},
	}}

	msg := e.Error()
	if !strings.Contains(msg, "2 attempts") {
		t.Fatalf("message should report attempt count, got: %q", msg)
	}
	if !strings.Contains(msg, "rate limited") || !strings.Contains(msg, "429") {
		t.Fatalf("message should surface the last attempt's cause, got: %q", msg)
	}

	// Unwrap must reach the underlying typed error so errors.As works for
	// callers that want to branch on the real provider fault.
	var got *ErrTransient
	if !errors.As(e, &got) {
		t.Fatal("errors.As should reach the wrapped *ErrTransient via Unwrap")
	}
	if got.Status != 429 {
		t.Fatalf("expected to unwrap the last attempt (429), got status %d", got.Status)
	}
}

func TestErrRetryBudgetExhausted_NoAttempts(t *testing.T) {
	e := &ErrRetryBudgetExhausted{}
	if !strings.Contains(e.Error(), "0 attempts") {
		t.Fatalf("expected a count-only message with no attempts, got: %q", e.Error())
	}
	if e.Unwrap() != nil {
		t.Fatal("Unwrap should be nil when no attempt carried an error")
	}
}

// TestErrAuth_Friendly and TestErrInvalidRequest_Friendly are WP02 of
// tool-error-legibility-01PMDL02: extending the Friendly() pattern
// (previously only on the attachment-error family) to ErrAuth and
// ErrInvalidRequest so auth failures and rejected requests read as
// distinct failure legs rather than one bare error string.
func TestErrAuth_Friendly(t *testing.T) {
	e := &ErrAuth{Status: 401, Message: "invalid x-api-key"}
	got := e.Friendly()
	if !strings.Contains(got, "401") {
		t.Errorf("Friendly() should surface the status code, got: %q", got)
	}
	if !strings.Contains(got, "invalid x-api-key") {
		t.Errorf("Friendly() should surface the provider message, got: %q", got)
	}
	if !strings.Contains(strings.ToLower(got), "api key") {
		t.Errorf("Friendly() should give an actionable auth hint, got: %q", got)
	}
	if got == e.Error() {
		t.Errorf("Friendly() should differ from the raw Error() string")
	}
}

func TestErrInvalidRequest_Friendly(t *testing.T) {
	e := &ErrInvalidRequest{Status: 400, Message: "unknown parameter: foo"}
	got := e.Friendly()
	if !strings.Contains(got, "400") {
		t.Errorf("Friendly() should surface the status code, got: %q", got)
	}
	if !strings.Contains(got, "unknown parameter: foo") {
		t.Errorf("Friendly() should surface the provider message, got: %q", got)
	}
	if got == e.Error() {
		t.Errorf("Friendly() should differ from the raw Error() string")
	}
}

// TestErrAuth_ErrInvalidRequest_FriendlyTextDiffers pins that the two
// new Friendly() renderers read as distinct failure legs, not the same
// templated text with a different label swapped in — the whole point
// of WP02's taxonomy differentiation.
func TestErrAuth_ErrInvalidRequest_FriendlyTextDiffers(t *testing.T) {
	auth := (&ErrAuth{Status: 401, Message: "bad key"}).Friendly()
	invalid := (&ErrInvalidRequest{Status: 400, Message: "bad key"}).Friendly()
	if auth == invalid {
		t.Fatalf("ErrAuth and ErrInvalidRequest Friendly() text must not be identical: %q", auth)
	}
}
