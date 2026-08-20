package trust

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/trust/internal/fingerprint"
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

// TestEngineVerifier_AC001_SignedPayloadWithAnchorAccepts is UNIT-2's
// AC-001: a correctly signed payload plus an installed matching anchor
// must now produce OK=true. Before UNIT-2 this was impossible —
// trust.VerifyRequest had no field to carry signature bytes, so every
// Verify call failed step 1 (envelope shape) regardless of anchor
// state. See research/corrections.md's BT-02 execution.
func TestEngineVerifier_AC001_SignedPayloadWithAnchorAccepts(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID := fingerprint.Compute(pub)
	anchor := Anchor{
		AnchorID:  "ac001-anchor",
		Kind:      AnchorRawPublicKey,
		Algorithm: AlgEd25519,
		PublicKey: PublicKey{
			Algorithm:   AlgEd25519,
			Bytes:       append([]byte(nil), pub...),
			Fingerprint: keyID,
		},
	}
	if err := engine.InstallAnchor(context.Background(), anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}

	payload := []byte("ac-001 signed payload")
	sig := ed25519.Sign(priv, payload)

	res, err := v.Verify(context.Background(), VerifyRequest{
		Payload: payload,
		Signature: SignatureRef{
			Algorithm: string(AlgEd25519),
			KeyID:     keyID,
		},
		SignatureBytes: sig,
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if !res.OK {
		t.Fatalf("expected OK=true, got OK=false Reason=%q", res.Reason)
	}
	if res.Reason != "" {
		t.Fatalf("expected empty Reason on accept, got %q", res.Reason)
	}
}

// TestEngineVerifier_AC001_Mutation_NilSignatureBytesStillRejects pins
// the mutation described by AC-001: reverting to a nil/empty
// SignatureBytes (the pre-UNIT-2 behaviour, where bundleadapter.go
// hardcoded Signature: nil) must still produce OK=false with
// Reason=="signature_invalid" — i.e. this test goes red if UNIT-2's
// wiring is reverted.
func TestEngineVerifier_AC001_Mutation_NilSignatureBytesStillRejects(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID := fingerprint.Compute(pub)
	anchor := Anchor{
		AnchorID:  "mutation-anchor",
		Kind:      AnchorRawPublicKey,
		Algorithm: AlgEd25519,
		PublicKey: PublicKey{
			Algorithm:   AlgEd25519,
			Bytes:       append([]byte(nil), pub...),
			Fingerprint: keyID,
		},
	}
	if err := engine.InstallAnchor(context.Background(), anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}

	res, err := v.Verify(context.Background(), VerifyRequest{
		Payload: []byte("payload"),
		Signature: SignatureRef{
			Algorithm: string(AlgEd25519),
			KeyID:     keyID,
		},
		// SignatureBytes deliberately omitted — simulates the
		// pre-UNIT-2 adapter hardcoding Signature: nil.
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false with no signature bytes")
	}
	if res.Reason != string(RejSignatureInvalid) {
		t.Fatalf("Reason=%q, want %q", res.Reason, RejSignatureInvalid)
	}
}

// TestEngineVerifier_AC002_TamperedPayloadRejectsAsSignatureInvalid is
// UNIT-2's AC-002: flipping one byte of the signed payload after
// signing must produce OK=false, Reason=="signature_invalid" — and
// specifically NOT "envelope shape invalid" or "anchor_missing". A
// different reason would mean the pipeline is failing before step 7
// (signature math) for the wrong cause, and AC-001 would have passed
// for the wrong reason.
func TestEngineVerifier_AC002_TamperedPayloadRejectsAsSignatureInvalid(t *testing.T) {
	engine, err := NewEngine(Config{})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	v, err := NewEngineVerifier(engine)
	if err != nil {
		t.Fatalf("NewEngineVerifier: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyID := fingerprint.Compute(pub)
	anchor := Anchor{
		AnchorID:  "ac002-anchor",
		Kind:      AnchorRawPublicKey,
		Algorithm: AlgEd25519,
		PublicKey: PublicKey{
			Algorithm:   AlgEd25519,
			Bytes:       append([]byte(nil), pub...),
			Fingerprint: keyID,
		},
	}
	if err := engine.InstallAnchor(context.Background(), anchor); err != nil {
		t.Fatalf("InstallAnchor: %v", err)
	}

	payload := []byte("ac-002 original payload")
	sig := ed25519.Sign(priv, payload)

	tampered := append([]byte(nil), payload...)
	tampered[0] ^= 0xFF // flip one byte after signing

	res, err := v.Verify(context.Background(), VerifyRequest{
		Payload: tampered,
		Signature: SignatureRef{
			Algorithm: string(AlgEd25519),
			KeyID:     keyID,
		},
		SignatureBytes: sig,
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if res.OK {
		t.Fatalf("expected OK=false for a tampered payload")
	}
	if res.Reason != string(RejSignatureInvalid) {
		t.Fatalf("Reason=%q, want %q (not envelope-shape or anchor_missing)", res.Reason, RejSignatureInvalid)
	}
}
