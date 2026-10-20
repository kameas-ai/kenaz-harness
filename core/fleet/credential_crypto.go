package fleet

// credential_crypto.go — the PINNED provider-credential constructions
// (provider-credentials-client-01PCCLI01 spec §8, §10.1, §10.3; kenaz-fleet
// kitty-specs/provider-credentials-01NPCRED01/contract-draft.md §3, §4, §7
// and Fleet PR #268 docs/contract-harness-credentials.md §5, §6.1, §6.2,
// owner decisions D7 + D8 + H3, 2026-10-15). Implemented VERBATIM — Fleet
// and iOS (swift-sodium) must produce and open exactly these bytes. Labels
// are versioned: changing any construction means a NEW label, never an
// edit to one of these.
//
// Contract §3 "Device credential key":
//
//	cred_sk = clamp(HKDF-SHA256(ikm = context_seed, salt = nil,
//	  info = "kenaz-credential-identity-v1:" || node_id, L = 32)), X25519.
//
// Contract §3 "Wrap":
//
//	ephemeral X25519 → kek = HKDF-SHA256(ikm = shared, salt = nil,
//	  info = "kenaz-credential-v1-wrap", L = 32)
//	→ XChaCha20-Poly1305(kek, nonce24, payload, aad = credential_id ":" version ":" key_id).
//	Payload = the org/team secret ONLY (the UCK uses the authwrap below).
//
// Contract §4 (personal secret under the UCK):
//
//	ciphertext = XChaCha20-Poly1305(UCK, nonce24, secret_utf8,
//	  aad = user_id ":" credential_id ":" version)
//
// Contract §4 "Path (i) seed":
//
//	kek_seed = HKDF-SHA256(context_seed, nil, "kenaz-user-credentials-kek-v1", 32);
//	uck_seed_wrap = XChaCha20-Poly1305(kek_seed, nonce24, UCK, aad = user_id ":uck:" uck_version).
//	seed_key_check = hex(HKDF-SHA256(context_seed, nil, "kenaz-seed-key-check-v1", 16)).
//
// Fleet PR #268 docs/contract-harness-credentials.md §6.2 "Authenticated
// wrap (path ii)" — REPLACES the anonymous UCK device wrap (the old
// aad "uck:" user_id ":" uck_version ":" key_id is superseded and gone):
//
//	ikm = X25519(eph_sk, pk_N) || X25519(sk_W, pk_N)
//	kek = HKDF-SHA256(ikm, salt = nil,
//	  info = "kenaz-credential-v1-authwrap" || eph_pub || pk_N || pk_W, L = 32)
//	aad = "uck:" user_id ":" uck_version ":" key_id_N ":" key_id_W
//	sealed = XChaCha20-Poly1305(kek, nonce24, UCK, aad)
//
// §6.2 "Pairing SAS (commit / reveal)":
//
//	commit = SHA-256("kenaz-pair-commit-v1" || pk_N || nA)      (nA, nB: 32 bytes)
//	b = HKDF-SHA256(ikm = pk_N || pk_W || nA || nB, salt = nil,
//	  info = "kenaz-credential-key-sas-v2:" || user_id || ":" || node_N || ":" || node_W, L = 8)
//	sas = big_endian_uint64(b) mod 10^8, zero-padded to 8 digits.
//
// §6.1 "uck_auth":
//
//	uck_auth = HKDF-SHA256(UCK, salt = nil, info = "kenaz-uck-auth-v1", L = 32)
//	uck_auth_hash = SHA-256(uck_auth)
//
// Contract §7.1: device key "kenaz-credential-identity-v1:" || node_id (salt
// nil, clamped); wrap "kenaz-credential-v1-wrap"; seed KEK
// "kenaz-user-credentials-kek-v1"; seed_key_check "kenaz-seed-key-check-v1".
// §7.2 (D8): XChaCha20-Poly1305 everywhere.
//
// Encodings: every version is a base-10 ASCII integer with no padding or
// sign; separators are a single ASCII ":"; ids are used as the exact
// strings Fleet returns. The v2 constructions (authwrap, pairing) VALIDATE
// their ids first (contract §2): user_id / key_id are canonical lowercase
// hyphenated UUIDs, node ids canonical ULIDs; anything else is refused,
// because ":"-joined free-form ids could make two tuples share bytes. On the wire all byte strings are standard base64
// with padding; seed_key_check alone is lowercase hex.
//
// TestCredentialV1Vectors pins every label and AAD against golden bytes,
// against testdata/credentials/vectors.json, AND against an independent
// in-test reconstruction (manual HKDF + HChaCha20 + curve25519 + SHA-256).
//
// Privacy invariant: no key material, UCK, seed or plaintext is ever
// logged here.

