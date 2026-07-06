package fleet

import (
	"bytes"
	"testing"
)

// ── DeriveKey ─────────────────────────────────────────────────────────────────

func TestDeriveKey_Deterministic(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	k1, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	k2, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		t.Fatalf("DeriveKey second call: %v", err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("DeriveKey not deterministic for same seed+label")
	}
}

func TestDeriveKey_DifferentLabels(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	kSession, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		t.Fatalf("DeriveKey(session): %v", err)
	}
	kProject, err := DeriveKey(seed, LabelProjectEvents)
	if err != nil {
		t.Fatalf("DeriveKey(project): %v", err)
	}
	if bytes.Equal(kSession, kProject) {
		t.Fatal("different labels must produce different keys")
	}
}

func TestDeriveKey_BadSeed(t *testing.T) {
	_, err := DeriveKey([]byte{1, 2, 3}, LabelSessionEvents)
	if err == nil {
		t.Fatal("expected error for seed != 32 bytes")
	}
}

// ── Encrypt / Decrypt round-trip ──────────────────────────────────────────────

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}

	plaintext := []byte("the quick brown fox")
	ct, nonce, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if len(ct) == 0 {
		t.Fatal("ciphertext must be non-empty")
	}
	if len(nonce) == 0 {
		t.Fatal("nonce must be non-empty")
	}

	got, err := Decrypt(key, ct, nonce)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("plaintext mismatch: got %q want %q", got, plaintext)
	}
}

func TestDecrypt_TamperedCiphertext(t *testing.T) {
	seed := make([]byte, seedSize)
	key, _ := DeriveKey(seed, LabelSessionEvents)
	plaintext := []byte("sensitive context")
	ct, nonce, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// Flip a byte in the ciphertext.
	ct[0] ^= 0xFF

	_, err = Decrypt(key, ct, nonce)
	if err != ErrDecryptionFailed {
		t.Fatalf("expected ErrDecryptionFailed, got %v", err)
	}
}

func TestDecrypt_WrongKey(t *testing.T) {
	seed1 := make([]byte, seedSize)
	seed1[0] = 1
	seed2 := make([]byte, seedSize)
	seed2[0] = 2

	key1, _ := DeriveKey(seed1, LabelSessionEvents)
	key2, _ := DeriveKey(seed2, LabelSessionEvents)

	ct, nonce, err := Encrypt(key1, []byte("secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	_, err = Decrypt(key2, ct, nonce)
	if err != ErrDecryptionFailed {
		t.Fatalf("expected ErrDecryptionFailed for wrong key, got %v", err)
	}
}

// ── Recovery code ─────────────────────────────────────────────────────────────

func TestRecoveryCode_RoundTrip(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 5)
	}

	code, err := MintRecoveryCode(seed)
	if err != nil {
		t.Fatalf("MintRecoveryCode: %v", err)
	}
	if len(code) == 0 {
		t.Fatal("recovery code must be non-empty")
	}

	recovered, err := UseRecoveryCode(code)
	if err != nil {
		t.Fatalf("UseRecoveryCode: %v", err)
	}
	if !bytes.Equal(recovered, seed) {
		t.Fatalf("recovered seed mismatch")
	}
}

func TestUseRecoveryCode_InvalidPrefix(t *testing.T) {
	_, err := UseRecoveryCode("WRONG-abc123")
	if err != ErrBadRecoveryCode {
		t.Fatalf("expected ErrBadRecoveryCode, got %v", err)
	}
}

func TestUseRecoveryCode_BadBase64(t *testing.T) {
	_, err := UseRecoveryCode("KENAZ-!!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestUseRecoveryCode_WhitespaceTolerant(t *testing.T) {
	seed := make([]byte, seedSize)
	for i := range seed {
		seed[i] = byte(i + 10)
	}
	code, err := MintRecoveryCode(seed)
	if err != nil {
		t.Fatalf("MintRecoveryCode: %v", err)
	}
	// Wrap in whitespace.
	recovered, err := UseRecoveryCode("  " + code + "  ")
	if err != nil {
		t.Fatalf("UseRecoveryCode with whitespace: %v", err)
	}
	if !bytes.Equal(recovered, seed) {
		t.Fatal("whitespace-tolerant recovery mismatch")
	}
}
