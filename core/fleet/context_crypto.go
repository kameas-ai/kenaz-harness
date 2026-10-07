package fleet

// context_crypto.go — per-user AEAD encryption for fleet context sync.
//
// Key derivation chain:
//   credstore[fleet:<env>:context_seed] → 32-byte random seed (device-scoped)
//   HKDF-SHA256(seed, label) → 32-byte symmetric key per stream type
//   XChaCha20-Poly1305(key, nonce, plaintext) → ciphertext
//
// The seed is stored in the OS keychain under the same service as fleet tokens.
// It is NEVER transmitted to fleet. Fleet stores ciphertext only.
//
// Recovery flow: MintRecoveryCode encodes the seed as a BIP-39-style mnemonic.
// UseRecoveryCode decodes it back to seed bytes. Import the seed on a second
// device to decrypt fleet-stored sessions from the first device.
//
// Privacy invariant: no plaintext context bytes appear in slog or in the wire
// layer. Raw key material never leaves this file.

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// Seed size in bytes. HKDF expands this into per-label keys.
const seedSize = 32

// keyringService is shared with token_store.go — same OS keychain service.
var contextSeedAccount = "fleet:" + paths.KeychainNamespace() + ":context_seed"

// ErrContextSeedNotFound is returned by LoadContextSeed when the seed has not
// been initialised for this device yet (no sync enabled).
var ErrContextSeedNotFound = errors.New("fleet: context seed not found in keychain")

// ErrBadRecoveryCode is returned by UseRecoveryCode when the code is
// syntactically invalid.
var ErrBadRecoveryCode = errors.New("fleet: invalid recovery code")

// ErrDecryptionFailed is returned by Decrypt when AEAD authentication fails,
// indicating tampering or an incorrect key.
var ErrDecryptionFailed = errors.New("fleet: AEAD decryption failed")

// DeriveLabel enumerates the HKDF labels used to derive per-stream keys.
type DeriveLabel string

const (
	// LabelSessionEvents is the HKDF label for session event stream keys.
	LabelSessionEvents DeriveLabel = "session-events-v1"
	// LabelProjectEvents is the HKDF label for project event stream keys.
	LabelProjectEvents DeriveLabel = "project-events-v1"
	// LabelHandoffKey is the HKDF label of the LEGACY v1 "direct" handoff
	// mode: the AEAD key events are sealed under directly is
	// HKDF(X25519(eph, recipient), info="handoff-v1"). The harness no
	// longer SENDS v1 (device-keys-handoff-01DEVKH01 OQ-8); it is kept for
	// ACCEPTING mode:"direct" items, which fleet still stores while a v0.91
	// sender addresses a single-key recipient (fleet contract §10.3, O5 —
	// we tell fleet when the accept arm can die).
	LabelHandoffKey DeriveLabel = "handoff-v1"
	// LabelHandoffWrapV2 is the PINNED v2 key-wrap HKDF info (fleet
	// contract §10.3 "PINNED v2 wrap construction"):
	// kek = HKDF-SHA256(ikm=X25519(eph, recipient), salt=nil,
	// info="kenaz-handoff-v2-wrap", L=32). Never change it — every
	// in-flight inbox item would become undecryptable.
	LabelHandoffWrapV2 DeriveLabel = "kenaz-handoff-v2-wrap"

	// HEADSTONE (device-keys-handoff-01DEVKH01 WP01, 2026-10-07):
	// LabelHandoffIdentity ("handoff-identity-v1") is DELETED. It derived
	// the handoff identity from the seed ALONE, so two installs sharing a
	// seed (recovery-code import) derived the SAME key — the multi-device
	// collision per-device keys exist to kill. Clean cutover, no fallback:
	// the harness never uploaded a v1-derived public key anywhere (fleet's
	// key routes shipped with #182), so no inbox item in any environment is
	// wrapped to one. The v2 derivation is handoffIdentityV2Info below.
)

