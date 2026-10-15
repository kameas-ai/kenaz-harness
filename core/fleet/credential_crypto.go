package fleet

// credential_crypto.go — the PINNED provider-credential constructions
// (provider-credentials-client-01PCCLI01 spec §8; kenaz-fleet
// kitty-specs/provider-credentials-01NPCRED01/contract-draft.md §3, §4, §7,
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
//	Payload = the org/team secret (this section) or the UCK (§4 path ii).
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
// Contract §4 "Path (ii) device":
//
//	uck_device_wrap[node] = the §3 Wrap with payload = UCK (same info
//	"kenaz-credential-v1-wrap"). AAD fields for a UCK wrap: credential_id =
//	"uck:" user_id, version = uck_version.
//	⇒ aad = "uck:" user_id ":" uck_version ":" key_id  (pinned by the
//	  golden vector here, as the contract asks).
//
// Contract §7.1: device key "kenaz-credential-identity-v1:" || node_id (salt
// nil, clamped); wrap "kenaz-credential-v1-wrap"; seed KEK
// "kenaz-user-credentials-kek-v1"; seed_key_check "kenaz-seed-key-check-v1".
// §7.2 (D8): XChaCha20-Poly1305 everywhere.
//
// Encodings: every version is a base-10 ASCII integer with no padding or
// sign; separators are a single ASCII ":"; ids are used as the exact
// strings Fleet returns. On the wire all byte strings are standard base64
// with padding; seed_key_check alone is lowercase hex.
//
// TestCredentialV1Vectors pins every label and AAD against golden bytes,
// against testdata/credentials/vectors.json, AND against an independent
// in-test reconstruction (manual HKDF + HChaCha20 + curve25519).
//
// Privacy invariant: no key material, UCK, seed or plaintext is ever
// logged here.

import (
	"crypto/ecdh"
	"crypto/sha256"
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
	// LabelCredentialWrapV1 is the §3 wrap KEK info (payload = org/team
	// secret or the UCK).
	LabelCredentialWrapV1 DeriveLabel = "kenaz-credential-v1-wrap"
	// LabelUserCredentialsKEKV1 is the §4 path (i) seed KEK info.
	LabelUserCredentialsKEKV1 DeriveLabel = "kenaz-user-credentials-kek-v1"
	// LabelSeedKeyCheckV1 is the §4 seed_key_check info (L = 16).
	LabelSeedKeyCheckV1 DeriveLabel = "kenaz-seed-key-check-v1"

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
)

// ErrCredentialOpenFailed means a credential envelope did not open: wrong
// device key, wrong seed/UCK, an AAD mismatch (credential_id, version,
// key_id, user_id, uck_version), or tampered bytes. Deliberately one
// sentinel so the failure never says which input was wrong.
var ErrCredentialOpenFailed = errors.New("fleet: credential envelope open failed")

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

// uckDeviceWrapAAD is the §4 path (ii) AAD: the §3 wrap AAD with
// credential_id = "uck:"+user_id and version = uck_version.
func uckDeviceWrapAAD(userID string, uckVersion uint64, keyID string) []byte {
	return credentialWrapAAD("uck:"+userID, uckVersion, keyID)
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

// credentialDeviceWrap is one §3 wrap as it travels on the wire (the
// WrappedSecret / uck_device_wrap byte fields).
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

// wrapUCKToDevice writes a §4 path (ii) device wrap of the UCK for one of
// the user's own nodes.
func wrapUCKToDevice(rnd io.Reader, devicePub []byte, userID string, uckVersion uint64, keyID string, uck []byte) (credentialDeviceWrap, error) {
	if len(uck) != uckLen {
		return credentialDeviceWrap{}, fmt.Errorf("fleet: uck device wrap: uck must be %d bytes", uckLen)
	}
	return sealToCredentialKey(rnd, devicePub, keyID, uck, uckDeviceWrapAAD(userID, uckVersion, keyID))
}

// openUCKDeviceWrap opens a §4 path (ii) device wrap of the UCK.
func openUCKDeviceWrap(priv *ecdh.PrivateKey, userID string, uckVersion uint64, keyID string, ephPub, nonce, sealed []byte) ([]byte, error) {
	uck, err := openFromCredentialKey(priv, ephPub, nonce, sealed, uckDeviceWrapAAD(userID, uckVersion, keyID))
	if err != nil {
		return nil, err
	}
	if len(uck) != uckLen {
		return nil, ErrCredentialOpenFailed
	}
	return uck, nil
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