import (
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	// LabelCredentialWrapV1 is the §3 anonymous wrap KEK info. Payload =
	// an org/team secret ONLY; the UCK travels in the authenticated wrap.
	LabelCredentialWrapV1 DeriveLabel = "kenaz-credential-v1-wrap"
	// LabelUserCredentialsKEKV1 is the §4 path (i) seed KEK info.
	LabelUserCredentialsKEKV1 DeriveLabel = "kenaz-user-credentials-kek-v1"
	// LabelSeedKeyCheckV1 is the §4 seed_key_check info (L = 16).
	LabelSeedKeyCheckV1 DeriveLabel = "kenaz-seed-key-check-v1"
	// LabelCredentialAuthWrapV1 is the authenticated UCK device wrap info
	// prefix; eph_pub || pk_N || pk_W is appended (Fleet's kem_context).
	LabelCredentialAuthWrapV1 DeriveLabel = "kenaz-credential-v1-authwrap"
	// LabelPairCommitV1 prefixes the pairing commit hash input.
	LabelPairCommitV1 DeriveLabel = "kenaz-pair-commit-v1"
	// LabelCredentialKeySASV2 is the pairing SAS info prefix;
	// user_id ":" node_N ":" node_W is appended.
	LabelCredentialKeySASV2 DeriveLabel = "kenaz-credential-key-sas-v2:"
	// LabelUCKAuthV1 is the uck_auth info (proof of UCK possession).
	LabelUCKAuthV1 DeriveLabel = "kenaz-uck-auth-v1"

	// credentialIdentityV1Info is the §3 device credential key info prefix;
	// the node id is appended. A separate key purpose from the handoff
	// identity (D7) — never reuse handoffIdentityV2Info.
	credentialIdentityV1Info = "kenaz-credential-identity-v1:"

	// uckLen is the per-user credential key length (§4).
	uckLen = 32
	// seedKeyCheckLen is the seed_key_check HKDF output length (§4).
	seedKeyCheckLen = 16
	// credentialNonceLen is the XChaCha20-Poly1305 nonce length (24).
	credentialNonceLen = chacha20poly1305.NonceSizeX
	// pairNonceLen is the pairing nA / nB length (exactly 32, §6.2).
	pairNonceLen = 32
	// x25519KeyLen is an X25519 public key length.
	x25519KeyLen = 32
	// pairingSASLen is the pairing SAS HKDF output length (L = 8).
	pairingSASLen = 8
	// pairingSASModulus reduces the SAS to 8 decimal digits.
	pairingSASModulus = 100_000_000
)

// ErrCredentialOpenFailed means a credential envelope did not open: wrong
// device key, wrong seed/UCK, an AAD mismatch (credential_id, version,
// key_id, user_id, uck_version), or tampered bytes. Deliberately one
// sentinel so the failure never says which input was wrong.
var ErrCredentialOpenFailed = errors.New("fleet: credential envelope open failed")

