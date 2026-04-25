//go:build test_software_backend
// +build test_software_backend

package software_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/trust"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends"
	"github.com/sigil-tech/kaneaz-harness/core/trust/backends/software"
	"github.com/sigil-tech/kaneaz-harness/core/trust/internal/algo"
)

// TestSoftwareSignVerifyRoundTrip — the canonical software-backend
// happy path. A generated key signs a payload, the algo registry
// verifies the signature using the corresponding public key.
func TestSoftwareSignVerifyRoundTrip(t *testing.T) {
	b := software.New()
	t.Cleanup(func() { _ = b.Close() })

	pub, err := b.GenerateAndStore("ident-a")
	if err != nil {
		t.Fatalf("GenerateAndStore: %v", err)
	}
	payload := []byte("hello, signed world")
	sig, err := b.Sign(context.Background(), trust.BackendRef{Path: "ident-a"}, trust.AlgEd25519, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	r := algo.New()
	if err := r.Verify(trust.AlgEd25519, pub.Bytes, payload, sig); err != nil {
		t.Fatalf("verify roundtrip: %v", err)
	}
}

// TestSoftwareTamperRejected — the verify path rejects a tampered
// payload. Mirrors the pipeline-level test but at the backend level
// for an extra layer of evidence.
func TestSoftwareTamperRejected(t *testing.T) {
	b := software.New()
	t.Cleanup(func() { _ = b.Close() })

	pub, _ := b.GenerateAndStore("ident-b")
	payload := []byte("orig")
	sig, _ := b.Sign(context.Background(), trust.BackendRef{Path: "ident-b"}, trust.AlgEd25519, payload)

	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0xFF
	r := algo.New()
	if err := r.Verify(trust.AlgEd25519, pub.Bytes, tampered, sig); err == nil {
		t.Fatal("expected tampered payload to fail verification")
	}
}

// TestSoftwareRegistration — confirms the build-tagged form lets the
// dispatcher discover the backend through the registry.
func TestSoftwareRegistration(t *testing.T) {
	r := backends.NewRegistry()
	b := software.New()
	t.Cleanup(func() { _ = b.Close() })
	software.Register(r, b)

	got, err := r.Lookup(trust.BackendSoftware)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if got.Health(context.Background()) != trust.HealthOK {
		t.Fatalf("software backend Health = %v, want HealthOK in test build", got.Health(context.Background()))
	}
}

// TestSoftwareUnknownAlgorithmRefused.
func TestSoftwareUnknownAlgorithmRefused(t *testing.T) {
	b := software.New()
	t.Cleanup(func() { _ = b.Close() })
	_, _ = b.GenerateAndStore("ident-c")
	_, err := b.Sign(context.Background(), trust.BackendRef{Path: "ident-c"}, trust.AlgECDSAP256, []byte("p"))
	if err == nil {
		t.Fatal("expected ECDSA sign to fail on Ed25519-only software backend")
	}
}
