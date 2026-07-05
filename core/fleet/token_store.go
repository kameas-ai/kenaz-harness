package fleet

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
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

// SaveTokens persists a TokenSet to the OS keychain. The access token,
// refresh token, and expiry are stored separately so they can be
// individually rotated.
func SaveTokens(ts TokenSet) error {
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
//
// (FR-004) Individual delete failures are now WARN-logged (not silently
// swallowed) so a failing keychain clear is diagnosable.  Partial deletion
// is still treated as a best-effort success — the caller's sign-out goal
// is satisfied even when one token key was already absent or the keychain
// backend returns a transient error, provided the access token is cleared.
// A non-nil error is returned only when the access-token delete fails
// (because a stale access token is the most likely cause of "zombie" login).
//
// NOTE: the fleet-integrity mission's WP06 also touches this function.
// Keep this change self-contained (logging + structured error return) so the
// two changes compose cleanly when merged.
func ClearTokens() error {
	var firstErr error
	for _, key := range []string{keyAccessToken(), keyRefreshToken(), keyExpiresAt()} {
		if err := keyring.Delete(keyringService, key); err != nil {
			if err == keyring.ErrNotFound {
				// Already absent — that's the desired post-state.
				continue
			}
			slog.Warn("fleet: ClearTokens keychain delete failed",
				"key",   key,
				"error", err.Error(),
			)
			if firstErr == nil && key == keyAccessToken() {
				firstErr = fmt.Errorf("fleet: clear access token: %w", err)
			}
		}
	}
	return firstErr
}
