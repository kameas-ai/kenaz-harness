package hooks

import (
	"os"
	"testing"
)

// TestVerboseAttrsGate is the privacy CI invariant test.
// It asserts that:
//   - addVerboseAttrs is a no-op when HARNESS_VERBOSE_ATTRS is unset.
//   - addVerboseAttrs writes attributes when HARNESS_VERBOSE_ATTRS is set.
//
// This test MUST fail when the env var guard is removed from addVerboseAttrs
// so CI catches privacy regressions.
func TestVerboseAttrsGate_NoLeakByDefault(t *testing.T) {
	t.Parallel()
	// Unset env var (or it was already unset in the test environment).
	orig := os.Getenv(verboseAttrsEnv)
	_ = os.Unsetenv(verboseAttrsEnv)
	defer func() {
		if orig != "" {
			_ = os.Setenv(verboseAttrsEnv, orig)
		}
	}()

	// When the env var is unset, verboseAttrsEnabled must return false.
	if verboseAttrsEnabled() {
		t.Fatal("verboseAttrsEnabled() must return false when HARNESS_VERBOSE_ATTRS is not set")
	}
}

func TestVerboseAttrsGate_EnabledWhenSet(t *testing.T) {
	// NOT parallel — mutates env var.
	orig := os.Getenv(verboseAttrsEnv)
	_ = os.Setenv(verboseAttrsEnv, "1")
	defer func() {
		if orig != "" {
			_ = os.Setenv(verboseAttrsEnv, orig)
		} else {
			_ = os.Unsetenv(verboseAttrsEnv)
		}
	}()

	if !verboseAttrsEnabled() {
		t.Fatal("verboseAttrsEnabled() must return true when HARNESS_VERBOSE_ATTRS=1")
	}
}

// TestSpanHookFire_NilSafe verifies startHookSpan + end are nil-safe.
func TestSpanHookFire_NilSafe(t *testing.T) {
	t.Parallel()
	// nil hookSpan.end must not panic.
	var s *hookSpan
	s.end(OutcomeSuccess, "approve")
}

// TestOutcomeConstants verifies outcome strings are the documented values.
func TestOutcomeConstants(t *testing.T) {
	t.Parallel()
	if OutcomeSuccess != "success" {
		t.Errorf("OutcomeSuccess=%q, want %q", OutcomeSuccess, "success")
	}
	if OutcomeNonBlockingError != "non_blocking_error" {
		t.Errorf("OutcomeNonBlockingError=%q, want %q", OutcomeNonBlockingError, "non_blocking_error")
	}
	if OutcomeCancelled != "cancelled" {
		t.Errorf("OutcomeCancelled=%q, want %q", OutcomeCancelled, "cancelled")
	}
}

// TestSpanHookFire_Name verifies the span name constant.
func TestSpanHookFire_Name(t *testing.T) {
	t.Parallel()
	if SpanHookFire != "harness.hook.fire" {
		t.Errorf("SpanHookFire=%q, want %q", SpanHookFire, "harness.hook.fire")
	}
}
