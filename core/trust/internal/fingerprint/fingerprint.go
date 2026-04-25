// Package fingerprint provides deterministic SHA-256 fingerprints over
// public keys for use as the canonical key_id in envelopes and as the
// identity-collision unique-index column.
//
// The function is platform-agnostic and produces stable output across
// runs; callers can compare fingerprint strings directly.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
)

// Compute returns the lower-case hex SHA-256 over keyBytes. Callers
// supply the raw public-key bytes in the algorithm-natural encoding
// (e.g. 32 bytes for Ed25519). An empty input returns the empty string
// rather than the SHA-256 of an empty buffer; callers can therefore
// detect "no fingerprint set" without inspecting raw bytes.
func Compute(keyBytes []byte) string {
	if len(keyBytes) == 0 {
		return ""
	}
	sum := sha256.Sum256(keyBytes)
	return hex.EncodeToString(sum[:])
}