// ErrCredentialIDInvalid means an id that feeds an AAD or KDF info is not
// in its canonical form (contract §2: lowercase hyphenated UUID for
// user_id / key_id; §6.2: canonical ULID for node ids), or a version is 0.
var ErrCredentialIDInvalid = errors.New("fleet: credential id not canonical")

// ErrCredentialSelfPairing means the sender and recipient of a pairing or
// authenticated wrap are the same key or node (pk_W == pk_N, node_W ==
// node_N) — rejected as a KCI mitigation (§6.2).
var ErrCredentialSelfPairing = errors.New("fleet: credential pairing peer is this device")

// ErrCredentialIdentityUnavailable is returned when there is no persistent
// node id to derive the device credential key from.
var ErrCredentialIdentityUnavailable = errors.New("fleet: credential identity unavailable: no node id")

// clampX25519 applies the RFC 7748 §5 scalar clamp in place.
func clampX25519(k []byte) {
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
}

// DeriveCredentialPrivKey derives the per-device X25519 credential key
// (contract §3, D7) from the context seed and node id: HKDF-SHA256(seed,
// salt=nil, info="kenaz-credential-identity-v1:"+nodeID, L=32), clamped.
// The clamp is explicit so the exported private scalar matches libsodium's
// convention; crypto/ecdh would clamp internally anyway, so the public key
// is identical either way. Same shape as DeriveHandoffPrivKey.
func DeriveCredentialPrivKey(seed []byte, nodeID string) (*ecdh.PrivateKey, error) {
	if len(seed) != seedSize {
		return nil, fmt.Errorf("fleet: credential identity: seed must be %d bytes", seedSize)
	}
	if strings.TrimSpace(nodeID) == "" {
		return nil, ErrCredentialIdentityUnavailable
	}
	scalar, err := hkdf32(seed, credentialIdentityV1Info+nodeID)
	if err != nil {
		return nil, fmt.Errorf("fleet: derive credential identity scalar: %w", err)
	}
	clampX25519(scalar)
	priv, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		return nil, fmt.Errorf("fleet: build X25519 credential key: %w", err)
	}
	return priv, nil
}

// SeedKeyCheck is §4 seed_key_check: lowercase hex of
// HKDF-SHA256(seed, salt=nil, info="kenaz-seed-key-check-v1", L=16).
// Non-secret; Fleet stores it to detect seed divergence.
func SeedKeyCheck(seed []byte) (string, error) {
	if len(seed) != seedSize {
		return "", fmt.Errorf("fleet: seed_key_check: seed must be %d bytes", seedSize)
	}
	r := hkdf.New(sha256.New, seed, nil, []byte(LabelSeedKeyCheckV1))
	out := make([]byte, seedKeyCheckLen)
	if _, err := io.ReadFull(r, out); err != nil {
		return "", fmt.Errorf("fleet: HKDF expand: %w", err)
	}
	return hex.EncodeToString(out), nil
}

// credentialWrapAAD is the §3 wrap AAD: credential_id ":" decimal(version) ":" key_id.
func credentialWrapAAD(credentialID string, version uint64, keyID string) []byte {
	return []byte(credentialID + ":" + strconv.FormatUint(version, 10) + ":" + keyID)
}

// uckSeedWrapAAD is the §4 path (i) AAD: user_id ":uck:" decimal(uck_version).
func uckSeedWrapAAD(userID string, uckVersion uint64) []byte {
	return []byte(userID + ":uck:" + strconv.FormatUint(uckVersion, 10))
}

// personalSecretAAD is the §4 AAD: user_id ":" credential_id ":" decimal(version).
func personalSecretAAD(userID, credentialID string, version uint64) []byte {
	return []byte(userID + ":" + credentialID + ":" + strconv.FormatUint(version, 10))
}

// credentialWrapKEK derives the §3 KEK from an X25519 shared secret.
func credentialWrapKEK(shared []byte) ([]byte, error) {
	return hkdf32(shared, string(LabelCredentialWrapV1))
}

