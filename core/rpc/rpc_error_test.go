package rpc

import (
	"errors"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// TestMapLLMError_Nil verifies that a nil error returns a nil *RPCError.
func TestMapLLMError_Nil(t *testing.T) {
	if got := MapLLMError(nil); got != nil {
		t.Errorf("MapLLMError(nil) = %+v, want nil", got)
	}
}

// TestMapLLMError_Auth verifies ErrAuth maps to code "auth", not retryable.
func TestMapLLMError_Auth(t *testing.T) {
	err := &corellm.ErrAuth{Status: 401, Message: "invalid api key"}
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(ErrAuth) returned nil")
	}
	if got.Code != "auth" {
		t.Errorf("code = %q, want %q", got.Code, "auth")
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
	if got.Hint == "" {
		t.Error("expected non-empty Hint for auth error")
	}
}

// TestMapLLMError_ProviderAuthFailed verifies ErrProviderAuthFailed (which
// wraps ErrAuth) also maps to "auth".
func TestMapLLMError_ProviderAuthFailed(t *testing.T) {
	err := &corellm.ErrProviderAuthFailed{
		Provider: "anthropic",
		Reason:   "API key rejected",
		Cause:    &corellm.ErrAuth{Status: 401},
	}
	got := MapLLMError(err)
	if got == nil || got.Code != "auth" {
		t.Fatalf("MapLLMError(ErrProviderAuthFailed) = %+v, want code=auth", got)
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

// TestMapLLMError_Transient verifies ErrTransient maps to "transient",
// retryable.
func TestMapLLMError_Transient(t *testing.T) {
	err := &corellm.ErrTransient{Status: 503, Message: "service unavailable"}
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(ErrTransient) returned nil")
	}
	if got.Code != "transient" {
		t.Errorf("code = %q, want %q", got.Code, "transient")
	}
	if !got.Retryable {
		t.Error("Retryable = false, want true")
	}
}

// TestMapLLMError_RetryBudgetExhausted verifies ErrRetryBudgetExhausted
// maps to "budget_exhausted", not retryable.
func TestMapLLMError_RetryBudgetExhausted(t *testing.T) {
	inner := &corellm.ErrTransient{Status: 429, Message: "rate limited"}
	err := &corellm.ErrRetryBudgetExhausted{
		Attempts: []corellm.AttemptOutcome{
			{Attempt: 1, Err: inner},
			{Attempt: 2, Err: inner},
			{Attempt: 3, Err: inner},
		},
	}
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(ErrRetryBudgetExhausted) returned nil")
	}
	if got.Code != "budget_exhausted" {
		t.Errorf("code = %q, want %q", got.Code, "budget_exhausted")
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

// TestMapLLMError_ContextOverflow verifies ErrInvalidRequest with overflow
// keywords maps to "context_overflow".
func TestMapLLMError_ContextOverflow(t *testing.T) {
	err := &corellm.ErrInvalidRequest{
		Status:  400,
		Message: "This model's maximum context length is 8192 tokens",
	}
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(overflow ErrInvalidRequest) returned nil")
	}
	if got.Code != "context_overflow" {
		t.Errorf("code = %q, want %q", got.Code, "context_overflow")
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

// TestMapLLMError_InvalidRequest verifies a non-overflow ErrInvalidRequest
// maps to "invalid_request".
func TestMapLLMError_InvalidRequest(t *testing.T) {
	err := &corellm.ErrInvalidRequest{Status: 400, Message: "invalid model specified"}
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(ErrInvalidRequest) returned nil")
	}
	if got.Code != "invalid_request" {
		t.Errorf("code = %q, want %q", got.Code, "invalid_request")
	}
	if got.Retryable {
		t.Error("Retryable = true, want false")
	}
}

// TestMapLLMError_Internal verifies an unrecognised error maps to "internal".
func TestMapLLMError_Internal(t *testing.T) {
	err := errors.New("some unexpected error")
	got := MapLLMError(err)
	if got == nil {
		t.Fatal("MapLLMError(generic error) returned nil")
	}
	if got.Code != "internal" {
		t.Errorf("code = %q, want %q", got.Code, "internal")
	}
}

// TestBootHealth_GetSetRoundTrip verifies SetBootErrors + BootHealth_Get
// round-trips correctly (FR-008 WP08).
func TestBootHealth_GetSetRoundTrip(t *testing.T) {
	// Use a fresh store to avoid test pollution.
	store := &bootHealthStore{}
	orig := globalBootStore
	globalBootStore = store
	defer func() { globalBootStore = orig }()

	SetBootErrors("mcp: dial failed", "", "fleet: timeout")

	b := NewBindings(nil)
	report := b.BootHealth_Get()

	if report.MCPInitError != "mcp: dial failed" {
		t.Errorf("MCPInitError = %q, want %q", report.MCPInitError, "mcp: dial failed")
	}
	if report.SkillsInitError != "" {
		t.Errorf("SkillsInitError = %q, want empty", report.SkillsInitError)
	}
	if report.FleetInitError != "fleet: timeout" {
		t.Errorf("FleetInitError = %q, want %q", report.FleetInitError, "fleet: timeout")
	}
	if report.IsHealthy() {
		t.Error("IsHealthy() = true, want false (MCP and Fleet errors present)")
	}
}

// TestBootHealth_IsHealthy_EmptyReport verifies IsHealthy when no errors.
func TestBootHealth_IsHealthy_EmptyReport(t *testing.T) {
	r := BootHealthReport{}
	if !r.IsHealthy() {
		t.Error("IsHealthy() = false on empty report, want true")
	}
}
