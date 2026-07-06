// Package rpc — keychain-op helpers (silent-failure-elimination WP04 / FR-004).
//
// keychainSet and keychainDelete wrap the zalando/go-keyring primitives with
// WARN-level logging on failure so callers can propagate or flag errors instead
// of bare `_ = err` swallows.  All callers in api.go and fleet/ adopt these
// helpers so a keychain-unavailability event is immediately diagnosable in logs.
package rpc

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/zalando/go-keyring"
)

// keychainSet stores a plaintext value in the OS keychain under service+key.
//
// On failure it WARN-logs with the service, key locator, and the underlying
// error, then returns the wrapped error to the caller.  The caller decides
// whether to propagate the error (hard-fail) or continue with degraded
// persistence (in-memory only); see keychainWriter.Write for the latter case.
//
// The key parameter must be a safe loggable label (e.g. "api-key/anthropic").
// It MUST NOT contain the key material itself.
func keychainSet(ctx context.Context, service, key, value string) error {
	if err := keyring.Set(service, key, value); err != nil {
		slog.WarnContext(ctx, "keychain set failed",
			"service", service,
			"key",     key,
			"error",   err.Error(),
		)
		return fmt.Errorf("keychain set(%q, %q): %w", service, key, err)
	}
	return nil
}

// keychainDelete removes a value from the OS keychain under service+key.
//
// ErrNotFound is treated as success — the caller's goal (the entry is gone)
// is already satisfied.  Other errors are WARN-logged and returned.
func keychainDelete(ctx context.Context, service, key string) error {
	if err := keyring.Delete(service, key); err != nil {
		if isKeyringNotFound(err) {
			// Entry was already absent — that's the desired post-state.
			return nil
		}
		slog.WarnContext(ctx, "keychain delete failed",
			"service", service,
			"key",     key,
			"error",   err.Error(),
		)
		return fmt.Errorf("keychain delete(%q, %q): %w", service, key, err)
	}
	return nil
}

// isKeyringNotFound returns true when err is the sentinel "not found" error
// that keyring backends return for missing entries.  go-keyring uses the
// ErrNotFound sentinel; we also guard against nil.
func isKeyringNotFound(err error) bool {
	if err == nil {
		return false
	}
	return err == keyring.ErrNotFound
}
