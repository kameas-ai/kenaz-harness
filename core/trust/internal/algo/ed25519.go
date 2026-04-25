package algo

import (
	"crypto/ed25519"
	"errors"
)

// ed25519Verifier verifies detached Ed25519 signatures per RFC 8032.
type ed25519Verifier struct{}

// Verify runs the Ed25519 signature check. Returns nil on success or an
// error whose .Error() text describes the failure. core/trust maps these
// to RejSignatureInvalid via its verify pipeline fallback; the algo
// package deliberately does not import core/trust to avoid a cycle
// (Algorithm + algorithm-error vars live here, in algorithm.go).
func (ed25519Verifier) Verify(pubKey, payload, signature []byte) error {
	if len(pubKey) != ed25519.PublicKeySize {
		return errors.New("ed25519: bad public key length")
	}
	if len(signature) != ed25519.SignatureSize {
		return errors.New("ed25519: bad signature length")
	}
	if !ed25519.Verify(ed25519.PublicKey(pubKey), payload, signature) {
		return errors.New("ed25519: verification failed")
	}
	return nil
}
