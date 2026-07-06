package fleet

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// signBundle signs a Bundle the same way the fleet server would:
// JSON-marshal the signing payload → SHA-256 → ed25519.Sign.
func signBundle(t *testing.T, priv ed25519.PrivateKey, b *Bundle) {
	t.Helper()
	payload, err := b.signingPayload()
	if err != nil {
		t.Fatalf("sign: marshal payload: %v", err)
	}
	hash := sha256.Sum256(payload)
	sig := ed25519.Sign(priv, hash[:])
	b.Signature = base64.RawStdEncoding.EncodeToString(sig)
}

func newTestKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func sampleBundle() *Bundle {
	return &Bundle{
		BundleID: 42,
		IssuedAt: time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		MCPAllowlist: []string{"github", "slack"},
		ModelPrefs: &BundleModelPrefs{
			DefaultModel:      "anthropic/claude-opus-4",
			ProviderAllowlist: []string{"anthropic", "openai"},
		},
		CedarDelta: json.RawMessage(`{"rules":[]}`),
	}
}

// TestVerify_SignedRoundTrip verifies that a properly signed bundle passes.
func TestVerify_SignedRoundTrip(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	if err := Verify(b, pub, 0); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// TestVerify_TamperedSignature verifies that altering the signature fails.
func TestVerify_TamperedSignature(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	// Corrupt the last byte of the signature.
	sigBytes, _ := base64.RawStdEncoding.DecodeString(b.Signature)
	sigBytes[len(sigBytes)-1] ^= 0xff
	b.Signature = base64.RawStdEncoding.EncodeToString(sigBytes)

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerify_TamperedPayload verifies that altering a payload field fails.
func TestVerify_TamperedPayload(t *testing.T) {
	pub, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	// Mutate a payload field after signing.
	b.MCPAllowlist = append(b.MCPAllowlist, "evil-recipe")

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerify_ReplayRejected verifies the monotonic bundle_id guard.
func TestVerify_ReplayRejected(t *testing.T) {
	pub, priv := newTestKeyPair(t)

	// First bundle: ID=5, last applied = 0 → accepted.
	b1 := sampleBundle()
	b1.BundleID = 5
	signBundle(t, priv, b1)
	if err := Verify(b1, pub, 0); err != nil {
		t.Fatalf("b1 should pass: %v", err)
	}

	// Same bundle replayed against last=5 → rejected.
	if err := Verify(b1, pub, 5); !errors.Is(err, ErrBundleIDNonMonotonic) {
		t.Errorf("replay: expected ErrBundleIDNonMonotonic, got: %v", err)
	}

	// Lower bundle ID replayed → also rejected.
	b2 := sampleBundle()
	b2.BundleID = 3
	signBundle(t, priv, b2)
	if err := Verify(b2, pub, 5); !errors.Is(err, ErrBundleIDNonMonotonic) {
		t.Errorf("lower ID: expected ErrBundleIDNonMonotonic, got: %v", err)
	}

	// Next sequential bundle → accepted.
	b3 := sampleBundle()
	b3.BundleID = 6
	signBundle(t, priv, b3)
	if err := Verify(b3, pub, 5); err != nil {
		t.Errorf("b3 should pass: %v", err)
	}
}

// TestVerify_NilKey verifies that a nil signing key returns ErrSigningKeyNotConfigured.
func TestVerify_NilKey(t *testing.T) {
	_, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	err := Verify(b, nil, 0)
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("expected ErrSigningKeyNotConfigured, got: %v", err)
	}
}

// TestVerify_EmptySignature verifies that an empty signature field fails cleanly.
func TestVerify_EmptySignature(t *testing.T) {
	pub, _ := newTestKeyPair(t)
	b := sampleBundle()
	b.Signature = ""

	err := Verify(b, pub, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("expected ErrInvalidSignature, got: %v", err)
	}
}

// TestFleetSigningKey_EmptyBytes verifies FleetSigningKey returns nil when
// the ldflags variable is not populated (dev build path).
func TestFleetSigningKey_EmptyBytes(t *testing.T) {
	// The package var is empty by default in tests.
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = ""
	defer func() { fleetSigningPublicKeyBytes = saved }()

	if k := FleetSigningKey(); k != nil {
		t.Errorf("expected nil key when bytes empty, got %v", k)
	}
}

// TestFleetSigningKey_InvalidHex verifies FleetSigningKey returns nil on
// malformed hex input.
func TestFleetSigningKey_InvalidHex(t *testing.T) {
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = "not-valid-hex!!"
	defer func() { fleetSigningPublicKeyBytes = saved }()

	if k := FleetSigningKey(); k != nil {
		t.Errorf("expected nil key on bad hex, got %v", k)
	}
}

// TestFleetSigningKey_ValidHex verifies that a proper 64-char hex string
// decodes to a non-nil ed25519.PublicKey.
func TestFleetSigningKey_ValidHex(t *testing.T) {
	pub, _ := newTestKeyPair(t)
	// Encode pub as hex.
	hexStr := ""
	for _, b := range []byte(pub) {
		hexStr += string([]byte{hexEncNibble(b >> 4), hexEncNibble(b & 0x0f)})
	}
	saved := fleetSigningPublicKeyBytes
	fleetSigningPublicKeyBytes = hexStr
	defer func() { fleetSigningPublicKeyBytes = saved }()

	got := FleetSigningKey()
	if got == nil {
		t.Fatal("expected non-nil key")
	}
	if !got.Equal(pub) {
		t.Errorf("decoded key != expected public key")
	}
}

func hexEncNibble(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

// ── Accept-set (key rotation) tests ─────────────────────────────────────────

// TestVerifyWithKeySet_EmptySet verifies that an empty accept-set always rejects.
func TestVerifyWithKeySet_EmptySet(t *testing.T) {
	_, priv := newTestKeyPair(t)
	b := sampleBundle()
	signBundle(t, priv, b)

	err := VerifyWithKeySet(b, nil, 0)
	if !errors.Is(err, ErrSigningKeyNotConfigured) {
		t.Errorf("empty set: expected ErrSigningKeyNotConfigured, got: %v", err)
	}
}

// TestVerifyWithKeySet_ForeignKeyRejected verifies that a bundle signed by
// an unknown key is rejected even when the accept-set is non-empty.
func TestVerifyWithKeySet_ForeignKeyRejected(t *testing.T) {
	pub1, _ := newTestKeyPair(t) // the accepted key
	_, priv2 := newTestKeyPair(t) // the foreign key used to sign

	b := sampleBundle()
	signBundle(t, priv2, b) // signed by foreign key

	err := VerifyWithKeySet(b, []ed25519.PublicKey{pub1}, 0)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("foreign key: expected ErrInvalidSignature, got: %v", err)
	}
}

// TestVerifyWithKeySet_RotationOverlap verifies that during a key rotation
// window, a bundle signed by either the old or the new key is accepted.
func TestVerifyWithKeySet_RotationOverlap(t *testing.T) {
	pubOld, privOld := newTestKeyPair(t)
	pubNew, privNew := newTestKeyPair(t)

	// Accept-set contains BOTH keys (rotation overlap window).
	acceptSet := []ed25519.PublicKey{pubOld, pubNew}

	// Bundle signed by old key → accepted.
	bOld := sampleBundle()
	bOld.BundleID = 10
	signBundle(t, privOld, bOld)
	if err := VerifyWithKeySet(bOld, acceptSet, 0); err != nil {
		t.Errorf("old key during overlap: expected nil, got: %v", err)
	}

	// Bundle signed by new key → also accepted.
	bNew := sampleBundle()
	bNew.BundleID = 11
	signBundle(t, privNew, bNew)
	if err := VerifyWithKeySet(bNew, acceptSet, 10); err != nil {
		t.Errorf("new key during overlap: expected nil, got: %v", err)
	}
}

// TestVerifyWithKeySet_AfterRotation verifies that after retiring the old key,
// only the new key is accepted.
func TestVerifyWithKeySet_AfterRotation(t *testing.T) {
	_, privOld := newTestKeyPair(t)
	pubNew, privNew := newTestKeyPair(t)

	// Old key retired: accept-set contains only the new key.
	acceptSet := []ed25519.PublicKey{pubNew}

	// Bundle signed by old key → now rejected.
	bOld := sampleBundle()
	bOld.BundleID = 20
	signBundle(t, privOld, bOld)
	if err := VerifyWithKeySet(bOld, acceptSet, 0); !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("old key after retirement: expected ErrInvalidSignature, got: %v", err)
	}

	// Bundle signed by new key → still accepted.
	bNew := sampleBundle()
	bNew.BundleID = 21
	signBundle(t, privNew, bNew)
	if err := VerifyWithKeySet(bNew, acceptSet, 0); err != nil {
		t.Errorf("new key after rotation: expected nil, got: %v", err)
	}
}

// TestConfigDistributionEnabled verifies the helper gates on key presence.
func TestConfigDistributionEnabled(t *testing.T) {
	saved := fleetSigningPublicKeyBytes
	defer func() { fleetSigningPublicKeyBytes = saved }()

	fleetSigningPublicKeyBytes = ""
	if ConfigDistributionEnabled() {
		t.Error("expected false when no key bytes set")
	}

	// Set a valid key.
	pub, _ := newTestKeyPair(t)
	hexStr := ""
	for _, b := range []byte(pub) {
		hexStr += string([]byte{hexEncNibble(b >> 4), hexEncNibble(b & 0x0f)})
	}
	fleetSigningPublicKeyBytes = hexStr
	if !ConfigDistributionEnabled() {
		t.Error("expected true when valid key bytes set")
	}
}
