// Package integrity wires together hashing and signature verification
// for the bundle layer. SHA-256 is mandatory for every activated
// artifact (D5, FR-006). Signature verification is delegated entirely
// to the trust mission's Verifier interface — no signature math lives
// in this package.
//
// Imports outside this package: stdlib hash + parent core/bundle for
// errors + core/trust for the Verifier interface. No imports from
// channels/, resolver/, manifest/, or lockfile/.
package integrity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

// HashSHA256 streams r and returns sha256:<hex> of its bytes.
func HashSHA256(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", n, fmt.Errorf("integrity: hash: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// CompareDigests returns true iff a and b are equal after lower-casing
// the hex portion. Both are expected to start with "sha256:".
func CompareDigests(a, b string) bool {
	return strings.EqualFold(a, b)
}