// userCredentialsSeedKEK derives the §4 path (i) kek_seed.
func userCredentialsSeedKEK(seed []byte) ([]byte, error) {
	if len(seed) != seedSize {
		return nil, fmt.Errorf("fleet: uck seed kek: seed must be %d bytes", seedSize)
	}
	return hkdf32(seed, string(LabelUserCredentialsKEKV1))
}

// credentialDeviceWrap is one §3 anonymous wrap as it travels on the wire
// (the WrappedSecret byte fields). UCK device wraps use credentialAuthWrap.
type credentialDeviceWrap struct {
	KeyID              string
	EphemeralPublicKey []byte
	Nonce              []byte
	Sealed             []byte
}

// sealToCredentialKeyWith is the deterministic §3 wrap core: explicit
// ephemeral key and nonce (the golden vectors drive it).
func sealToCredentialKeyWith(eph *ecdh.PrivateKey, devicePub, nonce, payload, aad []byte) ([]byte, error) {
	if len(nonce) != credentialNonceLen {
		return nil, fmt.Errorf("fleet: credential wrap: nonce must be %d bytes", credentialNonceLen)
	}
	pub, err := ecdh.X25519().NewPublicKey(devicePub)
	if err != nil {
		return nil, fmt.Errorf("fleet: credential wrap: device key: %w", err)
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		// crypto/ecdh refuses an all-zero shared secret (low-order point).
		return nil, fmt.Errorf("fleet: credential wrap: ECDH: %w", err)
	}
	kek, err := credentialWrapKEK(shared)
	if err != nil {
		return nil, err
	}
	return sealXAAD(kek, nonce, payload, aad)
}

// sealToCredentialKey wraps payload to a device credential key with a
// FRESH ephemeral key pair and nonce drawn from rnd.
func sealToCredentialKey(rnd io.Reader, devicePub []byte, keyID string, payload, aad []byte) (credentialDeviceWrap, error) {
	eph, err := ecdh.X25519().GenerateKey(rnd)
	if err != nil {
		return credentialDeviceWrap{}, fmt.Errorf("fleet: credential wrap: ephemeral key: %w", err)
	}
	nonce := make([]byte, credentialNonceLen)
	if _, err := io.ReadFull(rnd, nonce); err != nil {
		return credentialDeviceWrap{}, fmt.Errorf("fleet: credential wrap: nonce: %w", err)
	}
	sealed, err := sealToCredentialKeyWith(eph, devicePub, nonce, payload, aad)
	if err != nil {
		return credentialDeviceWrap{}, err
	}
	return credentialDeviceWrap{KeyID: keyID, EphemeralPublicKey: eph.PublicKey().Bytes(), Nonce: nonce, Sealed: sealed}, nil
}

// openFromCredentialKey opens a §3 wrap with our device credential key.
func openFromCredentialKey(priv *ecdh.PrivateKey, ephPub, nonce, sealed, aad []byte) ([]byte, error) {
	if priv == nil || len(nonce) != credentialNonceLen {
		return nil, ErrCredentialOpenFailed
	}
	pub, err := ecdh.X25519().NewPublicKey(ephPub)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	shared, err := priv.ECDH(pub)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	kek, err := credentialWrapKEK(shared)
	if err != nil {
		return nil, err
	}
	pt, err := DecryptAAD(kek, sealed, nonce, aad)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	return pt, nil
}

// openWrappedSecret opens an org/team WrappedSecret (§3). The AAD is
// RECOMPUTED from the caller's credential_id / version / key_id — the
// envelope's echoed "aad" field is never trusted.
func openWrappedSecret(priv *ecdh.PrivateKey, credentialID string, version uint64, keyID string, ephPub, nonce, sealed []byte) ([]byte, error) {
	return openFromCredentialKey(priv, ephPub, nonce, sealed, credentialWrapAAD(credentialID, version, keyID))
}

