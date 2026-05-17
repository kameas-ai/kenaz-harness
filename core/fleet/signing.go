package fleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// deviceKeyFile is the filename within DataDir where the device ed25519 key is
// persisted. The file is owner-read-only (mode 0600) and contains a JSON
// envelope to allow schema evolution.
const deviceKeyFile = "fleet_device_key.json"

// deviceKeyEnvelope is the on-disk JSON shape for the device key.
type deviceKeyEnvelope struct {
	// Algorithm is always "ed25519" for now — reserved for future rotation.
	Algorithm string `json:"algorithm"`
	// PrivKeyHex is the hex-encoded 64-byte ed25519 private key
	// (seed || public key, as returned by crypto/ed25519).
	PrivKeyHex string `json:"priv_key_hex"`
}

// DeviceSigner implements Signer using a device-local ed25519 key pair.
// The key is generated on first use and persisted under DataDir. Subsequent
// calls load the persisted key, making the fingerprint stable for the device's
// lifetime.
//
// Thread-safe: all fields are immutable after construction.
type DeviceSigner struct {
	privKey ed25519.PrivateKey
	pubkeyFP string // sha256:<hex> fingerprint of the public key
}

// NewDeviceSigner loads (or creates) the device signing key from/in dataDir.
// dataDir is the harness data directory (e.g. ~/.kenaz).
// The key is persisted at dataDir/fleet_device_key.json with mode 0600.
func NewDeviceSigner(dataDir string) (*DeviceSigner, error) {
	if dataDir == "" {
		return nil, errors.New("fleet/signing: NewDeviceSigner: dataDir must not be empty")
	}
	keyPath := filepath.Join(dataDir, deviceKeyFile)
	privKey, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("fleet/signing: NewDeviceSigner: %w", err)
	}
	fp := pubkeyFingerprint(privKey.Public().(ed25519.PublicKey))
	return &DeviceSigner{privKey: privKey, pubkeyFP: fp}, nil
}

// Sign implements Signer. It signs payload using the device ed25519 key and
// returns the base64-encoded signature and the public key fingerprint.
func (s *DeviceSigner) Sign(payload []byte) (sig string, pubkeyFP string, err error) {
	if s.privKey == nil {
		return "", "", errors.New("fleet/signing: Sign: key not initialised")
	}
	raw := ed25519.Sign(s.privKey, payload)
	return base64.StdEncoding.EncodeToString(raw), s.pubkeyFP, nil
}

// PubkeyFingerprint returns the sha256-prefixed hex fingerprint of the device
// public key. Stable for the device's lifetime.
func (s *DeviceSigner) PubkeyFingerprint() string { return s.pubkeyFP }

// ── internal helpers ────────────────────────────────────────────────────────

// keyGenMu serialises concurrent first-use key generation across goroutines
// that race to create the file.
var keyGenMu sync.Mutex

// loadOrCreateKey loads the ed25519 private key from path. If the file does
// not exist, a fresh key pair is generated, persisted, and returned.
func loadOrCreateKey(path string) (ed25519.PrivateKey, error) {
	// Try reading the existing key first — optimistic path (no lock).
	if k, err := readKeyFile(path); err == nil {
		return k, nil
	}

	// File missing or unreadable: generate under the lock to avoid a
	// thundering-herd double-write.
	keyGenMu.Lock()
	defer keyGenMu.Unlock()

	// Double-check under the lock in case another goroutine raced.
	if k, err := readKeyFile(path); err == nil {
		return k, nil
	}

	// Generate a fresh key pair.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	if err := writeKeyFile(path, priv); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}
	return priv, nil
}

// readKeyFile reads and decodes the envelope at path. Returns an error when
// the file is absent, malformed, or the algorithm is unsupported.
func readKeyFile(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var env deviceKeyEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("unmarshal key envelope: %w", err)
	}
	if env.Algorithm != "ed25519" {
		return nil, fmt.Errorf("unsupported algorithm %q", env.Algorithm)
	}
	raw, err := hex.DecodeString(env.PrivKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode priv_key_hex: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("wrong private key length: want %d got %d",
			ed25519.PrivateKeySize, len(raw))
	}
	return ed25519.PrivateKey(raw), nil
}

// writeKeyFile serialises priv to the JSON envelope and writes it to path
// with mode 0600. Parent directories are created with 0700 if needed.
func writeKeyFile(path string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	env := deviceKeyEnvelope{
		Algorithm:  "ed25519",
		PrivKeyHex: hex.EncodeToString(priv),
	}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// pubkeyFingerprint returns "sha256:<hex>" fingerprint of the given public key
// bytes. This format matches the fleet server's device_pubkey_fingerprint field.
func pubkeyFingerprint(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return "sha256:" + hex.EncodeToString(h[:])
}

// VerifySignature verifies that sig is a valid ed25519 signature over payload
// using the provided base64-encoded public key or fingerprint. Used by tests
// and the fleet server to validate incoming batches.
//
// pubKeyBase64 is the standard base64 encoding of the raw 32-byte ed25519
// public key (NOT the fingerprint). For fingerprint verification, callers must
// look up the key from a key registry.
func VerifySignature(pubKeyBase64 string, payload []byte, sigBase64 string) (bool, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(pubKeyBase64)
	if err != nil {
		return false, fmt.Errorf("fleet/signing: decode pubkey: %w", err)
	}
	if len(pubBytes) != ed25519.PublicKeySize {
		return false, fmt.Errorf("fleet/signing: wrong pubkey length: want %d got %d",
			ed25519.PublicKeySize, len(pubBytes))
	}
	sigBytes, err := base64.StdEncoding.DecodeString(sigBase64)
	if err != nil {
		return false, fmt.Errorf("fleet/signing: decode sig: %w", err)
	}
	return ed25519.Verify(ed25519.PublicKey(pubBytes), payload, sigBytes), nil
}