// handoffIdentityV2Info is the HKDF info prefix of the per-device handoff
// identity: handoff_priv = HKDF-SHA256(ikm=seed, salt=nil,
// info="kenaz-handoff-identity-v2:"+node_id, L=32). Device-salted by the
// node id, so a recovery-code import on a second install yields a
// DIFFERENT key (by design; fleet makes no promise that an import recovers
// items wrapped to another device), while the same seed + node id always
// re-derives the same key (re-enroll is a fleet no-op, not a rotation).
const handoffIdentityV2Info = "kenaz-handoff-identity-v2:"

// hkdf32 expands ikm into 32 bytes with HKDF-SHA256, salt=nil and the
// given info string. It is the labeled-derive helper for DYNAMIC labels
// (the per-device identity) and for ECDH shared secrets; DeriveKey keeps
// the fixed-label stream keys.
func hkdf32(ikm []byte, info string) ([]byte, error) {
	r := hkdf.New(sha256.New, ikm, nil, []byte(info))
	out := make([]byte, 32)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, fmt.Errorf("fleet: HKDF expand: %w", err)
	}
	return out, nil
}

// ErrKeychainUnavailable is returned when the OS keychain answered with
// anything other than "not found" (locked keychain, denied prompt,
// transient backend error). The seed is a ROOT secret: such an error must
// never be read as "no seed yet" — minting a replacement would make every
// synced stream undecryptable and silently rotate the handoff key.
var ErrKeychainUnavailable = errors.New("fleet: OS keychain unavailable")

// seedMu serialises every read-modify-write of the context seed, so two
// first-run callers (enroll + sync enable) cannot each mint a seed and
// race last-write-wins on a root secret.
var seedMu sync.Mutex

// seedKeyringGet / seedKeyringSet are the keychain seam for the seed (a
// test substitutes a failing backend; production is core/keyring).
var (
	seedKeyringGet = keyring.Get
	seedKeyringSet = keyring.Set
)

// readSeedLocked reads the stored seed. (nil, nil) means DEFINITELY absent
// (keyring.ErrNotFound, or an empty value that cannot be a seed); any other
// keychain error is ErrKeychainUnavailable. Caller holds seedMu.
func readSeedLocked() ([]byte, error) {
	existing, err := seedKeyringGet(paths.FleetKeychainService(), contextSeedAccount)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
	case existing == "":
		return nil, nil
	}
	seed, decErr := base64.StdEncoding.DecodeString(existing)
	if decErr != nil {
		return nil, fmt.Errorf("fleet: context seed decode: %w", decErr)
	}
	if len(seed) != seedSize {
		return nil, fmt.Errorf("fleet: context seed bad length %d (want %d)", len(seed), seedSize)
	}
	return seed, nil
}