// sealUCKWithSeedWith is the deterministic §4 path (i) seed wrap.
func sealUCKWithSeedWith(seed []byte, userID string, uckVersion uint64, uck, nonce []byte) ([]byte, error) {
	if len(uck) != uckLen {
		return nil, fmt.Errorf("fleet: uck seed wrap: uck must be %d bytes", uckLen)
	}
	kek, err := userCredentialsSeedKEK(seed)
	if err != nil {
		return nil, err
	}
	return sealXAAD(kek, nonce, uck, uckSeedWrapAAD(userID, uckVersion))
}

// sealUCKWithSeed is sealUCKWithSeedWith with a fresh random nonce.
func sealUCKWithSeed(rnd io.Reader, seed []byte, userID string, uckVersion uint64, uck []byte) (sealed, nonce []byte, err error) {
	nonce = make([]byte, credentialNonceLen)
	if _, err := io.ReadFull(rnd, nonce); err != nil {
		return nil, nil, fmt.Errorf("fleet: uck seed wrap: nonce: %w", err)
	}
	sealed, err = sealUCKWithSeedWith(seed, userID, uckVersion, uck, nonce)
	if err != nil {
		return nil, nil, err
	}
	return sealed, nonce, nil
}

// openUCKSeedWrap opens a §4 path (i) seed wrap of the UCK.
func openUCKSeedWrap(seed []byte, userID string, uckVersion uint64, nonce, sealed []byte) ([]byte, error) {
	kek, err := userCredentialsSeedKEK(seed)
	if err != nil {
		return nil, err
	}
	uck, err := DecryptAAD(kek, sealed, nonce, uckSeedWrapAAD(userID, uckVersion))
	if err != nil || len(uck) != uckLen {
		return nil, ErrCredentialOpenFailed
	}
	return uck, nil
}

// sealPersonalSecretWith is the deterministic §4 personal-secret seal.
func sealPersonalSecretWith(uck []byte, userID, credentialID string, version uint64, secret, nonce []byte) ([]byte, error) {
	if len(uck) != uckLen {
		return nil, fmt.Errorf("fleet: personal secret: uck must be %d bytes", uckLen)
	}
	return sealXAAD(uck, nonce, secret, personalSecretAAD(userID, credentialID, version))
}

// sealPersonalSecret is sealPersonalSecretWith with a fresh random nonce.
func sealPersonalSecret(rnd io.Reader, uck []byte, userID, credentialID string, version uint64, secret []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, credentialNonceLen)
	if _, err := io.ReadFull(rnd, nonce); err != nil {
		return nil, nil, fmt.Errorf("fleet: personal secret: nonce: %w", err)
	}
	ciphertext, err = sealPersonalSecretWith(uck, userID, credentialID, version, secret, nonce)
	if err != nil {
		return nil, nil, err
	}
	return ciphertext, nonce, nil
}

// openPersonalSecret opens a §4 personal secret under the UCK.
func openPersonalSecret(uck []byte, userID, credentialID string, version uint64, nonce, ciphertext []byte) ([]byte, error) {
	if len(uck) != uckLen {
		return nil, ErrCredentialOpenFailed
	}
	pt, err := DecryptAAD(uck, ciphertext, nonce, personalSecretAAD(userID, credentialID, version))
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	return pt, nil
}

// ── v2 id validation (contract §2, §6.2) ──

// isCanonicalUUID reports whether s is a canonical LOWERCASE hyphenated
// UUID (8-4-4-4-12 hex). Stricter than IsWireUUID, which accepts upper
// case: the AAD bytes must be one fixed spelling on every platform.
func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// IsCanonicalNodeULID reports whether s is a canonical ULID as §6.2 pins
// for node ids in the pairing SAS info: exactly 26 characters of
// uppercase Crockford base32, [0-9A-HJKMNP-TV-Z] (no I, L, O, U, no
// lower case).
func IsCanonicalNodeULID(s string) bool {
	if len(s) != 26 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		digit := c >= '0' && c <= '9'
		letter := c >= 'A' && c <= 'Z' && c != 'I' && c != 'L' && c != 'O' && c != 'U'
		if !digit && !letter {
			return false
		}
	}
	return true
}

