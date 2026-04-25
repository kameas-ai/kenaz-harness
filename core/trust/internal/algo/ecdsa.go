package algo

// ECDSA-P256 verification slot. v1.0 ships interface-only per FR-004
// phasing. The notImplementedVerifier wired in algo.go returns
// trust.ErrAlgorithmNotImplemented.
//
// v1.x will fill in:
//   - SHA-256 hash of payload
//   - DER-encoded signature parse (r, s)
//   - ecdsa.Verify on the provided public key
//
// The math lands here without changing core/trust/ public API
// (NFR-006 / SC-007).
