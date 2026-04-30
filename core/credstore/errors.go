// Package credstore implements a short-lived credential-handle store.
// Handles are issued by Issue, consumed exactly once by Use, and
// displayed (without resolution) by Peek. All handles carry a TTL;
// expired handles are rejected and periodically reaped.
//
// Spec mapping: credential-store-01KQ8TDD.
package credstore

import "errors"

// Typed error sentinels returned by Store operations.
var (
	// ErrEmptyCredential is returned by Issue and Peek when the
	// CredentialReference has a zero/empty locator.
	ErrEmptyCredential = errors.New("credstore: empty credential reference")

	// ErrUnknownHandle is returned by Use when the Handle does not
	// exist in the store (never issued, or already reaped).
	ErrUnknownHandle = errors.New("credstore: unknown handle")

	// ErrHandleExpired is returned by Use when the entry's expiresAt
	// has passed.
	ErrHandleExpired = errors.New("credstore: handle expired")

	// ErrUseAfterFree is returned by Use when the handle has already
	// been consumed. Handles are single-use by contract.
	ErrUseAfterFree = errors.New("credstore: handle already used (single-use contract)")

	// ErrStoreClosed is returned when any operation is attempted on a
	// closed store.
	ErrStoreClosed = errors.New("credstore: store closed")
)