// ── uck_auth (§6.1, spec §10.3) ──

// UCKAuth is uck_auth = HKDF-SHA256(UCK, salt=nil, info="kenaz-uck-auth-v1",
// L=32): a one-way proof of UCK possession that Fleet checks (as
// SHA-256(uck_auth)) before personal writes, rotate and seed-wrap repair.
// It is sent to Fleet, so it is a credential: never log it.
func UCKAuth(uck []byte) ([]byte, error) {
	if len(uck) != uckLen {
		return nil, fmt.Errorf("fleet: uck_auth: uck must be %d bytes", uckLen)
	}
	return hkdf32(uck, string(LabelUCKAuthV1))
}

// UCKAuthHash is uck_auth_hash = SHA-256(uck_auth) for the UCK (sent as
// uck_auth_hash / new_uck_auth_hash, base64 on the wire).
func UCKAuthHash(uck []byte) ([]byte, error) {
	auth, err := UCKAuth(uck)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(auth)
	return h[:], nil
}

// ── pairing commit / reveal + SAS (§6.2, spec §10.1) ──

// PairingCommit is N's step-1 commit = SHA-256("kenaz-pair-commit-v1" ||
// pk_N || nA). pk_N is N's 32-byte credential public key; nA is exactly
// 32 fresh random bytes, revealed only after W's nB is seen.
func PairingCommit(pkN, nA []byte) ([]byte, error) {
	if len(pkN) != x25519KeyLen {
		return nil, fmt.Errorf("fleet: pairing commit: pk_N must be %d bytes", x25519KeyLen)
	}
	if len(nA) != pairNonceLen {
		return nil, fmt.Errorf("fleet: pairing commit: nA must be %d bytes", pairNonceLen)
	}
	h := sha256.New()
	h.Write([]byte(LabelPairCommitV1))
	h.Write(pkN)
	h.Write(nA)
	return h.Sum(nil), nil
}

// pairingCommitMatches is W's step-4 check: recompute the commit from pk_N
// (the key W will pair with) and the revealed nA, and compare in constant
// time. Any malformed input is a mismatch.
func pairingCommitMatches(commit, pkN, nA []byte) bool {
	want, err := PairingCommit(pkN, nA)
	if err != nil || len(commit) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare(commit, want) == 1
}

// PairingSAS is the 8-digit pairing code both devices show:
//
//	b = HKDF-SHA256(ikm = pk_N || pk_W || nA || nB, salt = nil,
//	  info = "kenaz-credential-key-sas-v2:" || user_id || ":" || node_N || ":" || node_W, L = 8)
//	sas = big_endian_uint64(b) mod 10^8, zero-padded to 8 digits.
//
// The "#### ####" display form is the UI's job. user_id must be a
// canonical UUID and node ids canonical ULIDs (ErrCredentialIDInvalid);
// pk_W == pk_N or node_W == node_N is ErrCredentialSelfPairing.
func PairingSAS(userID, nodeN, nodeW string, pkN, pkW, nA, nB []byte) (string, error) {
	if !isCanonicalUUID(userID) || !IsCanonicalNodeULID(nodeN) || !IsCanonicalNodeULID(nodeW) {
		return "", ErrCredentialIDInvalid
	}
	if len(pkN) != x25519KeyLen || len(pkW) != x25519KeyLen {
		return "", fmt.Errorf("fleet: pairing sas: keys must be %d bytes", x25519KeyLen)
	}
	if len(nA) != pairNonceLen || len(nB) != pairNonceLen {
		return "", fmt.Errorf("fleet: pairing sas: nA and nB must be %d bytes", pairNonceLen)
	}
	if nodeN == nodeW || subtle.ConstantTimeCompare(pkN, pkW) == 1 {
		return "", ErrCredentialSelfPairing
	}
	ikm := make([]byte, 0, 2*x25519KeyLen+2*pairNonceLen)
	ikm = append(ikm, pkN...)
	ikm = append(ikm, pkW...)
	ikm = append(ikm, nA...)
	ikm = append(ikm, nB...)
	info := string(LabelCredentialKeySASV2) + userID + ":" + nodeN + ":" + nodeW
	r := hkdf.New(sha256.New, ikm, nil, []byte(info))
	b := make([]byte, pairingSASLen)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", fmt.Errorf("fleet: HKDF expand: %w", err)
	}
	return fmt.Sprintf("%08d", binary.BigEndian.Uint64(b)%pairingSASModulus), nil
}

