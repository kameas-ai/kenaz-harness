package algo_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust"
	"github.com/kameas-ai/kenaz-harness/core/trust/internal/algo"
)

// TestEd25519RoundTrip — sign with stdlib, verify through the registry.
// Asserts the canonical happy path FR-001/FR-002 require.
func TestEd25519RoundTrip(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	payload := []byte("the package is signed")
	sig := ed25519.Sign(priv, payload)

	r := algo.New()
	if err := r.Verify(trust.AlgEd25519, pub, payload, sig); err != nil {
		t.Fatalf("Verify happy path: %v", err)
	}
}

// TestEd25519TamperDetected — single-byte rewrite of the payload causes
// verification to fail. This is the core "tampering detection" claim
// from US2 / SC-002.
func TestEd25519TamperDetected(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	payload := []byte("the package is signed")
	sig := ed25519.Sign(priv, payload)

	r := algo.New()
	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0x01
	if err := r.Verify(trust.AlgEd25519, pub, tampered, sig); err == nil {
		t.Fatal("expected verification of tampered payload to fail; got nil")
	}
}

// TestECDSAReturnsNotImplemented — interface-conformant slot at v1.0
// returns a typed error rather than a panic. FR-004 phasing.
func TestECDSAReturnsNotImplemented(t *testing.T) {
	r := algo.New()
	err := r.Verify(trust.AlgECDSAP256, []byte{1, 2, 3}, []byte("payload"), []byte("sig"))
	if !errors.Is(err, trust.ErrAlgorithmNotImplemented) {
		t.Fatalf("ECDSA-P256 returned %v, want ErrAlgorithmNotImplemented", err)
	}
}

// TestRSAReturnsNotImplemented — interface-conformant slot at v1.0
// returns a typed error rather than a panic. FR-004 phasing.
func TestRSAReturnsNotImplemented(t *testing.T) {
	r := algo.New()
	err := r.Verify(trust.AlgRSAPSSSHA256, []byte{1, 2, 3}, []byte("payload"), []byte("sig"))
	if !errors.Is(err, trust.ErrAlgorithmNotImplemented) {
		t.Fatalf("RSA-PSS returned %v, want ErrAlgorithmNotImplemented", err)
	}
}

// TestUnknownAlgorithm — registry rejects unknown algorithms with
// ErrAlgorithmNotSupported.
func TestUnknownAlgorithm(t *testing.T) {
	r := algo.New()
	err := r.Verify(trust.Algorithm("bogus"), nil, nil, nil)
	if !errors.Is(err, trust.ErrAlgorithmNotSupported) {
		t.Fatalf("unknown alg returned %v, want ErrAlgorithmNotSupported", err)
	}
}

// TestImplementedListsEd25519 — registry reports Ed25519 as the only
// implemented algorithm at v1.0.
func TestImplementedListsEd25519(t *testing.T) {
	r := algo.New()
	got := r.Implemented()
	if len(got) != 1 || got[0] != trust.AlgEd25519 {
		t.Fatalf("Implemented() = %v, want [ed25519]", got)
	}
}
