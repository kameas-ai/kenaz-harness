package fleet

// token_state_pi_test.go — fleet-session-truth-01DOGF0A WP-PI.
//
// Persistence surface: the token store (three keychain slots written by
// SaveTokens since before this mission). ReadTokenState is new; it must read
// slots exactly as a previous release wrote them. Driven through the real
// core/keyring seam, which selects go-keyring's in-memory mock under
// `go test` — asserted first, so this test can never reach the macOS
// keychain (no Keychain dialog is possible).

import (
	"strconv"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/keyring"
)

func writeLegacyTokenSlots(t *testing.T, access, refresh string, expires time.Time) {
	t.Helper()
	// Exactly the v0.85.2 SaveTokens layout: three separate slots, expiry as
	// unix seconds.
	if err := keyring.Set(keyringService, keyAccessToken(), access); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(keyringService, keyRefreshToken(), refresh); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Set(keyringService, keyExpiresAt(), strconv.FormatInt(expires.Unix(), 10)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ClearTokens() })
}

func TestPI_ReadTokenState_ReadsPreviousReleaseSlots(t *testing.T) {
	if b := keyring.ActiveBackend(); b != "mock" {
		t.Fatalf("keyring backend = %q under go test; refusing to touch a real keychain", b)
	}
	SetExternalTokenSource(nil)

	cases := []struct {
		name                string
		refresh             string
		expires             time.Time
		accessValid, usable bool
	}{
		{"valid access", "rt", time.Now().Add(time.Hour), true, true},
		{"expired access, refresh present", "rt", time.Now().Add(-time.Hour), false, true},
		{"expired access, no refresh", "", time.Now().Add(-time.Hour), false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeLegacyTokenSlots(t, "header.eyJzdWIiOiJzdWItYSJ9.sig", c.refresh, c.expires)
			st := ReadTokenState()
			if !st.Present || st.AccessValid != c.accessValid || st.Usable() != c.usable {
				t.Fatalf("state = %+v (usable=%v), want accessValid=%v usable=%v", st, st.Usable(), c.accessValid, c.usable)
			}
			if st.Claims.Subject != "sub-a" {
				t.Fatalf("claims.subject = %q, want sub-a decoded from the stored token", st.Claims.Subject)
			}
		})
	}

	// Cleared store (sign-out) reads as not present — never as a session.
	_ = ClearTokens()
	if st := ReadTokenState(); st.Present || st.Usable() {
		t.Fatalf("cleared store reads %+v, want not present", st)
	}
}
