package fleet

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestNewDeviceSigner_CreatesPersistentKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	signer, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatalf("NewDeviceSigner: %v", err)
	}
	if signer.PubkeyFingerprint() == "" {
		t.Error("want non-empty fingerprint")
	}
	if got := signer.PubkeyFingerprint()[:7]; got != "sha256:" {
		t.Errorf("fingerprint prefix want sha256:, got %q", got)
	}

	// Key file must exist.
	keyPath := filepath.Join(dir, deviceKeyFile)
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("key file not created: %v", err)
	}

	// Mode must be 0600.
	fi, _ := os.Stat(keyPath)
	if fi.Mode().Perm() != 0600 {
		t.Errorf("key file mode want 0600, got %v", fi.Mode().Perm())
	}
}

func TestNewDeviceSigner_LoadsExistingKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// First call creates the key.
	s1, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatalf("first NewDeviceSigner: %v", err)
	}
	fp1 := s1.PubkeyFingerprint()

	// Second call must load the same key (same fingerprint).
	s2, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatalf("second NewDeviceSigner: %v", err)
	}
	if s2.PubkeyFingerprint() != fp1 {
		t.Errorf("fingerprint mismatch: first=%s second=%s", fp1, s2.PubkeyFingerprint())
	}
}

func TestDeviceSigner_SignRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	signer, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatalf("NewDeviceSigner: %v", err)
	}

	payload := []byte(`{"batch_id":"test","consent_level":"full"}`)
	sigBase64, fp, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if sigBase64 == "" {
		t.Fatal("empty signature")
	}
	if fp == "" {
		t.Fatal("empty fingerprint from Sign")
	}

	// Decode the signature and verify directly with the public key.
	// We need to extract the public key from the private key to verify.
	privKey := signer.privKey
	pubKey := privKey.Public().(ed25519.PublicKey)
	pubBase64 := base64.StdEncoding.EncodeToString(pubKey)

	ok, err := VerifySignature(pubBase64, payload, sigBase64)
	if err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if !ok {
		t.Error("signature verification failed")
	}
}

func TestDeviceSigner_TamperedPayloadFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	signer, err := NewDeviceSigner(dir)
	if err != nil {
		t.Fatalf("NewDeviceSigner: %v", err)
	}

	payload := []byte(`{"batch_id":"test"}`)
	sigBase64, _, _ := signer.Sign(payload)

	privKey := signer.privKey
	pubKey := privKey.Public().(ed25519.PublicKey)
	pubBase64 := base64.StdEncoding.EncodeToString(pubKey)

	// Tamper with the payload.
	tampered := []byte(`{"batch_id":"TAMPERED"}`)
	ok, err := VerifySignature(pubBase64, tampered, sigBase64)
	if err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if ok {
		t.Error("tampered payload should not verify")
	}
}

func TestNewDeviceSigner_ConcurrentCreation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Race multiple goroutines trying to create the key at the same time.
	errs := make(chan error, 10)
	fps := make(chan string, 10)
	for i := 0; i < 10; i++ {
		go func() {
			s, err := NewDeviceSigner(dir)
			if err != nil {
				errs <- err
				return
			}
			fps <- s.PubkeyFingerprint()
		}()
	}

	seen := make(map[string]bool)
	for i := 0; i < 10; i++ {
		select {
		case err := <-errs:
			t.Errorf("concurrent creation error: %v", err)
		case fp := <-fps:
			seen[fp] = true
		}
	}

	if len(seen) != 1 {
		t.Errorf("want all goroutines to get the same fingerprint, got %d distinct: %v", len(seen), seen)
	}
}