// ── authenticated UCK device wrap (§6.2 path ii, spec §10.1) ──

// credentialAuthWrap is one authenticated UCK device wrap as it travels on
// the wire (PUT …/device-wraps/{node_id}). key_id_N and key_id_W are bound
// by the AAD, so they are not carried here.
type credentialAuthWrap struct {
	EphemeralPublicKey []byte
	Nonce              []byte
	Sealed             []byte
}

// uckAuthWrapAAD is "uck:" user_id ":" uck_version ":" key_id_N ":" key_id_W,
// after validating every id (contract §2) and a positive version.
func uckAuthWrapAAD(userID string, uckVersion uint64, keyIDN, keyIDW string) ([]byte, error) {
	if uckVersion == 0 || !isCanonicalUUID(userID) || !isCanonicalUUID(keyIDN) || !isCanonicalUUID(keyIDW) {
		return nil, ErrCredentialIDInvalid
	}
	return []byte("uck:" + userID + ":" + strconv.FormatUint(uckVersion, 10) + ":" + keyIDN + ":" + keyIDW), nil
}

// uckAuthWrapKEK is HKDF-SHA256(ikm = dh1 || dh2, salt=nil,
// info = "kenaz-credential-v1-authwrap" || eph_pub || pk_N || pk_W, L=32).
func uckAuthWrapKEK(dh1, dh2, ephPub, pkN, pkW []byte) ([]byte, error) {
	ikm := make([]byte, 0, len(dh1)+len(dh2))
	ikm = append(ikm, dh1...)
	ikm = append(ikm, dh2...)
	info := make([]byte, 0, len(LabelCredentialAuthWrapV1)+3*x25519KeyLen)
	info = append(info, LabelCredentialAuthWrapV1...)
	info = append(info, ephPub...)
	info = append(info, pkN...)
	info = append(info, pkW...)
	return hkdf32(ikm, string(info))
}

// sealUCKAuthWrapWith is the deterministic authwrap core (explicit
// ephemeral key and nonce; the golden vectors drive it). sender is W's
// credential private key; recipientPub is pk_N, which the caller must
// already have authenticated by pairing (SAS or QR).
func sealUCKAuthWrapWith(eph, sender *ecdh.PrivateKey, recipientPub []byte, userID string, uckVersion uint64, keyIDN, keyIDW string, uck, nonce []byte) ([]byte, error) {
	if eph == nil || sender == nil {
		return nil, errors.New("fleet: uck authwrap: missing key")
	}
	if len(uck) != uckLen {
		return nil, fmt.Errorf("fleet: uck authwrap: uck must be %d bytes", uckLen)
	}
	if len(nonce) != credentialNonceLen {
		return nil, fmt.Errorf("fleet: uck authwrap: nonce must be %d bytes", credentialNonceLen)
	}
	aad, err := uckAuthWrapAAD(userID, uckVersion, keyIDN, keyIDW)
	if err != nil {
		return nil, err
	}
	pkW := sender.PublicKey().Bytes()
	if subtle.ConstantTimeCompare(pkW, recipientPub) == 1 {
		return nil, ErrCredentialSelfPairing
	}
	pkN, err := ecdh.X25519().NewPublicKey(recipientPub)
	if err != nil {
		return nil, fmt.Errorf("fleet: uck authwrap: recipient key: %w", err)
	}
	// crypto/ecdh refuses an all-zero shared secret (low-order point).
	dh1, err := eph.ECDH(pkN)
	if err != nil {
		return nil, fmt.Errorf("fleet: uck authwrap: ECDH: %w", err)
	}
	dh2, err := sender.ECDH(pkN)
	if err != nil {
		return nil, fmt.Errorf("fleet: uck authwrap: ECDH: %w", err)
	}
	kek, err := uckAuthWrapKEK(dh1, dh2, eph.PublicKey().Bytes(), recipientPub, pkW)
	if err != nil {
		return nil, err
	}
	return sealXAAD(kek, nonce, uck, aad)
}