// SeedKey ensures a context seed exists in the OS keychain and returns it.
// It generates one ONLY when the keychain says the seed is definitely
// absent (keyring.ErrNotFound); any other keychain error is returned as
// ErrKeychainUnavailable and nothing is written. Serialised: concurrent
// first-run callers get the same seed.
func SeedKey() ([]byte, error) {
	seedMu.Lock()
	defer seedMu.Unlock()
	seed, err := readSeedLocked()
	if err != nil {
		return nil, err
	}
	if seed != nil {
		return seed, nil
	}
	seed = make([]byte, seedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("fleet: generate context seed: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(seed)
	if err := seedKeyringSet(paths.FleetKeychainService(), contextSeedAccount, encoded); err != nil {
		return nil, fmt.Errorf("fleet: persist context seed: %w", err)
	}
	logging.L().Info("fleet.context_crypto.seed_generated")
	return seed, nil
}

// LoadContextSeed reads the seed from the OS keychain without generating one.
// Returns ErrContextSeedNotFound when no seed exists and
// ErrKeychainUnavailable (wrapped) when the keychain could not be read.
func LoadContextSeed() ([]byte, error) {
	seedMu.Lock()
	defer seedMu.Unlock()
	seed, err := readSeedLocked()
	if err != nil {
		return nil, err
	}
	if seed == nil {
		return nil, ErrContextSeedNotFound
	}
	return seed, nil
}

// StoreContextSeed writes a raw seed to the OS keychain, overwriting any
// existing value. Used by UseRecoveryCode to import a seed from another
// device — an explicit user action, the only sanctioned overwrite.
func StoreContextSeed(seed []byte) error {
	if len(seed) != seedSize {
		return fmt.Errorf("fleet: seed must be %d bytes, got %d", seedSize, len(seed))
	}
	seedMu.Lock()
	defer seedMu.Unlock()
	encoded := base64.StdEncoding.EncodeToString(seed)
	if err := seedKeyringSet(paths.FleetKeychainService(), contextSeedAccount, encoded); err != nil {
		return fmt.Errorf("fleet: persist imported context seed: %w", err)
	}
	logging.L().Info("fleet.context_crypto.seed_imported")
	return nil
}

// DeriveKey derives a 32-byte symmetric key from seed using HKDF-SHA256 with
// the given label. The label differentiates session keys from project keys so
// a compromise of one stream type does not affect others.
func DeriveKey(seed []byte, label DeriveLabel) ([]byte, error) {
	if len(seed) != seedSize {
		return nil, fmt.Errorf("fleet: DeriveKey: seed must be %d bytes", seedSize)
	}
	r := hkdf.New(sha256.New, seed, nil, []byte(label))
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("fleet: HKDF expand: %w", err)
	}
	return key, nil
}

// ErrHandoffIdentityUnavailable is returned when the device handoff
// identity cannot be derived because there is no persistent node id (no
// data dir). Deriving from a transient node id would mint a key nobody can
// ever re-derive, so it is refused.
var ErrHandoffIdentityUnavailable = errors.New("fleet: handoff identity unavailable: no data dir for a persistent node id")

// DeriveHandoffPrivKey derives the per-device X25519 handoff identity from
// the context seed and the device's node id (device-keys-handoff-01DEVKH01
// FR-1): HKDF-SHA256(seed, salt=nil, info="kenaz-handoff-identity-v2:"+
// nodeID) → 32 raw scalar bytes → X25519 private key (crypto/ecdh clamps).
//
// Privacy invariant: the private key bytes are never logged.
func DeriveHandoffPrivKey(seed []byte, nodeID string) (*ecdh.PrivateKey, error) {
	if len(seed) != seedSize {
		return nil, fmt.Errorf("fleet: handoff identity: seed must be %d bytes", seedSize)
	}
	if strings.TrimSpace(nodeID) == "" {
		return nil, ErrHandoffIdentityUnavailable
	}
	scalar, err := hkdf32(seed, handoffIdentityV2Info+nodeID)
	if err != nil {
		return nil, fmt.Errorf("fleet: derive handoff identity scalar: %w", err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(scalar)
	if err != nil {
		return nil, fmt.Errorf("fleet: build X25519 private key: %w", err)
	}
	return priv, nil
}

// LoadOwnHandoffPrivKey derives this device's handoff identity from the
// keychain seed and the persistent node id under dataDir. Returns
// ErrContextSeedNotFound (wrapped) when no seed exists yet — enroll mints
// one (OQ-4), so a signed-in device normally has it.
//
// Privacy invariant: the private key bytes are never logged.
func LoadOwnHandoffPrivKey(dataDir string) (*ecdh.PrivateKey, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, ErrHandoffIdentityUnavailable
	}
	seed, err := LoadContextSeed()
	if err != nil {
		return nil, fmt.Errorf("fleet: load own handoff priv key: %w", err)
	}
	nodeID, err := NodeID(dataDir)
	if err != nil {
		return nil, fmt.Errorf("fleet: load own handoff priv key: node id: %w", err)
	}
	return DeriveHandoffPrivKey(seed, nodeID)
}

// KeyFingerprint is "sha256:<hex>" of a raw public key — the format fleet
// stores in device_keys.fingerprint and returns in public_keys[] and
// handoff recipients[] (same as the signing key's, signing.go).
func KeyFingerprint(pub []byte) string {
	sum := sha256.Sum256(pub)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EncryptAAD seals plaintext with XChaCha20-Poly1305 under key with
// additional data aad and a fresh random 24-byte nonce.
//
// Privacy invariant: plaintext bytes are never logged.
func EncryptAAD(key, plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, chacha20poly1305.NonceSizeX)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("fleet: generate nonce: %w", err)
	}
	ct, err := sealXAAD(key, nonce, plaintext, aad)
	if err != nil {
		return nil, nil, err
	}
	return ct, nonce, nil
}

// DecryptAAD opens an XChaCha20-Poly1305 ciphertext bound to aad. Any
// authentication failure (wrong key, tampered bytes, different aad) is
// ErrDecryptionFailed.
func DecryptAAD(key, ciphertext, nonce, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("fleet: build cipher for decrypt: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, ErrDecryptionFailed
	}
	pt, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return pt, nil
}

// sealXAAD is the deterministic core of EncryptAAD (explicit nonce) — the
// golden-vector tests drive it directly.
func sealXAAD(key, nonce, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("fleet: build cipher: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("fleet: nonce must be %d bytes", aead.NonceSize())
	}
	return aead.Seal(nil, nonce, plaintext, aad), nil
}

// Encrypt encrypts plaintext with XChaCha20-Poly1305 using key.
// Returns (ciphertext, nonce, nil) on success.
// The nonce is randomly generated per call and must be stored alongside the
// ciphertext for decryption.
//
// Privacy invariant: plaintext bytes are never logged.
func Encrypt(key, plaintext []byte) (ciphertext, nonce []byte, err error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet: build cipher: %w", err)
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("fleet: generate nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, plaintext, nil)
	return ct, nonce, nil
}

// Decrypt decrypts ciphertext with XChaCha20-Poly1305 using key and nonce.
// Returns ErrDecryptionFailed if authentication fails (wrong key or tampered
// ciphertext).
//
// Privacy invariant: returned plaintext bytes are never logged.
func Decrypt(key, ciphertext, nonce []byte) ([]byte, error) {
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, fmt.Errorf("fleet: build cipher for decrypt: %w", err)
	}
	pt, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}
	return pt, nil
}

