package fleet

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// fleetSigningPublicKeys holds the PINNED SET of ed25519 public keys used to
// verify signed config bundles distributed by the fleet server, as a
// comma-separated list. Each entry is exactly 64 hex characters — the raw
// 32-byte ed25519 public key. Whitespace around entries is ignored, as are
// empty entries (a trailing comma, or an unset Actions variable expanding to
// ""). Any other malformed entry rejects the WHOLE set — see
// parseSigningKeySet — so a typo in one pin can never silently shrink the
// set to "whatever happened to parse".
//
// This variable is intentionally left as an empty string at source time.
// At release time the build pipeline populates it via:
//
//	-ldflags "-X github.com/kameas-ai/kenaz-harness/core/fleet.fleetSigningPublicKeys=<hex>[,<hex>...]"
//
// release.yml selects the per-env list from the repo Actions VARIABLES
// FLEET_SIGNING_PUBKEYS_DEV / _STAGE / _PROD (public keys — deliberately
// variables, not secrets). During a key rotation the list carries the
// current AND the next key (docs/fleet-key-rotation.md); each bundle names
// its signer via the signed "key_id" field, which routes verification to
// exactly one pinned key (see VerifyWithKeySet).
//
// During development / CI without the ldflags the variable remains empty and
// FleetSigningKeys() returns nil, causing all bundle verification to fail with
// ErrSigningKeyNotConfigured. This is the intended secure-default: no
// verification → no config applied.
//
// Example generation of one entry:
//
//	# Generate a keypair (do NOT commit the private key):
//	openssl genpkey -algorithm ed25519 -out fleet-signing-key.pem
//	openssl pkey -in fleet-signing-key.pem -pubout -outform DER | tail -c 32 | xxd -p | tr -d '\n'
var fleetSigningPublicKeys string

// fleetSigningPublicKeyBytes is the LEGACY single-key pin (one 64-hex entry),
// injected by release.yml up to v0.89.x from secrets.FLEET_SIGNING_PUBKEY.
// It is merged into the fleetSigningPublicKeys set (deduplicated) so a build
// that still sets only the old ldflag keeps working.
//
// DATED (2026-10-06, owner: alec): accepted for ONE release only. Delete this
// var, its merge in signingKeySetSource, and the secrets.FLEET_SIGNING_PUBKEY
// fallback in release.yml in the first release after the one that ships
// fleetSigningPublicKeys.
var fleetSigningPublicKeyBytes string

// SigningKeyID returns the bundle key_id for a raw 32-byte ed25519 public key:
// lowercase hex of the first 8 bytes of SHA-256(rawPublicKey) — 16 chars.
// This is the cross-repo wire contract shared with kenaz-fleet
// (KeyIDForPublicKey); a bundle's signed "key_id" field selects the pinned
// key whose SigningKeyID equals it.
func SigningKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// signingKeySetSource returns the raw comma-separated pin list: the new set
// var plus the legacy single-key var (one-release compatibility).
func signingKeySetSource() string {
	if strings.TrimSpace(fleetSigningPublicKeyBytes) == "" {
		return fleetSigningPublicKeys
	}
	return fleetSigningPublicKeys + "," + fleetSigningPublicKeyBytes
}

// parseSigningKeySet parses a comma-separated pin list. Empty entries are
// skipped; every other entry must be exactly 64 hex characters (32 raw
// bytes). A single malformed entry rejects the WHOLE set with an error
// wrapping ErrSigningKeyNotConfigured — never a silent skip, because a
// silently-dropped pin is indistinguishable from a deliberate rotation and
// would surface much later as "bundle signed with an unknown key".
// Duplicate keys collapse to one.
func parseSigningKeySet(s string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	seen := map[string]bool{}
	for i, raw := range strings.Split(s, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if len(entry) != 2*ed25519.PublicKeySize {
			return nil, fmt.Errorf("%w: pinned signing key entry #%d has %d characters, want exactly %d hex characters (a raw 32-byte ed25519 public key) — the whole pin set is rejected",
				ErrSigningKeyNotConfigured, i+1, len(entry), 2*ed25519.PublicKeySize)
		}
		b, err := hexDecodeString(entry)
		if err != nil {
			return nil, fmt.Errorf("pinned signing key entry #%d: %w — the whole pin set is rejected", i+1, err)
		}
		if err := checkPinnableEd25519Key(b); err != nil {
			return nil, fmt.Errorf("%w: pinned signing key entry #%d %v — the whole pin set is rejected",
				ErrSigningKeyNotConfigured, i+1, err)
		}
		k := string(b)
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, ed25519.PublicKey(b))
	}
	return keys, nil
}

