package trust

import "github.com/kameas-ai/kenaz-harness/core/trust/internal/fingerprint"

// ComputeFingerprint re-exports core/trust/internal/fingerprint.Compute
// at the public API surface, so callers outside core/trust (e.g.
// core/rpc/views/trustanchor, which builds an Anchor from an
// operator-supplied public key) can compute the same SHA-256
// fingerprint the verify pipeline expects in Envelope.KeyID without
// reaching into an internal/ package they cannot import.
func ComputeFingerprint(keyBytes []byte) string {
	return fingerprint.Compute(keyBytes)
}
