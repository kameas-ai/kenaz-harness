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

	// ErrCredentialAccessDenied is returned by Use when the Cedar
	// policy gate denies the credential access — either an explicit
	// Deny outcome or a NotApplicable outcome under strict mode (for
	// non-mcp_spawn purposes). Mission cedar-credential-policy WP05.
	ErrCredentialAccessDenied = errors.New("credstore: credential access denied by policy")

	// ErrMCPSpawnPolicyPending is a forward-marker for the
	// IssueForMCPSpawn interactive-prompt path landing in credstore
	// WP06. WP05 documents the integration point but does not return
	// this error today; the mcp_spawn Cedar gate fires best-effort
	// (Deny → ErrCredentialAccessDenied; NotApplicable → allow).
	ErrMCPSpawnPolicyPending = errors.New("credstore: mcp_spawn cedar gate pending — wired in credstore WP06")
)
