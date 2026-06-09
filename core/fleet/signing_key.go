package fleet

import "crypto/ed25519"

// fleetSigningPublicKeyBytes holds the raw 32-byte ed25519 public key used
// to verify signed config bundles distributed by the fleet server.
//
// This variable is intentionally left as an empty string at source time.
// At release time the build pipeline populates it via:
//
//	-ldflags "-X github.com/kameas-ai/kenaz-harness/core/fleet.fleetSigningPublicKeyBytes=<hex>"
//
// During development / CI without the ldflags the variable remains empty and
// FleetSigningKey() returns nil, causing all bundle verification to fail with
// ErrSigningKeyNotConfigured. This is the intended secure-default: no
// verification → no config applied.
var fleetSigningPublicKeyBytes string

// FleetSigningKey returns the embedded ed25519 public key, or nil when the
// key has not been populated at build time (ldflags absent or dev build).
//
// Callers MUST treat a nil return as a hard-reject: do not apply any config
// bundle when the key is unavailable.
func FleetSigningKey() ed25519.PublicKey {
	if len(fleetSigningPublicKeyBytes) == 0 {
		return nil
	}
	// The ldflags value is a 64-character lowercase hex string (32 raw bytes).
	// Decode hex → []byte, then wrap as ed25519.PublicKey.
	b, err := hexDecodeString(fleetSigningPublicKeyBytes)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil
	}
	return ed25519.PublicKey(b)
}

// hexDecodeString decodes a lowercase hex string without importing
// encoding/hex to keep the dependency footprint minimal.
func hexDecodeString(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, ErrSigningKeyNotConfigured
	}
	b := make([]byte, len(s)/2)
	for i := range b {
		hi, ok1 := hexNibble(s[i*2])
		lo, ok2 := hexNibble(s[i*2+1])
		if !ok1 || !ok2 {
			return nil, ErrSigningKeyNotConfigured
		}
		b[i] = hi<<4 | lo
	}
	return b, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
