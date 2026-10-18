package fleet

// keyring_seam_test.go — keyring-seam-01 (2026-09-15) proof.
//
// This is the class the seam exists to close: core/fleet/token_store.go
// (SaveTokens/LoadTokens/ClearTokens) is the single most common way
// production code reaches the OS keychain — every fleet HTTP request
// touches it via Client.do -> LoadTokens. Before core/keyring existed,
// core/fleet/main_test.go carried its own `TestMain{ keyring.MockInit() }`
// (one of the seven per-package patches this mission ended); this test
// proves the seam covers exactly what that TestMain used to cover, without
// core/fleet needing to know anything about mocking keyrings at all.
//
// Per the mission's proof requirement: assert the backend SELECTION
// (keyring.ActiveBackend()), never perform a live probe of a
// wrongly-selected "os" backend — that can hang under a sandboxed HOME
// (see core/keyring's package doc). SaveTokens/LoadTokens/ClearTokens here
// are exercised through their real, production code path; the only thing
// this test does differently from production is check what backend they
// landed on.

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
)

// TestFleetTokenStore_RoutesThroughMockUnderTest constructs the fleet
// token store exactly the way production does — SaveTokens, LoadTokens,
// ClearTokens, no test-only shortcuts — and proves the real OS keychain
// is never reached: the seam it authenticates through is in mock mode for
// this entire test binary.
func TestFleetTokenStore_RoutesThroughMockUnderTest(t *testing.T) {
	if got := keyring.ActiveBackend(); got != "mock" {
		t.Fatalf("keyring.ActiveBackend() = %q, want %q — this test would otherwise "+
			"read/write the real OS keychain via fleet.SaveTokens/LoadTokens below", got, "mock")
	}

	ts := TokenSet{
		AccessToken:  "seam-proof-access-token",
		RefreshToken: "seam-proof-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).Truncate(time.Second),
	}

	if err := SaveTokens(ts); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	got, err := LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if got.AccessToken != ts.AccessToken {
		t.Errorf("LoadTokens().AccessToken = %q, want %q", got.AccessToken, ts.AccessToken)
	}
	if got.RefreshToken != ts.RefreshToken {
		t.Errorf("LoadTokens().RefreshToken = %q, want %q", got.RefreshToken, ts.RefreshToken)
	}
	if !got.ExpiresAt.Equal(ts.ExpiresAt) {
		t.Errorf("LoadTokens().ExpiresAt = %v, want %v", got.ExpiresAt, ts.ExpiresAt)
	}

	if err := ClearTokens(); err != nil {
		t.Fatalf("ClearTokens: %v", err)
	}
	if _, err := LoadTokens(); err != ErrTokensNotFound {
		t.Errorf("LoadTokens after ClearTokens: got err=%v, want ErrTokensNotFound", err)
	}
}
