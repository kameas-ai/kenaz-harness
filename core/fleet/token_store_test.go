package fleet

import (
	"testing"
	"time"
)

// TestClearTokens_EmptyKeyring verifies ClearTokens succeeds (nil error) when
// the keychain is already empty — "not found" must not be treated as an error
// during sign-out (FR-007).
func TestClearTokens_EmptyKeyring(t *testing.T) {
	// Ensure the keyring is clean before the test.
	_ = ClearTokens()

	if err := ClearTokens(); err != nil {
		t.Fatalf("ClearTokens on empty keyring: expected nil error, got %v", err)
	}
}

// TestClearTokens_AllTokensRemoved verifies that after SaveTokens + ClearTokens,
// LoadTokens returns ErrTokensNotFound.
func TestClearTokens_AllTokensRemoved(t *testing.T) {
	ts := TokenSet{
		AccessToken:  "a.b.c",
		RefreshToken: "refresh-xyz",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	// Confirm tokens are readable before clear.
	if _, err := LoadTokens(); err != nil {
		t.Fatalf("LoadTokens before clear: %v", err)
	}

	if err := ClearTokens(); err != nil {
		t.Fatalf("ClearTokens: %v", err)
	}

	// After clear, LoadTokens must return ErrTokensNotFound.
	if _, err := LoadTokens(); err == nil {
		t.Error("expected ErrTokensNotFound after ClearTokens, got nil")
	}
}

// TestClearTokens_IdempotentOnPartialKeychain verifies that clearing a keychain
// with only the access token set (no refresh, no expiry) returns nil — common
// after a legacy sign-in that did not persist all three slots.
func TestClearTokens_IdempotentOnPartialKeychain(t *testing.T) {
	// Simulate a partial keychain by saving a token set with empty refresh.
	ts := TokenSet{
		AccessToken:  "partial-token",
		RefreshToken: "",
		ExpiresAt:    time.Time{},
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	// ClearTokens must not error even though some slots may be absent.
	if err := ClearTokens(); err != nil {
		t.Fatalf("ClearTokens on partial keychain: %v", err)
	}
}
