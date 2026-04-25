package trust

import (
	"context"
	"strings"
	"testing"
)

// TestEngineVerifier_NilEngine asserts the constructor refuses a nil
// engine outright rather than emitting a verifier that panics on first
// use.
func TestEngineVerifier_NilEngine(t *testing.T) {
	if _, err := NewEngineVerifier(nil); err == nil {
		t.Fatalf("NewEngineVerifier(nil) returned nil error")
	}
}

// TestEngineVerifier_RejectsUnknownAlgorithm exercises the algorithm
// translation path: a wire-string id that does not correspond to a
// registered algorithm must surface as a non-OK result with the
// canonical "algorithm_unsupported" reason.
func TestEngineVerifier_RejectsUnknownAlgorithm(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}
	res, err := v.Verify(context.Background(), VerifyRequest{
		Payload: []byte("payload"),
		Signature: SignatureRef{
			Algorithm: "no-such-alg",
			KeyID:     "irrelevant",
		},
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false for unsupported algorithm")
	}
	if !strings.Contains(res.Reason, "algorithm_unsupported") {
		t.Fatalf("Reason=%q want substring algorithm_unsupported", res.Reason)
	}
}

// TestEngineVerifier_DelegatesToEngine exercises the adapter end-to-end
// against the real engine: a non-empty payload + signature ref produces
// a structured rejection (the bundle layer never carries signature bytes
// in its SignatureRef, so the engine's shape check refuses, which is the
// observable side-effect proving delegation occurred).
func TestEngineVerifier_DelegatesToEngine(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}
	res, err := v.Verify(context.Background(), VerifyRequest{
		Payload: []byte("payload"),
		Signature: SignatureRef{
			Algorithm: string(AlgEd25519),
			KeyID:     "deadbeef",
		},
	})
	if err != nil {
		t.Fatalf("Verify error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false when delegating to engine without sig bytes")
	}
	if res.Reason == "" {
		t.Fatalf("expected non-empty rejection reason")
	}
	// The engine's shape validator runs first; an empty sig surfaces as
	// signature_invalid. Either way the adapter must produce one of the
	// canonical RejectionCode strings, never an opaque error.
	found := false
	for _, code := range AllRejectionCodes() {
		if res.Reason == string(code) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Reason=%q is not a canonical RejectionCode", res.Reason)
	}
}

// TestEngineVerifier_InstallAnchorsOption asserts the
// WithRequestAnchorInstall option causes request-supplied anchors to
// be installed on the engine before delegation. We confirm by listing
// the engine's anchors after a Verify call.
func TestEngineVerifier_InstallAnchorsOption(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine, WithRequestAnchorInstall())
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}
	anchor := Anchor{
		AnchorID:  "test-anchor",
		Kind:      AnchorRawPublicKey,
		Algorithm: AlgEd25519,
		PublicKey: PublicKey{
			Algorithm:   AlgEd25519,
			Bytes:       make([]byte, 32),
			Fingerprint: "deadbeef",
		},
	}
	_, _ = v.Verify(context.Background(), VerifyRequest{
		Payload:   []byte("payload"),
		Signature: SignatureRef{Algorithm: string(AlgEd25519), KeyID: "deadbeef"},
		Anchors:   []Anchor{anchor},
	})
	got, err := engine.ListAnchors(context.Background())
	if err != nil {
		t.Fatalf("ListAnchors: %v", err)
	}
	found := false
	for _, a := range got {
		if a.AnchorID == "test-anchor" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected anchor 'test-anchor' to be installed by adapter")
	}
}
