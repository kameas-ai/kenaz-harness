// Package trust is the cross-mission stub for a2a-signed-cards-trust.
//
// The full trust mission owns Sigstore wiring, anchor management, and
// detached-Ed25519 verification. The bundle layer never reimplements
// any of this; it only consumes the Verifier interface defined here.
//
// Replace this file at integration time with the real implementation
// from the a2a-signed-cards-trust mission's worktree.
package trust

import "context"

// Verifier is the narrow interface the bundle layer offers payloads to
// for signature verification. The full trust mission supplies a
// concrete implementation that consults the operator's trust set,
// transparency log, and revocation policy.
type Verifier interface {
	Verify(ctx context.Context, req VerifyRequest) (VerifyResult, error)
}

// VerifyRequest carries a payload + signature reference to verify.
type VerifyRequest struct {
	// Payload is the canonical bytes the signature covers.
	Payload []byte
	// Signature is the channel-resolved signature pointer.
	Signature SignatureRef
	// Anchors is the operator's trust set (may be nil for offline-only
	// verification paths).
	Anchors []Anchor
}

// SignatureRef mirrors the manifest-layer SignatureRef shape; defined
// here so this package has no upward dependency on core/bundle.
type SignatureRef struct {
	Kind      string
	Locator   string
	Algorithm string
	KeyID     string
	// Bytes is the resolved signature bytes (may be nil for sigstore
	// referrer kinds where the verifier resolves the bytes itself).
	Bytes []byte
}

// Anchor is one entry in the operator's trust set.
type Anchor struct {
	ID         string
	PublicKey  []byte
	Identifier string // OIDC identity or key id
}

// VerifyResult is the verifier's structured outcome.
type VerifyResult struct {
	OK        bool
	AnchorID  string
	KeyID     string
	Reason    string // populated when OK == false
}

// NoopVerifier is a stub that fails every verification with an
// informative reason. The bundle layer treats a NoopVerifier as a
// signal that signature verification is "not configured"; operator
// policy `signatures: required` therefore returns ErrSignatureRequired.
type NoopVerifier struct{}

// Verify always returns OK=false with a "not configured" reason.
func (NoopVerifier) Verify(_ context.Context, _ VerifyRequest) (VerifyResult, error) {
	return VerifyResult{OK: false, Reason: "trust verifier not configured (a2a-signed-cards-trust stub)"}, nil
}
