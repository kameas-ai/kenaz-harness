package algo

import (
	"crypto/ed25519"

	"kaneaz-harness/core/trust"
)

// ed25519Verifier verifies detached Ed25519 signatures per RFC 8032.
type ed25519Verifier struct{}

// Verify runs the Ed25519 signature check. Returns nil on success, the
// trust.RejectionError-equivalent sentinel ErrSignatureInvalid otherwise
// (callers map it to RejSignatureInvalid).
func (ed25519Verifier) Verify(pubKey, payload, signature []byte) error {
	if len(pubKey) != ed25519.PublicKeySize {
		return trust.NewRejection(trust.RejSignatureInvalid, "ed25519: bad public key length")
	}
	if len(signature) != ed25519.SignatureSize {
		return trust.NewRejection(trust.RejSignatureInvalid, "ed25519: bad signature length")
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), payload, signature) {
		return trust.NewRejection(trust.RejSignatureInvalid, "ed25519: verification failed")
	}
	return nil
}
