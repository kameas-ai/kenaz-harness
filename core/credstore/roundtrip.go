package credstore

// RoundTrip — credential-store-01KQ8TDD WP04.
//
// RoundTrip is a convenience wrapper that issues a one-shot 60-second handle
// and immediately calls Use(op) in one step. Callers that already have a
// Handle from Issue should call Use directly; RoundTrip is for call sites that
// want an atomic issue-and-use without managing the Handle lifecycle.
//
// The audit event emitted by Use carries purpose verbatim, so the audit trail
// distinguishes provider_call from provider_test etc. even though the handle
// is internal and invisible to the caller.

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// RoundTrip issues a single-use handle for ref/purpose and immediately calls
// Use(op). The raw bytes are visible only inside op and are zeroed when Use
// returns (even if op panics).
//
// Errors:
//   - ErrEmptyCredential — ref.Locator is empty.
//   - ErrStoreClosed — store has been closed.
//   - Resolver errors (secret not found, keychain locked, etc.).
//   - op's own error, returned verbatim.
func (s *store) RoundTrip(ctx context.Context, ref secrets.CredentialReference, purpose AccessPurpose, op func([]byte) error) error {
	h, err := s.Issue(ctx, ref, purpose)
	if err != nil {
		return err
	}
	return s.Use(ctx, h, op)
}
