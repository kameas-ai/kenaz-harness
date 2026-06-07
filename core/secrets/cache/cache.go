// Package cache implements the in-process TTL cache that sits between
// consumer calls and backend resolution.
//
// Design notes:
//
//   - Keys are the redaction-safe reference id from
//     ref.CredentialReference.ID. The locator never appears in the
//     map keys so a heap dump of the cache cannot leak tenant
//     identifiers.
//
//   - Values are Secret handles, NOT raw bytes. Eviction calls
//     Secret.Destroy so zeroization is automatic and consistent with
//     the WP03 contract.
//
//   - TTLs are per-BackendKind with a default of 60s (plan §9 Q1).
//
//   - Invalidation has three drivers: (a) operator/CLI command,
//     (b) backend-published rotation signal, (c)
//     `Resolver.Invalidate(ref)` from a consumer that detected an
//     auth-rejection. All three end up calling Cache.Invalidate.
//
// Spec mapping: FR-010, FR-011, NFR-001, NFR-007.
package cache

import (
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	"github.com/kameas-ai/kenaz-harness/core/secrets/registry"
	"github.com/kameas-ai/kenaz-harness/core/secrets/secret"
)

// DefaultTTL is the default cache TTL applied when a backend has not
// declared an override. Plan §9 Q1: 60 seconds.
const DefaultTTL = 60 * time.Second

// entry is the internal cache record.
type entry struct {
	value    secret.Secret
	expires  time.Time
	kind     ref.RefKind
	backend  registry.BackendKind
}

// Clock abstracts time.Now for deterministic tests.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Cache is the TTL store. The zero value is unusable; call New().
type Cache struct {
	mu          sync.Mutex
	entries     map[string]*entry
	defaultTTL  time.Duration
	perBackend  map[registry.BackendKind]time.Duration
	clock       Clock
	rotationCh  chan ref.CredentialReference // backend-driven invalidation
	stop        chan struct{}
	stopped     bool
	wg          sync.WaitGroup
	subscribers []func(ref.CredentialReference)
}

// Option configures the cache at construction.
type Option func(*Cache)

// WithDefaultTTL sets the default TTL.
func WithDefaultTTL(d time.Duration) Option {
	return func(c *Cache) { c.defaultTTL = d }
}

// WithBackendTTL sets a per-backend TTL override.
func WithBackendTTL(b registry.BackendKind, d time.Duration) Option {
	return func(c *Cache) { c.perBackend[b] = d }
}

// WithClock substitutes a deterministic clock (for tests).
func WithClock(clk Clock) Option {
	return func(c *Cache) { c.clock = clk }
}

// New constructs a Cache.
func New(opts ...Option) *Cache {
	c := &Cache{
		entries:    make(map[string]*entry),
		defaultTTL: DefaultTTL,
		perBackend: make(map[registry.BackendKind]time.Duration),
		clock:      realClock{},
		rotationCh: make(chan ref.CredentialReference, 16),
		stop:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(c)
	}
	c.wg.Add(1)
	go c.rotationLoop()
	return c
}

// ttlFor returns the TTL applicable to backend kind b.
func (c *Cache) ttlFor(b registry.BackendKind) time.Duration {
	if d, ok := c.perBackend[b]; ok {
		return d
	}
	return c.defaultTTL
}

// Put inserts s under r's redaction-safe id. If a previous entry
// exists, it is destroyed before being overwritten.
func (c *Cache) Put(r ref.CredentialReference, b registry.BackendKind, s secret.Secret) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		s.Destroy()
		return
	}
	id := r.ID()
	if old, ok := c.entries[id]; ok && old.value != nil {
		old.value.Destroy()
	}
	c.entries[id] = &entry{
		value:   s,
		expires: c.clock.Now().Add(c.ttlFor(b)),
		kind:    r.Kind,
		backend: b,
	}
}

// Get returns the cached Secret if present and unexpired. The boolean
// is true on hit, false on miss. Expired entries are destroyed and
// removed transparently.
func (c *Cache) Get(r ref.CredentialReference) (secret.Secret, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := r.ID()
	e, ok := c.entries[id]
	if !ok {
		return nil, false
	}
	if !c.clock.Now().Before(e.expires) {
		// Expired.
		if e.value != nil {
			e.value.Destroy()
		}
		delete(c.entries, id)
		return nil, false
	}
	return e.value, true
}

// Invalidate destroys and removes any cached Secret for r and notifies
// subscribers. It is the operator-driven path (FR-011); backend-driven
// rotation goes through Notify.
func (c *Cache) Invalidate(r ref.CredentialReference) {
	c.mu.Lock()
	id := r.ID()
	e, ok := c.entries[id]
	if ok {
		if e.value != nil {
			e.value.Destroy()
		}
		delete(c.entries, id)
	}
	subs := append([]func(ref.CredentialReference){}, c.subscribers...)
	c.mu.Unlock()
	for _, fn := range subs {
		fn(r)
	}
}

// Notify is the backend-driven invalidation hook. A backend that
// detects rotation publishes the reference through this channel; the
// cache invalidates asynchronously in rotationLoop.
//
// Sending to a closed cache is a safe no-op.
func (c *Cache) Notify(r ref.CredentialReference) {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	select {
	case c.rotationCh <- r:
	default:
		// Channel full: drop. The next Resolve will re-resolve once
		// the TTL expires; rotation propagation is bounded by 5×TTL
		// p99 per NFR-007 even in this path.
	}
}

// Subscribe registers fn to be invoked whenever an entry is
// invalidated. Subscribers run after the cache mutex is released.
func (c *Cache) Subscribe(fn func(ref.CredentialReference)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscribers = append(c.subscribers, fn)
}

// Len returns the number of resident entries (used by tests and
// by the `harness secrets status` CLI).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Close drains the cache, destroys every Secret, and stops the
// rotation goroutine. Safe to call multiple times.
func (c *Cache) Close() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	close(c.stop)
	for id, e := range c.entries {
		if e.value != nil {
			e.value.Destroy()
		}
		delete(c.entries, id)
	}
	c.mu.Unlock()
	c.wg.Wait()
}

func (c *Cache) rotationLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			return
		case r := <-c.rotationCh:
			c.Invalidate(r)
		}
	}
}
