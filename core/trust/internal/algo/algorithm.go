package algo

import "errors"

// Algorithm identifies a signing/verification algorithm. Stable string
// constants conform to JOSE-style names so they can travel in envelopes.
//
// Implements FR-004 (algorithm policy) — the set is fixed at v1.0 with
// only Ed25519 implemented; ECDSA-P256 and RSA-PSS are interface-conformant
// slots filled in v1.x. Defined here (not in core/trust) so the algo
// math packages can reference Algorithm without inducing an import cycle
// back into core/trust.
type Algorithm string

const (
	// AlgEd25519 — RFC 8032 Ed25519. Default and only implemented at v1.0.
	AlgEd25519 Algorithm = "ed25519"
	// AlgECDSAP256 — interface slot for ECDSA over NIST P-256 / SHA-256.
	AlgECDSAP256 Algorithm = "ecdsa-p256"
	// AlgRSAPSSSHA256 — interface slot for RSA-PSS / SHA-256.
	AlgRSAPSSSHA256 Algorithm = "rsa-pss-sha256"
)

// String implements fmt.Stringer.
func (a Algorithm) String() string { return string(a) }

// ErrAlgorithmNotImplemented is returned by interface-conformant algorithm
// slots that do not yet have a real implementation (FR-004 phasing —
// ECDSA-P256 / RSA-PSS at v1.0).
var ErrAlgorithmNotImplemented = errors.New("trust: algorithm not implemented")

// ErrAlgorithmNotSupported is returned when a backend cannot produce
// signatures for the requested algorithm.
var ErrAlgorithmNotSupported = errors.New("trust: algorithm not supported by backend")
