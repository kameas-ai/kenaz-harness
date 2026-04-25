package llm

import (
	"errors"
	"fmt"
	"strings"
)

// Error sentinels for the typed taxonomy. The retry middleware classifies
// adapter errors via errors.As / errors.Is against the concrete types
// below (ErrTransient, ErrRetryBudgetExhausted, etc.) — these sentinels
// give callers a stable equality target.
var (
	ErrUnknown = errors.New("llm: unknown error")
)

// ErrCapabilityUnsupported is returned when a request opts into a
// capability the (provider, model) does not support (FR-013).
type ErrCapabilityUnsupported struct {
	Provider     string
	Model        string
	Capabilities []Capability
}

func (e *ErrCapabilityUnsupported) Error() string {
	caps := make([]string, len(e.Capabilities))
	for i, c := range e.Capabilities {
		caps[i] = string(c)
	}
	return fmt.Sprintf("llm: capability unsupported by provider %q model %q: %s",
		e.Provider, e.Model, strings.Join(caps, ","))
}

// ErrCredentialResolution wraps a failure to resolve a CredentialReference
// at preflight or at request time (FR-003 / FR-019).
type ErrCredentialResolution struct {
	ProfileID string
	Ref       CredentialReference
	Cause     error
}

func (e *ErrCredentialResolution) Error() string {
	return fmt.Sprintf("llm: credential resolution failed for profile %q ref %s: %v",
		e.ProfileID, e.Ref.String(), e.Cause)
}

func (e *ErrCredentialResolution) Unwrap() error { return e.Cause }

// ErrTransient marks a recoverable provider error subject to retry
// (FR-016 / FR-017). Adapters MUST wrap network blips, 408, 425, 429,
// and 5xx responses in ErrTransient.
type ErrTransient struct {
	Status  int
	Message string
	Cause   error
}

func (e *ErrTransient) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("llm: transient provider error (status=%d): %s", e.Status, e.Message)
	}
	return "llm: transient provider error: " + e.Message
}

func (e *ErrTransient) Unwrap() error { return e.Cause }

// AttemptOutcome records the outcome of one retry attempt for the
// budget-exhausted error.
type AttemptOutcome struct {
	Attempt    int
	Err        error
	BackoffMS  int
	ActualMS   int
}

// ErrRetryBudgetExhausted is returned when the retry middleware has
// exhausted MaxAttempts without recovering (FR-016 / US4 Acceptance 3).
type ErrRetryBudgetExhausted struct {
	Attempts []AttemptOutcome
}

func (e *ErrRetryBudgetExhausted) Error() string {
	return fmt.Sprintf("llm: retry budget exhausted after %d attempts", len(e.Attempts))
}

// ErrAuth marks an authentication / authorization failure (401/403).
// Non-transient — never retried (FR-017).
type ErrAuth struct {
	Status  int
	Message string
}

func (e *ErrAuth) Error() string {
	return fmt.Sprintf("llm: auth error (status=%d): %s", e.Status, e.Message)
}

// ErrInvalidRequest marks a 4xx (other than 408/425/429) provider
// rejection. Non-transient — never retried (FR-017).
type ErrInvalidRequest struct {
	Status  int
	Message string
}

func (e *ErrInvalidRequest) Error() string {
	return fmt.Sprintf("llm: invalid request (status=%d): %s", e.Status, e.Message)
}

// ErrPolicyDenied indicates a policy-engine refusal pre-call.
type ErrPolicyDenied struct {
	Reason string
}

func (e *ErrPolicyDenied) Error() string {
	return "llm: policy denied: " + e.Reason
}

// ErrCancelled marks a caller-initiated cancellation (FR-012).
type ErrCancelled struct {
	Reason string
}

func (e *ErrCancelled) Error() string {
	if e.Reason == "" {
		return "llm: cancelled"
	}
	return "llm: cancelled: " + e.Reason
}

// IsTransient reports whether err should be retried by the middleware.
//
// The classification is by error type (errors.As against ErrTransient)
// not by string match — provider adapters convert their native error
// shapes into ErrTransient before returning, so this function never
// has to inspect provider-specific data.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var t *ErrTransient
	return errors.As(err, &t)
}
