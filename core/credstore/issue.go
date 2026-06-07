package credstore

import (
	"context"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Issue registers ref in the store and returns a single-use Handle.
//
// Defaults:
//   - TTL = 60s (overridable via WithTTL).
//
// Errors:
//   - ErrEmptyCredential when ref.Locator is empty.
//   - ErrStoreClosed when the store has been closed.
func (s *store) Issue(_ context.Context, ref secrets.CredentialReference, purpose AccessPurpose, opts ...IssueOption) (Handle, error) {
	if ref.Locator == "" {
		return "", ErrEmptyCredential
	}

	cfg := issueConfig{ttl: defaultTTL}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.ttl <= 0 {
		cfg.ttl = defaultTTL
	}

	h, err := mintHandle()
	if err != nil {
		return "", err
	}

	e := &entry{
		ref:       ref,
		purpose:   purpose,
		expiresAt: s.clock().Add(cfg.ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Detect closed store after acquiring the write lock so we don't
	// race with Close.
	select {
	case <-s.done:
		return "", ErrStoreClosed
	default:
	}

	s.entries[h] = e
	return h, nil
}

// issue TTL helper — exported for tests.
func defaultHandleTTL() time.Duration { return defaultTTL }
