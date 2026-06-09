package fingerprint_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust/internal/fingerprint"
)

// TestFingerprintDeterministic — the same input produces the same
// output across calls; spec calls this out as the basis for the
// (agent_id, public_key_fingerprint) unique index.
func TestFingerprintDeterministic(t *testing.T) {
	a := fingerprint.Compute([]byte{1, 2, 3, 4})
	b := fingerprint.Compute([]byte{1, 2, 3, 4})
	if a != b {
		t.Fatalf("fingerprint not deterministic: %s vs %s", a, b)
	}
}

// TestFingerprintDifferentForDifferentInputs.
func TestFingerprintDifferentForDifferentInputs(t *testing.T) {
	a := fingerprint.Compute([]byte("alpha"))
	b := fingerprint.Compute([]byte("beta"))
	if a == b {
		t.Fatal("different inputs produced identical fingerprint")
	}
}

// TestFingerprintEmptyIsEmpty — zero-input returns the empty string so
// callers can detect "no fingerprint set" without inspecting bytes.
func TestFingerprintEmptyIsEmpty(t *testing.T) {
	got := fingerprint.Compute(nil)
	if got != "" {
		t.Fatalf("empty input yielded fingerprint %q, want empty string", got)
	}
}

// TestFingerprintHexLength — SHA-256 is 32 bytes -> 64 hex chars.
func TestFingerprintHexLength(t *testing.T) {
	got := fingerprint.Compute([]byte("hello"))
	if len(got) != 64 {
		t.Fatalf("fingerprint length %d, want 64", len(got))
	}
}
