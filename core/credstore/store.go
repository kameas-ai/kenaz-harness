package credstore

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// defaultTTL is the handle lifetime when no WithTTL option is given.
const defaultTTL = 60 * time.Second

// reapInterval is how often the expiration goroutine wakes to sweep
// expired entries. Per spec: entries are deleted within 30s of
// expiresAt, so 30s interval is the upper bound.
const reapInterval = 30 * time.Second

// AccessPurpose is a caller-supplied label describing why the
// credential is being accessed. It appears in audit events.
type AccessPurpose string

// entry is the internal record created by Issue. It is private to
// this package.
type entry struct {
	ref       secrets.CredentialReference
	purpose   AccessPurpose
	expiresAt time.Time
	used      atomic.Bool
}

// store implements Store. All exported methods are safe for concurrent
// use.
type store struct {
	mu        sync.RWMutex
	entries   map[Handle]*entry
	resolver  secrets.ResolverAPI
	clock     func() time.Time
	done      chan struct{}
	closeOnce sync.Once
}

// Store is the public interface. Callers obtain a *store via New.
type Store interface {
	// Issue registers a new credential reference and returns a handle.
	Issue(ctx context.Context, ref secrets.CredentialReference, purpose AccessPurpose, opts ...IssueOption) (Handle, error)
	// Use resolves the credential behind h and passes the raw bytes to
	// op. The bytes are zeroed on return even if op panics.
	Use(ctx context.Context, h Handle, op func([]byte) error) error
	// Peek resolves the credential behind ref and returns a redacted
	// display value. No audit event is emitted.
	Peek(ctx context.Context, ref secrets.CredentialReference) (Redacted, error)
	// Close stops the expiration goroutine and zeroes all entries.
	Close()
}

// IssueOption is a functional option for Issue.
type IssueOption func(*issueConfig)

type issueConfig struct {
	ttl time.Duration
}

// WithTTL overrides the default handle TTL.
func WithTTL(d time.Duration) IssueOption {
	return func(c *issueConfig) { c.ttl = d }
}

// New constructs a Store. resolver is used by Use to fetch raw bytes.
// clock defaults to time.Now when nil (tests inject a fake clock).
func New(resolver secrets.ResolverAPI, clock func() time.Time) Store {
	if clock == nil {
		clock = time.Now
	}
	s := &store{
		entries:  make(map[Handle]*entry),
		resolver: resolver,
		clock:    clock,
		done:     make(chan struct{}),
	}
	go s.reapLoop()
	return s
}

// reapLoop runs in the background and deletes expired entries roughly
// every reapInterval. It NEVER holds the entries lock while doing
// anything external — it snapshots expired handles under the lock,
// releases, then deletes in a second pass.
func (s *store) reapLoop() {
	ticker := time.NewTicker(reapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.reapOnce()
		}
	}
}

func (s *store) reapOnce() {
	now := s.clock()

	// Phase 1: collect expired handles under the read lock.
	s.mu.RLock()
	var expired []Handle
	for h, e := range s.entries {
		if now.After(e.expiresAt) {
			expired = append(expired, h)
		}
	}
	s.mu.RUnlock()

	if len(expired) == 0 {
		return
	}

	// Phase 2: delete under the write lock (no external calls here).
	s.mu.Lock()
	for _, h := range expired {
		delete(s.entries, h)
	}
	s.mu.Unlock()
}

// Close signals the expiration goroutine to stop and clears all
// entries. Idempotent.
func (s *store) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.mu.Lock()
		s.entries = make(map[Handle]*entry)
		s.mu.Unlock()
	})
}
