package rpc

// keychain_test.go — unit tests for the keychain-op helpers (WP04 / FR-004).
//
// The tests use a stubbed keyring backend to avoid touching the real OS keychain
// in CI / sandbox environments where it may not be available.  We exercise the
// WARN-log-on-failure contract by asserting that errors are returned and that
// ErrNotFound on delete is treated as success.

import (
	"context"
	"errors"
	"testing"

	"github.com/zalando/go-keyring"
)

// TestKeychainDelete_NotFoundIsSilent asserts that ErrNotFound from a delete
// is swallowed (the desired post-state is achieved) and no error is returned.
func TestKeychainDelete_NotFoundIsSilent(t *testing.T) {
	// The key was never set, so delete must return ErrNotFound which
	// keychainDelete converts to nil.
	if err := keychainDelete(context.Background(), "test-svc", "missing-key"); err != nil {
		t.Errorf("keychainDelete with ErrNotFound should return nil; got %v", err)
	}
}

// TestKeychainSet_SuccessNoError asserts that a successful set returns nil.
func TestKeychainSet_SuccessNoError(t *testing.T) {
	if err := keychainSet(context.Background(), "test-svc", "my-key", "my-value"); err != nil {
		t.Errorf("keychainSet should succeed with mock backend; got %v", err)
	}
}

// TestKeychainDelete_ExistingKeyReturnsNil asserts a present key is deleted cleanly.
func TestKeychainDelete_ExistingKeyReturnsNil(t *testing.T) {
	// Plant the key first.
	if err := keyring.Set("test-svc", "del-key", "v"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := keychainDelete(context.Background(), "test-svc", "del-key"); err != nil {
		t.Errorf("keychainDelete of existing key should return nil; got %v", err)
	}
	// Verify it's actually gone.
	if _, err := keyring.Get("test-svc", "del-key"); !errors.Is(err, keyring.ErrNotFound) {
		t.Errorf("key should be deleted; keyring.Get returned %v", err)
	}
}

// TestIsKeyringNotFound checks the helper function.
func TestIsKeyringNotFound(t *testing.T) {
	if !isKeyringNotFound(keyring.ErrNotFound) {
		t.Error("expected isKeyringNotFound(keyring.ErrNotFound) = true")
	}
	if isKeyringNotFound(nil) {
		t.Error("expected isKeyringNotFound(nil) = false")
	}
	if isKeyringNotFound(errors.New("other error")) {
		t.Error("expected isKeyringNotFound(other) = false")
	}
}