// sealUCKAuthWrap is sealUCKAuthWrapWith with a FRESH ephemeral key and
// nonce drawn from rnd.
func sealUCKAuthWrap(rnd io.Reader, sender *ecdh.PrivateKey, recipientPub []byte, userID string, uckVersion uint64, keyIDN, keyIDW string, uck []byte) (credentialAuthWrap, error) {
	eph, err := ecdh.X25519().GenerateKey(rnd)
	if err != nil {
		return credentialAuthWrap{}, fmt.Errorf("fleet: uck authwrap: ephemeral key: %w", err)
	}
	nonce := make([]byte, credentialNonceLen)
	if _, err := io.ReadFull(rnd, nonce); err != nil {
		return credentialAuthWrap{}, fmt.Errorf("fleet: uck authwrap: nonce: %w", err)
	}
	sealed, err := sealUCKAuthWrapWith(eph, sender, recipientPub, userID, uckVersion, keyIDN, keyIDW, uck, nonce)
	if err != nil {
		return credentialAuthWrap{}, err
	}
	return credentialAuthWrap{EphemeralPublicKey: eph.PublicKey().Bytes(), Nonce: nonce, Sealed: sealed}, nil
}

// openUCKAuthWrap opens an authenticated UCK device wrap on N.
// expectedPkW is the sender key N PAIRED WITH, resolved from N's local
// paired-key table by key_id_W, never a key Fleet supplies: the wrap
// opens only if it was sealed by the holder of that key's private half
// (or of N's own, the accepted KCI case). pk_W == pk_N is refused. Ids
// and the version are re-validated and the AAD recomputed.
func openUCKAuthWrap(recipient *ecdh.PrivateKey, expectedPkW []byte, userID string, uckVersion uint64, keyIDN, keyIDW string, ephPub, nonce, sealed []byte) ([]byte, error) {
	aad, err := uckAuthWrapAAD(userID, uckVersion, keyIDN, keyIDW)
	if err != nil {
		return nil, err
	}
	if recipient == nil || len(nonce) != credentialNonceLen {
		return nil, ErrCredentialOpenFailed
	}
	pkN := recipient.PublicKey().Bytes()
	if subtle.ConstantTimeCompare(pkN, expectedPkW) == 1 {
		return nil, ErrCredentialSelfPairing
	}
	senderPub, err := ecdh.X25519().NewPublicKey(expectedPkW)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	ephKey, err := ecdh.X25519().NewPublicKey(ephPub)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	dh1, err := recipient.ECDH(ephKey)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	dh2, err := recipient.ECDH(senderPub)
	if err != nil {
		return nil, ErrCredentialOpenFailed
	}
	kek, err := uckAuthWrapKEK(dh1, dh2, ephPub, pkN, expectedPkW)
	if err != nil {
		return nil, err
	}
	uck, err := DecryptAAD(kek, sealed, nonce, aad)
	if err != nil || len(uck) != uckLen {
		return nil, ErrCredentialOpenFailed
	}
	return uck, nil
}
