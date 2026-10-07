package gemini

import (
	"errors"
	"testing"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestClassifyStatus_402IsPaymentRequired: an insufficient-credits 402
// must type as ErrPaymentRequired (user_actionable, never auto-retried),
// not fall into the ErrInvalidRequest default whose copy says "check the
// request shape" (undelivered-message-retry, dogfood 2026-10-07).
func TestClassifyStatus_402IsPaymentRequired(t *testing.T) {
	err := classifyStatus(402, []byte(`{"error":{"message":"insufficient credits"}}`))
	var pay *llm.ErrPaymentRequired
	if !errors.As(err, &pay) {
		t.Fatalf("402 classified as %T, want *llm.ErrPaymentRequired", err)
	}
	if pay.Status != 402 {
		t.Fatalf("status = %d", pay.Status)
	}
	f := llm.ClassifyFailure(err, Kind)
	if f.Class != llm.FailureUserActionable || f.Retryable() {
		t.Fatalf("402 must be user_actionable: %+v", f)
	}
	for status, want := range map[int]llm.FailureClass{
		401: llm.FailureUserActionable,
		429: llm.FailureTransient,
		503: llm.FailureTransient,
		400: llm.FailureUserActionable,
	} {
		if got := llm.ClassifyFailure(classifyStatus(status, nil), Kind).Class; got != want {
			t.Fatalf("status %d: class %s want %s", status, got, want)
		}
	}
}
