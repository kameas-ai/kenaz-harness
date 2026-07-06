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
