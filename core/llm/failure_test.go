package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestClassifyFailure_StatusTable pins the run-failure taxonomy for every
// HTTP status the canonical ClassifyStatus produces (undelivered-message-
// retry, dogfood 2026-10-07): 402 must be user_actionable — the bug that
// motivated this was an out-of-credits OpenRouter account — and only
// 408/425/429/5xx may be transient (auto-retryable).
func TestClassifyFailure_StatusTable(t *testing.T) {
	cases := []struct {
		status    int
		wantClass FailureClass
		wantCode  string
	}{
		{400, FailureUserActionable, FailureCodeInvalidRequest},
		{401, FailureUserActionable, FailureCodeAuthInvalid},
		{402, FailureUserActionable, FailureCodePaymentRequired},
		{403, FailureUserActionable, FailureCodeForbidden},
		{404, FailureUserActionable, FailureCodeModelNotFound},
		{408, FailureTransient, FailureCodeTimeout},
		{422, FailureUserActionable, FailureCodeInvalidRequest},
		{425, FailureTransient, FailureCodeProviderDown},
		{429, FailureTransient, FailureCodeRateLimited},
		{500, FailureTransient, FailureCodeProviderDown},
		{502, FailureTransient, FailureCodeProviderDown},
		{503, FailureTransient, FailureCodeProviderDown},
		{529, FailureTransient, FailureCodeProviderDown},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			err := ClassifyStatus(tc.status, []byte(`{"error":{"message":"upstream says no"}}`))
			// Wrap the way the kernel does by the time driveRun sees it.
			wrapped := fmt.Errorf("loop: node a: body: model: chat: registry stream: %w", err)
			f := ClassifyFailure(wrapped, "openrouter")
			if f.Class != tc.wantClass || f.Code != tc.wantCode {
				t.Fatalf("status %d: got (%s,%s) want (%s,%s)", tc.status, f.Class, f.Code, tc.wantClass, tc.wantCode)
			}
			if f.Status != tc.status {
				t.Fatalf("status lost: got %d want %d", f.Status, tc.status)
			}
			if f.Message != "upstream says no" {
				t.Fatalf("provider message not carried: %q", f.Message)
			}
		})
	}
}

func TestClassifyFailure_OpenRouterOutOfCreditsCopy(t *testing.T) {
	base := ClassifyStatus(402, []byte(`{"error":{"message":"This request requires more credits, or fewer max_tokens.","code":402}}`))
	var pay *ErrPaymentRequired
	if !errors.As(base, &pay) {
		t.Fatalf("402 did not classify as ErrPaymentRequired: %T", base)
	}
	decorated := &ErrProviderPaymentRequired{Provider: "openrouter", ProfileID: "p1", ModelID: "m", Reason: pay.Message, Cause: pay}
	f := ClassifyFailure(fmt.Errorf("wrap: %w", decorated), "")
	if f.Summary != "Out of credits with OpenRouter" {
		t.Fatalf("summary = %q", f.Summary)
	}
	if f.Provider != "openrouter" {
		t.Fatalf("provider from decoration = %q", f.Provider)
	}
	if f.Class != FailureUserActionable {
		t.Fatalf("402 must be user_actionable and not retryable: %+v", f)
	}
}

func TestClassifyFailure_RetryBudgetUnwrapsToLastAttempt(t *testing.T) {
	err := &ErrRetryBudgetExhausted{Attempts: []AttemptOutcome{
		{Attempt: 1, Err: &ErrTransient{Status: 503, Message: "overloaded"}},
		{Attempt: 2, Err: &ErrTransient{Status: 429, Message: "slow down"}},
	}}
	f := ClassifyFailure(err, "anthropic")
	if f.Class != FailureTransient || f.Code != FailureCodeRateLimited || f.Status != 429 {
		t.Fatalf("got %+v", f)
	}
	if !strings.Contains(f.Summary, "Anthropic") {
		t.Fatalf("summary should name the provider: %q", f.Summary)
	}
}

func TestClassifyFailure_NetworkAndUnknown(t *testing.T) {
	f := ClassifyFailure(&ErrTransient{Message: "dial tcp: connection refused"}, "openai")
	if f.Class != FailureTransient || f.Code != FailureCodeNetwork {
		t.Fatalf("network: %+v", f)
	}
	f = ClassifyFailure(fmt.Errorf("x: %w", context.DeadlineExceeded), "openai")
	if f.Class != FailureTransient || f.Code != FailureCodeTimeout {
		t.Fatalf("deadline: %+v", f)
	}
	f = ClassifyFailure(errors.New("something odd"), "openai")
	if f.Class != FailureUnknown {
		t.Fatalf("untyped error must be unknown and not retryable: %+v", f)
	}
	if f.Message != "" {
		t.Fatalf("untyped error must not leak its raw text as the provider message: %q", f.Message)
	}
}

func TestClassifyFailure_PreflightRejectionsAreUserActionable(t *testing.T) {
	for _, err := range []error{
		&UnsupportedModalityError{Modality: "image", Model: "m"},
		&ErrUnsupportedFeature{ModelID: "m", Feature: "seed"},
		&ErrCapabilityUnsupported{Provider: "p", Model: "m"},
	} {
		f := ClassifyFailure(err, "openai")
		if f.Class != FailureUserActionable || f.Code != FailureCodeUnsupported {
			t.Fatalf("%T: %+v", err, f)
		}
	}
}

// TestSanitizeProviderMessage_RedactsCredentialShapes: a provider message
// reaches a chat bubble and a database row, so nothing shaped like a key
// or an Authorization header may survive (spec: never echo API keys).
func TestSanitizeProviderMessage_RedactsCredentialShapes(t *testing.T) {
	secrets := []string{
		"sk-or-v1-0123456789abcdef0123456789abcdef",
		"sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAA",
		"sk-proj-BBBBBBBBBBBBBBBBBBBBBBBB",
		"AIzaSyA1234567890abcdefghijklmnopqrstu",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig",
	}
	for _, s := range secrets {
		in := "Invalid API key provided: " + s + " (check your dashboard)"
		out := SanitizeProviderMessage(in)
		if strings.Contains(out, s) {
			t.Fatalf("secret survived sanitization: %q", out)
		}
	}
	out := SanitizeProviderMessage("rejected header Authorization: Bearer abcdefghijklmnop12345")
	if strings.Contains(out, "abcdefghijklmnop12345") {
		t.Fatalf("authorization header survived: %q", out)
	}
	long := strings.Repeat("x", 1000)
	if got := []rune(SanitizeProviderMessage(long)); len(got) > failureMessageMaxRunes+1 {
		t.Fatalf("not capped: %d runes", len(got))
	}
	// Through ClassifyFailure as well — the field the wire carries.
	f := ClassifyFailure(&ErrAuth{Status: 401, Message: "bad key sk-or-v1-0123456789abcdef0123"}, "openrouter")
	if strings.Contains(f.Message, "0123456789abcdef0123") {
		t.Fatalf("ClassifyFailure leaked a key: %q", f.Message)
	}
}