// ── Recovery code ──────────────────────────────────────────────────────────────

// recoveryCodePrefix is a human-readable prefix to distinguish recovery codes.
const recoveryCodePrefix = "KENAZ"

// MintRecoveryCode encodes seed as a standard (non-URL-safe) base64 string
// prefixed with "KENAZ-" and chunked for readability. Standard base64 uses
// A-Z, a-z, 0-9, +, / (no hyphens), so the "-" character is safe as a
// block delimiter.
//
// Format: KENAZ-<base64std-block1>-<base64std-block2>-...
// Block size: 8 chars per block for readability.
//
// Privacy invariant: the returned code contains the raw seed. Never log it.
func MintRecoveryCode(seed []byte) (string, error) {
	if len(seed) != seedSize {
		return "", fmt.Errorf("fleet: seed must be %d bytes", seedSize)
	}
	// Use standard base64 (no padding). Standard base64 alphabet does not
	// contain "-", so the hyphen delimiter is safe to use.
	encoded := base64.RawStdEncoding.EncodeToString(seed)
	// Chunk into 8-char blocks for readability.
	var blocks []string
	blocks = append(blocks, recoveryCodePrefix)
	for i := 0; i < len(encoded); i += 8 {
		end := i + 8
		if end > len(encoded) {
			end = len(encoded)
		}
		blocks = append(blocks, encoded[i:end])
	}
	return strings.Join(blocks, "-"), nil
}

// UseRecoveryCode decodes a recovery code produced by MintRecoveryCode and
// returns the seed bytes. The caller should then call StoreContextSeed to
// persist it to the keychain, enabling decryption of fleet-stored sessions.
//
// Returns ErrBadRecoveryCode for any syntactically invalid input.
func UseRecoveryCode(code string) ([]byte, error) {
	code = strings.TrimSpace(code)
	parts := strings.Split(code, "-")
	if len(parts) < 2 {
		return nil, ErrBadRecoveryCode
	}
	// First block must be the prefix.
	if parts[0] != recoveryCodePrefix {
		return nil, ErrBadRecoveryCode
	}
	// Rejoin the data blocks.
	encoded := strings.Join(parts[1:], "")
	seed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode: %v", ErrBadRecoveryCode, err)
	}
	if len(seed) != seedSize {
		return nil, fmt.Errorf("%w: decoded seed is %d bytes (want %d)", ErrBadRecoveryCode, len(seed), seedSize)
	}
	return seed, nil
}
