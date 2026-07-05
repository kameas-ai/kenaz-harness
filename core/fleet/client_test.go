package fleet

import (
	"context"
	"testing"
	"time"
)

// TestSignedIn_NoTokens verifies that SignedIn returns false when no tokens
// are present in the keychain.
func TestSignedIn_NoTokens(t *testing.T) {
	c := &Client{}
	// Ensure the keyring mock is empty for this env namespace.
	_ = ClearTokens()

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false when no tokens in keychain")
	}
}

// TestSignedIn_ValidToken verifies that SignedIn returns true when a valid
// (non-expired) token is present.
func TestSignedIn_ValidToken(t *testing.T) {
	c := &Client{}
	ts := TokenSet{
		AccessToken:  "valid-token",
		RefreshToken: "valid-refresh",
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true when token is valid and not expired")
	}
}

// TestSignedIn_ExpiredAccessNoRefresh verifies that SignedIn returns false
// when the access token is expired and there is no refresh token (FR-004).
func TestSignedIn_ExpiredAccessNoRefresh(t *testing.T) {
	c := &Client{}
	ts := TokenSet{
		AccessToken:  "expired-token",
		RefreshToken: "", // no refresh token
		ExpiresAt:    time.Now().Add(-5 * time.Minute), // already expired
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false when access token is expired and no refresh token")
	}
}

// TestSignedIn_ExpiredAccessWithRefresh verifies that SignedIn returns false
// when the access token is expired, even with a refresh token present.
// The caller is expected to surface a re-auth prompt (not retry silently).
// The actual refresh happens in do() when the next fleet request is made.
func TestSignedIn_ExpiredAccessWithRefresh(t *testing.T) {
	c := &Client{}
	ts := TokenSet{
		AccessToken:  "expired-token",
		RefreshToken: "still-valid-refresh",
		ExpiresAt:    time.Now().Add(-5 * time.Minute), // already expired
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false when access token is expired (even with refresh token present)")
	}
}

// TestSignedIn_NoExpiry verifies that SignedIn returns true when no expiry
// is recorded (legacy tokens written before ExpiresAt tracking was added).
func TestSignedIn_NoExpiry(t *testing.T) {
	c := &Client{}
	ts := TokenSet{
		AccessToken:  "legacy-token",
		RefreshToken: "refresh",
		ExpiresAt:    time.Time{}, // zero = no expiry info
	}
	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Error("expected true when no expiry is recorded (legacy token path)")
	}
}

// TestSignedIn_NopClient verifies that a nop (fleet-disabled) client always
// returns false.
func TestSignedIn_NopClient(t *testing.T) {
	c := nopClientInstance()

	ok, err := c.SignedIn(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected false for nop client")
	}
}
