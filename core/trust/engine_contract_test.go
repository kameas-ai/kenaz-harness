package trust_test

// Black-box compile-only contract test (DIRECTIVE_036) — confirms an
// external package can declare a variable of type trust.TrustEngine and
// reference every method on the interface. Also covers a few key value
// types so a downstream consumer (acp / bundle / context / policy) can
// plan against the surface without importing internals.

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/trust"
)

// _ = (trust.TrustEngine)(nil) at file scope guarantees the interface
// remains exported and structurally usable from other packages.
var _ trust.TrustEngine = (trust.TrustEngine)(nil)

// TestPublicSurfaceCompiles is a compile-only black-box assertion.
// If any rename or signature change breaks consumers, this test fails to
// build before any runtime check fires. Spec: SC-007 (backend contract
// stability).
func TestPublicSurfaceCompiles(t *testing.T) {
	// A declared engine variable is enough to lock the surface; no
	// methods need to be invoked here. Building this file is the test.
	var engine trust.TrustEngine

	// Reference every interface method to fail compilation on a
	// signature drift.
	_ = func() {
		ctx := context.Background()
		_, _ = engine.Verify(ctx, nil, trust.Envelope{}, trust.VerifyOptions{})
		_, _ = engine.Sign(ctx, nil, trust.IdentityRef{}, trust.SignOptions{})
		_ = engine.InstallAnchor(ctx, trust.Anchor{})
		_ = engine.RemoveAnchor(ctx, "")
		_, _ = engine.ListAnchors(ctx)
		_ = engine.BeginRotation(ctx, "", trust.PublicKey{}, time.Hour)
		_ = engine.CompleteRotation(ctx, "")
		_ = engine.IngestRevocation(ctx, trust.RevocationRecord{})
		_, _ = engine.Preflight(ctx)
	}
}

// TestRejectionTaxonomyComplete asserts that every code listed in FR-017
// (plus chain_depth_exceeded from spec edge cases) is exposed as an
// exported constant and present in trust.AllRejectionCodes(). DIRECTIVE_036.
func TestRejectionTaxonomyComplete(t *testing.T) {
	want := map[trust.RejectionCode]bool{
		trust.RejSignatureInvalid:    false,
		trust.RejAlgorithmNotPermit:  false,
		trust.RejAnchorMissing:       false,
		trust.RejAnchorRemoved:       false,
		trust.RejKeyRevoked:          false,
		trust.RejKeyExpired:          false,
		trust.RejIdentityCollision:   false,
		trust.RejClockSkewExceeded:   false,
		trust.RejChainDepthExceeded:  false,
	}
	for _, c := range trust.AllRejectionCodes() {
		want[c] = true
	}
	for code, present := range want {
		if !present {
			t.Errorf("rejection code %q missing from AllRejectionCodes()", code)
		}
	}
	if len(trust.AllRejectionCodes()) != len(want) {
		t.Errorf("AllRejectionCodes returned %d codes, want %d", len(trust.AllRejectionCodes()), len(want))
	}
}

// TestNewEngineProducesUsableEngine — black-box smoke that the factory
// returns a non-nil engine whose verify-only path runs without a backend
// dispatcher. Sign should fail closed with ErrBackendNotRegistered.
func TestNewEngineProducesUsableEngine(t *testing.T) {
	eng, err := trust.NewEngine(trust.Config{})
	if err != nil {
		t.Fatalf("NewEngine returned error: %v", err)
	}
	if eng == nil {
		t.Fatal("NewEngine returned nil engine")
	}
	_, err = eng.Sign(context.Background(), []byte("payload"), trust.IdentityRef{Backend: trust.BackendSoftware, Algorithm: trust.AlgEd25519}, trust.SignOptions{})
	if err == nil {
		t.Fatal("expected Sign without dispatcher to fail; got nil error")
	}
}
