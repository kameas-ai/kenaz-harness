package fleet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// installExternalSource installs fn as the external token source and
// guarantees the package-level hook is cleared when the test ends, so the
// keychain-backed tests in this package are unaffected by ordering.
func installExternalSource(t *testing.T, fn func() string) {
	t.Helper()
	SetExternalTokenSource(fn)
	t.Cleanup(func() { SetExternalTokenSource(nil) })
}

// TestExternalTokenSource_LoadTokens: with a source installed, LoadTokens
// returns an access-only TokenSet (no refresh token, zero expiry — renewal
// is the broker's job), and an empty source token reads as signed-out.
func TestExternalTokenSource_LoadTokens(t *testing.T) {
	installExternalSource(t, func() string { return "brokered-token" })

	ts, err := LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if ts.AccessToken != "brokered-token" {
		t.Errorf("AccessToken = %q, want brokered-token", ts.AccessToken)
	}
	if ts.RefreshToken != "" {
		t.Error("external TokenSet must never carry a refresh token")
	}
	if !ts.ExpiresAt.IsZero() {
		t.Error("external TokenSet must have zero expiry (broker paces renewal)")
	}

	SetExternalTokenSource(func() string { return "" })
	if _, err := LoadTokens(); !errors.Is(err, ErrTokensNotFound) {
		t.Errorf("empty source token: err = %v, want ErrTokensNotFound", err)
	}
}

// TestExternalTokenSource_SaveAndClearAreNoOps: the persistence surface is
// inert while a source is installed — nothing may touch the (absent)
// keychain, and removing the source restores keychain behavior.
func TestExternalTokenSource_SaveAndClearAreNoOps(t *testing.T) {
	// Baseline: keychain (mocked in main_test.go) round-trips.
	if err := SaveTokens(TokenSet{AccessToken: "keychain-token", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed keychain: %v", err)
	}
	t.Cleanup(func() { _ = ClearTokens() })

	installExternalSource(t, func() string { return "brokered-token" })

	if err := SaveTokens(TokenSet{AccessToken: "must-not-land"}); err != nil {
		t.Fatalf("SaveTokens with source installed: %v", err)
	}
	if err := ClearTokens(); err != nil {
		t.Fatalf("ClearTokens with source installed: %v", err)
	}

	// Uninstall → the keychain state is exactly what the baseline wrote:
	// the no-op Save didn't overwrite it, the no-op Clear didn't delete it.
	SetExternalTokenSource(nil)
	ts, err := LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens after uninstall: %v", err)
	}
	if ts.AccessToken != "keychain-token" {
		t.Errorf("keychain token = %q, want keychain-token (external no-ops leaked through)", ts.AccessToken)
	}
}

// TestExternalTokenSource_BearerProvider: DefaultBearerProvider reads
// through the source and observes rotation without re-construction — the
// OTLP transport re-invokes the provider on every flush.
func TestExternalTokenSource_BearerProvider(t *testing.T) {
	var mu sync.Mutex
	tok := "first"
	installExternalSource(t, func() string { mu.Lock(); defer mu.Unlock(); return tok })

	bearer := DefaultBearerProvider()
	if got, _ := bearer(); got != "first" {
		t.Errorf("bearer = %q, want first", got)
	}
	mu.Lock()
	tok = "rotated"
	mu.Unlock()
	if got, _ := bearer(); got != "rotated" {
		t.Errorf("bearer after rotation = %q, want rotated", got)
	}
}

// TestExternalTokenSource_Do401_RetriesWithRotatedToken: a 401 with a source
// installed re-reads the source once (the broker may have renewed mid-flight)
// and retries — it must NOT attempt the OAuth refresh flow, which would need
// the refresh token that never crosses the host→VM boundary.
func TestExternalTokenSource_Do401_RetriesWithRotatedToken(t *testing.T) {
	var mu sync.Mutex
	tok := "stale"
	installExternalSource(t, func() string { mu.Lock(); defer mu.Unlock(); return tok })

	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seen = append(seen, auth)
		if auth != "Bearer renewed" {
			// Simulate the broker renewing while the 401 is in flight.
			mu.Lock()
			tok = "renewed"
			mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.do(context.Background(), http.MethodGet, "/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(seen) != 2 || seen[0] != "Bearer stale" || seen[1] != "Bearer renewed" {
		t.Errorf("auth headers seen = %v, want [Bearer stale, Bearer renewed]", seen)
	}
}

// TestExternalTokenSource_Do401_UnchangedTokenIsSessionDeath: when the
// server rejects a token the broker still considers current, there is
// nothing to retry with — the session is dead host-side.
func TestExternalTokenSource_Do401_UnchangedTokenIsSessionDeath(t *testing.T) {
	installExternalSource(t, func() string { return "rejected" })

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.do(context.Background(), http.MethodGet, "/api/v1/ping", nil)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
	if calls != 1 {
		t.Errorf("server calls = %d, want 1 (no blind retry with the same rejected token)", calls)
	}
}
