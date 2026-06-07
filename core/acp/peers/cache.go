package peers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/acp"
)

// DefaultCardCacheTTL is the v1 default Agent Card cache TTL (plan
// §9 Q3).
const DefaultCardCacheTTL = 5 * time.Minute

// CardFetcher is the chunk of the envelope surface this cache needs.
// Defined here as a small interface to keep the dependency direction
// peers -> envelope (one way only) and to make testing trivial.
type CardFetcher interface {
	FetchCard(ctx context.Context, peer acp.PeerProfile) (acp.AgentCard, error)
}

// CardCache holds AgentCards keyed by peer_id with per-entry TTL and
// version-mismatch invalidation. FR-008.
type CardCache struct {
	fetcher  CardFetcher
	verifier acp.Verifier
	emitter  AuthEventEmitter
	clock    func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
	maxSize int
}

type cacheEntry struct {
	card    acp.AgentCard
	expires time.Time
}

// CacheOption configures a CardCache.
type CacheOption func(*CardCache)

// WithClock injects a deterministic clock for tests.
func WithClock(fn func() time.Time) CacheOption {
	return func(c *CardCache) { c.clock = fn }
}

// WithMaxSize bounds the cache to keep at most n entries (LRU-ish:
// oldest expiry is evicted when full).
func WithMaxSize(n int) CacheOption {
	return func(c *CardCache) { c.maxSize = n }
}

// NewCardCache constructs a CardCache.
func NewCardCache(fetcher CardFetcher, verifier acp.Verifier, emitter AuthEventEmitter, opts ...CacheOption) *CardCache {
	if emitter == nil {
		emitter = NoopEmitter{}
	}
	c := &CardCache{
		fetcher:  fetcher,
		verifier: verifier,
		emitter:  emitter,
		clock:    func() time.Time { return time.Now() },
		entries:  map[string]cacheEntry{},
		maxSize:  1024,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Get returns the AgentCard for peer, fetching it from the underlying
// envelope on a cache miss. Cache hits emit `card_cache_hit`; misses
// emit `card_fetched`; version mismatches on a refetch emit
// `card_cache_miss` and re-populate the entry.
//
// Verifier rejection produces ErrVerifierRejected without populating
// the cache.
func (c *CardCache) Get(ctx context.Context, peer acp.PeerProfile) (acp.AgentCard, error) {
	now := c.clock()
	c.mu.Lock()
	entry, hit := c.entries[peer.PeerID]
	if hit && entry.expires.After(now) {
		c.mu.Unlock()
		c.emitter.CardCacheHit(ctx, "", peer.PeerID, entry.card.Version)
		return entry.card, nil
	}
	c.mu.Unlock()

	if c.fetcher == nil {
		return acp.AgentCard{}, errors.New("peers: card cache has no fetcher configured")
	}
	card, err := c.fetcher.FetchCard(ctx, peer)
	if err != nil {
		return acp.AgentCard{}, err
	}
	if c.verifier != nil {
		res, vErr := c.verifier.Verify(ctx, card)
		if vErr != nil || !res.Accepted {
			if vErr == nil {
				vErr = acp.ErrVerifierRejected
			}
			return acp.AgentCard{}, vErr
		}
	}

	prevVersion := ""
	if hit {
		prevVersion = entry.card.Version
		// Hit but expired or version-bumped — emit cache_miss with
		// reason for the audit trail.
		reason := "ttl_expired"
		if entry.card.Version != "" && card.Version != "" && entry.card.Version != card.Version {
			reason = "version_mismatch"
		}
		c.emitter.CardCacheMiss(ctx, "", peer.PeerID, prevVersion, card.Version, reason)
	}

	ttl := peer.CardCacheTTL
	if ttl <= 0 {
		ttl = DefaultCardCacheTTL
	}

	c.mu.Lock()
	if c.maxSize > 0 && len(c.entries) >= c.maxSize {
		c.evictOneestLocked(now)
	}
	c.entries[peer.PeerID] = cacheEntry{card: card, expires: now.Add(ttl)}
	c.mu.Unlock()

	c.emitter.CardFetched(ctx, "", peer.PeerID, card.Version)
	return card, nil
}

// Invalidate forcibly drops a cached entry (used by callers that
// detect a remote-version bump out-of-band).
func (c *CardCache) Invalidate(peerID string) {
	c.mu.Lock()
	delete(c.entries, peerID)
	c.mu.Unlock()
}

// Size returns the current entry count (test helper).
func (c *CardCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// evictOneestLocked drops the entry with the earliest expiry. Caller
// must hold c.mu.
func (c *CardCache) evictOneestLocked(now time.Time) {
	var (
		oldestKey string
		oldestExp time.Time
	)
	for k, e := range c.entries {
		if oldestKey == "" || e.expires.Before(oldestExp) {
			oldestKey = k
			oldestExp = e.expires
		}
		_ = now
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}
