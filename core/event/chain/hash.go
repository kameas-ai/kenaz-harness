package chain

import (
	"crypto/sha256"
)

// PayloadHash returns the canonical-JSON hash of canonical bytes.
//
// DEVIATION: the spec mandates BLAKE3. This implementation uses
// SHA-256 from the standard library while we wait on a BLAKE3
// dependency to land in go.mod (see ADR for hash-function pinning,
// plan §11). The hash function is intentionally isolated to this
// single call site so the swap is mechanical: every other component
// in the chain operates on opaque [32]byte digests.
//
// SHA-256 is also acceptable from a SOC 2 evidence standpoint (it is
// the auditor-default expectation per plan §9).
func PayloadHash(canonical []byte) [32]byte {
	return sha256.Sum256(canonical)
}

// LinkHash composes a previous-hash link with the current payload
// hash. v1 stores prev_hash directly (matches plan §5.5) so this
// helper is a passthrough that documents the contract; v2 may
// fold prev into the digest if the chain shape changes.
func LinkHash(prev [32]byte, payload [32]byte) [32]byte {
	// Current schema: prev_hash references the previous event's
	// payload_hash directly. The verifier recomputes payload_hash
	// from canonical bytes and compares prev_hash to the chain
	// head. LinkHash returns prev unchanged so callers can compose
	// with the same shape if the schema later evolves.
	_ = payload
	return prev
}

// ZeroHash is the all-zero 32-byte digest. Used as prev_hash for the
// first event in a session.
var ZeroHash [32]byte
