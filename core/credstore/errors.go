package credstore

import "errors"

// ErrUnknownHandle is returned by Use when the supplied Handle is not
// present in the store's handle table. This occurs when the handle was
// never issued by this store instance or when the store has been closed.
var ErrUnknownHandle = errors.New("credstore: unknown handle")

// ErrHandleExpired is returned by Use when the handle exists in the
// table but its expiresAt timestamp has passed. Single-use handles that
// have already been consumed also return this error.
var ErrHandleExpired = errors.New("credstore: handle expired")

// ErrUseAfterFree is returned when a one-shot handle is used more than
// once. The first Use removes the handle from the table; subsequent
// calls see ErrUseAfterFree rather than ErrUnknownHandle so callers can
// distinguish "never existed" from "already consumed".
var ErrUseAfterFree = errors.New("credstore: handle already consumed (use-after-free)")

// ErrEmptyCredential is returned when the underlying secrets.Resolver
// returns an empty or zero-length byte slice. This typically indicates a
// configuration error — the credential reference points to a valid
// backend location but the stored value is empty.
var ErrEmptyCredential = errors.New("credstore: resolved credential is empty")

// errNotImplemented is the internal sentinel returned by every method
// body that WP01 stubs out. WP02 replaces these with real
// implementations; the typed exported errors above are what callers
// should match in production code.
var errNotImplemented = errors.New("credstore: WP02: not yet implemented")
