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
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
	"crypto/sha256"

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
)

// SeedKey ensures a context seed exists in the OS keychain. If no seed exists,
// it generates a fresh 32-byte random seed and persists it. Returns the seed
// bytes. Safe to call multiple times; subsequent calls are idempotent.
func SeedKey() ([]byte, error) {
	svc := paths.FleetKeychainService()
	existing, err := keyring.Get(svc, contextSeedAccount)
	if err == nil && existing != "" {
		seed, decErr := base64.StdEncoding.DecodeString(existing)
		if decErr != nil {
			return nil, fmt.Errorf("fleet: context seed decode: %w", decErr)
		}
		if len(seed) != seedSize {
			return nil, fmt.Errorf("fleet: context seed bad length %d (want %d)", len(seed), seedSize)
		}
		return seed, nil
	}
	// Generate a fresh seed.
	seed := make([]byte, seedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, fmt.Errorf("fleet: generate context seed: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(seed)
	if err := keyring.Set(svc, contextSeedAccount, encoded); err != nil {
		return nil, fmt.Errorf("fleet: persist context seed: %w", err)
	}
	logging.L().Info("fleet.context_crypto.seed_generated")
	return seed, nil
}

// LoadContextSeed reads the seed from the OS keychain without generating one.
// Returns ErrContextSeedNotFound when no seed exists.
func LoadContextSeed() ([]byte, error) {
	svc := paths.FleetKeychainService()
	existing, err := keyring.Get(svc, contextSeedAccount)
	if err != nil || existing == "" {
		return nil, ErrContextSeedNotFound
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

// StoreContextSeed writes a raw seed to the OS keychain, overwriting any
// existing value. Used by UseRecoveryCode to import a seed from another device.
func StoreContextSeed(seed []byte) error {
	if len(seed) != seedSize {
		return fmt.Errorf("fleet: seed must be %d bytes, got %d", seedSize, len(seed))
	}
	svc := paths.FleetKeychainService()
	encoded := base64.StdEncoding.EncodeToString(seed)
	if err := keyring.Set(svc, contextSeedAccount, encoded); err != nil {
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

// MintRecoveryCode encodes seed as a URL-safe base64 string prefixed with
// "KENAZ-" and chunked for readability. The code is safe to display to the
// user once; the caller must not log or persist it.
//
// Format: KENAZ-<base64url-block1>-<base64url-block2>-...
// Block size: 8 chars per block for readability.
//
// Privacy invariant: the returned code contains the raw seed. Never log it.
func MintRecoveryCode(seed []byte) (string, error) {
	if len(seed) != seedSize {
		return "", fmt.Errorf("fleet: seed must be %d bytes", seedSize)
	}
	// Use raw URL-safe base64 (no padding).
	encoded := base64.RawURLEncoding.EncodeToString(seed)
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
	seed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: base64 decode: %v", ErrBadRecoveryCode, err)
	}
	if len(seed) != seedSize {
		return nil, fmt.Errorf("%w: decoded seed is %d bytes (want %d)", ErrBadRecoveryCode, len(seed), seedSize)
	}
	return seed, nil
}
