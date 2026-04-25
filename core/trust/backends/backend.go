// Package backends defines the [SigningBackend] interface that every
// concrete signing backend (software, oskeychain, awskms, yubikey,
// pkcs11) implements.
//
// # Charter boundary (DIRECTIVE_001 / C-001)
//
// This file imports nothing platform- or vendor-specific. Each backend
// lives in its own subpackage and may import its own SDK (zalando/go-keyring,
// aws-sdk-go-v2, go-piv/piv-go, miekg/pkcs11). No file outside
// core/trust/backends/<kind>/ may import a backend SDK.
package backends

import (
	"context"

	"kaneaz-harness/core/trust"
)

// SigningBackend is the contract every backend implements (FR-008).
//
// The dispatcher resolves an [trust.IdentityRef] to one of these by its
// [trust.BackendKind] and calls Health → Sign in a single hot path.
type SigningBackend interface {
	// Kind returns the BackendKind this backend handles. Must match the
	// IdentityRef.Backend value the dispatcher resolved on.
	Kind() trust.BackendKind

	// Health reports whether the backend can perform sign operations
	// right now (NFR-007). The dispatcher fails closed unless Health
	// returns trust.HealthOK.
	Health(ctx context.Context) trust.HealthStatus

	// SupportedAlgorithms enumerates which algorithms the backend can
	// sign with. The dispatcher refuses to call Sign with an algorithm
	// not in this list (returns ErrAlgorithmNotSupported).
	SupportedAlgorithms() []trust.Algorithm

	// Sign produces a detached signature over payload using the key
	// referenced by ref under the named algorithm. Returns the raw
	// signature bytes; the dispatcher wraps them in the A2A v1
	// envelope.
	Sign(ctx context.Context, ref trust.BackendRef, alg trust.Algorithm, payload []byte) ([]byte, error)

	// PublicKey returns the verifying key associated with ref. Used for
	// Sign-then-attach-key envelopes and for installing local
	// identities as their own anchors (loopback test mode).
	PublicKey(ctx context.Context, ref trust.BackendRef) (trust.PublicKey, error)
}
