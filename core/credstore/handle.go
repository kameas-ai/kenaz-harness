package credstore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Handle is an opaque token returned by Issue. It references a
// credential entry inside the Store without exposing the underlying
// CredentialReference. Handles are single-use and carry a TTL.
type Handle string

// mintHandle generates a cryptographically random 128-bit handle
// encoded as a 32-character hex string, prefixed with "ch_" for
// recognisability in logs.
func mintHandle() (Handle, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("credstore: mint handle: %w", err)
	}
	return Handle("ch_" + hex.EncodeToString(raw[:])), nil
}
