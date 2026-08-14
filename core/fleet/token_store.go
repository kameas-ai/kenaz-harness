package fleet

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// keyringService is the SHARED, product-neutral OS-keychain service under which
// fleet tokens live, so one sign-in spans every Kenaz product (harness,
// workbench, …). The per-env account namespace (fleet:<env>:*) still isolates
// dev/stage/prod. See core/paths.FleetKeychainService for the rationale.
var keyringService = paths.FleetKeychainService()

// The fleet token accounts are namespaced per environment (fleet:<env>:*) so a
// self-signed dev build (KENAZ_HARNESS_ENV=dev) never reads or overwrites the
// prod app's items, and vice-versa.
func keyAccessToken() string  { return "fleet:" + paths.KeychainNamespace() + ":access_token" }
func keyRefreshToken() string { return "fleet:" + paths.KeychainNamespace() + ":refresh_token" }
func keyExpiresAt() string    { return "fleet:" + paths.KeychainNamespace() + ":expires_at" }

// ErrTokensNotFound is returned by LoadTokens when no tokens are in the
// keychain. Callers should treat this as "not signed in".
var ErrTokensNotFound = errors.New("fleet: tokens not found in keychain")

// externalTokenSource, when installed, replaces the OS keychain as the
// source of the current access token. The served (in-VM) harness installs
// the host auth-broker session here: the headless guest has no Secret
// Service for go-keyring to talk to, and token renewal is owned host-side
// by the broker — the refresh token never crosses the host→VM boundary.
//
// The source returns the current access token, or "" when the host is
// signed out. LoadTokens wraps it in an access-only TokenSet (no refresh
// token, zero expiry — expiry pacing is the broker's renewal loop, not
// ours), which the client treats as a live session whose 401s mean
// "re-read the source", never "run the OAuth refresh flow" (see the
// externalTokens guards in http.go / identity.go). SaveTokens and
// ClearTokens become no-ops while a source is installed.
var (
	externalTokenMu     sync.RWMutex
	externalTokenSource func() string
)

// SetExternalTokenSource installs fn as the process-wide access-token
// source, displacing the OS keychain. Pass nil to restore keychain-backed
// behavior. Called once at served startup, before any fleet call; the
// source itself must be safe for concurrent use.
func SetExternalTokenSource(fn func() string) {
	externalTokenMu.Lock()
	defer externalTokenMu.Unlock()
	externalTokenSource = fn
}

// externalTokens returns the installed source, if any.
func externalTokens() (func() string, bool) {
	externalTokenMu.RLock()
	defer externalTokenMu.RUnlock()
	return externalTokenSource, externalTokenSource != nil
}

// SaveTokens persists a TokenSet to the OS keychain. The access token,
// refresh token, and expiry are stored separately so they can be
// individually rotated.
func SaveTokens(ts TokenSet) error {
	if _, ok := externalTokens(); ok {
		// Renewal is externally owned and there is no keychain where an
		// external source runs; nothing to persist.
		return nil
	}
	if err := keyring.Set(keyringService, keyAccessToken(), ts.AccessToken); err != nil {
		return fmt.Errorf("fleet: save access token: %w", err)
	}
	if err := keyring.Set(keyringService, keyRefreshToken(), ts.RefreshToken); err != nil {
		return fmt.Errorf("fleet: save refresh token: %w", err)
	}
	expStr := strconv.FormatInt(ts.ExpiresAt.Unix(), 10)
	if err := keyring.Set(keyringService, keyExpiresAt(), expStr); err != nil {
		return fmt.Errorf("fleet: save expires_at: %w", err)
	}
	return nil
}

// LoadTokens reads the TokenSet from the OS keychain. Returns
// ErrTokensNotFound when no tokens have been stored.
func LoadTokens() (TokenSet, error) {
	if src, ok := externalTokens(); ok {
		tok := src()
		if tok == "" {
			return TokenSet{}, ErrTokensNotFound
		}
		return TokenSet{AccessToken: tok}, nil
	}
	accessToken, err := keyring.Get(keyringService, keyAccessToken())
	if err != nil {
		return TokenSet{}, ErrTokensNotFound
	}
	if accessToken == "" {
		return TokenSet{}, ErrTokensNotFound
	}
	refreshToken, _ := keyring.Get(keyringService, keyRefreshToken()) // missing is tolerated
	expiresAtStr, _ := keyring.Get(keyringService, keyExpiresAt())

	var expiresAt time.Time
	if expiresAtStr != "" {
		if unix, err := strconv.ParseInt(expiresAtStr, 10, 64); err == nil {
			expiresAt = time.Unix(unix, 0)
		}
	}
	return TokenSet{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

// ClearTokens deletes all fleet tokens from the OS keychain.
// All three slots are attempted regardless of individual errors so a partial
// keychain state is avoided. Individual delete failures are WARN-logged (not
// silently swallowed, FR-004) so a failing keychain clear is diagnosable, and
// the returned error aggregates every failure (fleet-integrity WP06), excluding
// "not found" — missing tokens are not an error on sign-out. Callers should
// surface the error but may still treat sign-out as logically complete (the
// access token is the gate; a token that failed to delete is unusable anyway
// because Sign-In refuses to load an incomplete set).
func ClearTokens() error {
	if _, ok := externalTokens(); ok {
		// Nothing persisted locally; sign-out is the host's transition.
		return nil
	}
	var errs []string
	for _, key := range []string{keyAccessToken(), keyRefreshToken(), keyExpiresAt()} {
		if err := keyring.Delete(keyringService, key); err != nil {
			if err == keyring.ErrNotFound {
				// Already absent — that's the desired post-state.
				continue
			}
			slog.Warn("fleet: ClearTokens keychain delete failed",
				"key", key,
				"error", err.Error(),
			)
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("fleet: clear tokens: %s", strings.Join(errs, "; "))
}