// FleetSigningKeySetError reports why the build-time pin set failed to
// parse, or nil when it parsed (including the empty set). Used by the boot
// assertion in main.go so a malformed pin is a loud ERROR, not the quiet
// "no key" WARN a deliberately unkeyed build gets.
func FleetSigningKeySetError() error {
	_, err := parseSigningKeySet(signingKeySetSource())
	return err
}

// FleetSigningKey returns the first pinned ed25519 public key, or nil when
// no key has been populated at build time (ldflags absent or dev build).
//
// Deprecated: use FleetSigningKeys() — the pin is a set, and verification
// routes by key_id across all of it.
func FleetSigningKey() ed25519.PublicKey {
	keys := FleetSigningKeys()
	if len(keys) == 0 {
		return nil
	}
	return keys[0]
}

// FleetSigningKeys returns every pinned ed25519 public key (the accept-set
// used by VerifyWithKeySet). Returns nil when no key has been injected
// (dev/CI build) OR when the pin list is malformed — a malformed set is
// rejected whole (fail-closed: config distribution is disabled), and
// FleetSigningKeySetError says why.
func FleetSigningKeys() []ed25519.PublicKey {
	keys, err := parseSigningKeySet(signingKeySetSource())
	if err != nil {
		return nil
	}
	return keys
}

// SetSigningKeyForTesting installs pubs as the pinned fleet signing key set
// for the duration of a test and returns a restore func that puts the
// previous (ldflags-populated, normally empty in dev/CI) values back. The
// legacy single-key var is cleared for the duration so the set is exactly
// pubs.
//
// This is a test seam ONLY — production code populates the pin vars
// exclusively via the release-time -ldflags injection documented above. It
// exists so cross-package integration tests (e.g. core/rpc/views/settings,
// which cannot set the unexported package vars directly) can drive a REAL
// VerifyWithKeySet pass against a bundle they sign in-test, rather than
// stubbing verification out — spec rule (fleet-enforcement-truth-01PMZ505
// §8 rule 2 / AC-002): "the test proves nothing about the integrity claim"
// if verification is bypassed. Mirrors the existing NewClientForTesting /
// SeedFleetConfigForTesting convention in this package.
func SetSigningKeyForTesting(pubs ...ed25519.PublicKey) (restore func()) {
	entries := make([]string, 0, len(pubs))
	for _, pub := range pubs {
		entries = append(entries, hex.EncodeToString(pub))
	}
	savedSet, savedLegacy := fleetSigningPublicKeys, fleetSigningPublicKeyBytes
	fleetSigningPublicKeys = strings.Join(entries, ",")
	fleetSigningPublicKeyBytes = ""
	return func() {
		fleetSigningPublicKeys = savedSet
		fleetSigningPublicKeyBytes = savedLegacy
	}
}

// ConfigDistributionEnabled reports whether at least one valid fleet signing
// key is pinned in this binary. When false, all config-bundle distribution is
// disabled: every Verify call returns ErrSigningKeyNotConfigured and no
// bundle is applied.
//
// Used by boot assertions (FR-002) and the fleet-health surface (WP02/WP10).
func ConfigDistributionEnabled() bool {
	return len(FleetSigningKeys()) > 0
}

// hexDecodeString decodes a hex string (either case).
func hexDecodeString(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("%w: odd-length hex string", ErrSigningKeyNotConfigured)
	}
	b := make([]byte, len(s)/2)
	for i := range b {
		hi, ok1 := hexNibble(s[i*2])
		lo, ok2 := hexNibble(s[i*2+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%w: invalid hex character", ErrSigningKeyNotConfigured)
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
