package advice

import (
	"container/list"
	"sync"
)

// defaultAdviceCacheCapacity bounds the number of distinct cache entries
// an LLMAdvisor tracks across every session it serves. Mirrors
// risk.defaultRaterCacheCapacity's bounded-LRU rationale exactly: a
// long-running process serving many sessions must keep memory flat, and
// evicting the least-recently-used entry only costs a redundant advisor
// call on the next lookup for a call pattern gone cold — never a
// correctness problem, EXCEPT for a dismissed entry: eviction can
// re-surface a recommendation the user already dismissed once the LRU
// forgets it. Bounded at 512 (the rater's number) on the theory that a
// session actively cycling through more than 512 distinct (kind,
// features) pairs is already an edge case the cache's job (avoid
// redundant model calls / re-showing dismissed advice within a SINGLE
// session's normal use) was never trying to cover.
const defaultAdviceCacheCapacity = 512

// cacheKey is the advice cache's lookup key: (session, kind,
// features-hash, prompt-version). SessionID is a real struct field — not
// folded into the hash — so cross-session isolation is structural,
// mirroring risk.cacheKey's doc comment: two sessions asking the same
// kind the same materially-identical question never collide in the map.
type cacheKey struct {
	sessionID     string
	kindID        string
	featuresHash  string
	promptVersion string
}

// cacheEntry is one LRU node's payload. dismissed marks that the caller
// recorded a Dismiss for this exact (session, kind, features,
// promptVersion) — AC-03's "a dismissed recommendation is not re-shown
// for materially identical features within the session" proof point.
// When dismissed is true, rec may be the zero value (Dismiss can plant a
// tombstone before any Recommend ever computed a real recommendation for
// this key — see adviceCache.dismiss).
type cacheEntry struct {
	key       cacheKey
	rec       Recommendation
	dismissed bool
}

// adviceCache is a bounded, race-safe LRU cache of Recommendation values
// keyed by cacheKey, plus dismissal tombstones. Mirrors risk.ratingCache
// exactly in structure and concurrency contract (CLAUDE.md's mutex +
// snapshot-free direct-return shape — callers never need a point-in-time
// enumeration, only get/put/dismiss).
type adviceCache struct {
	mu       sync.Mutex
	order    *list.List
	elems    map[cacheKey]*list.Element
	capacity int
}

func newAdviceCache(capacity int) *adviceCache {
	if capacity <= 0 {
		capacity = defaultAdviceCacheCapacity
	}
	return &adviceCache{
		order:    list.New(),
		elems:    make(map[cacheKey]*list.Element, capacity),
		capacity: capacity,
	}
}

// get returns the cached entry for key, promoting it to
// most-recently-used on a hit.
func (c *adviceCache) get(key cacheKey) (cacheEntry, bool) {
	if c == nil {
		return cacheEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.elems[key]
	if !ok {
		return cacheEntry{}, false
	}
	c.order.MoveToFront(el)
	return *el.Value.(*cacheEntry), true
}

// put inserts or replaces the cached Recommendation for key, clearing
// any prior dismissal (a fresh model call means the caller is asking
// again from scratch — Dismiss is what re-establishes a skip, not put).
// Evicts the least-recently-used entry first if the cache is already at
// capacity and key is new.
func (c *adviceCache) put(key cacheKey, rec Recommendation) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.elems[key]; ok {
		entry := el.Value.(*cacheEntry)
		entry.rec = rec
		entry.dismissed = false
		c.order.MoveToFront(el)
		return
	}

	if c.order.Len() >= c.capacity {
		if oldest := c.order.Back(); oldest != nil {
			c.order.Remove(oldest)
			delete(c.elems, oldest.Value.(*cacheEntry).key)
		}
	}

	entry := &cacheEntry{key: key, rec: rec}
	c.elems[key] = c.order.PushFront(entry)
}

// dismiss marks key as dismissed, upserting a tombstone entry (zero
// Recommendation) if no entry exists yet — Dismiss can be called before
// any Recommend has computed a real recommendation for this exact key in
// degenerate call orders, and the skip must hold regardless.
func (c *adviceCache) dismiss(key cacheKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.elems[key]; ok {
		el.Value.(*cacheEntry).dismissed = true
		c.order.MoveToFront(el)
		return
	}

	if c.order.Len() >= c.capacity {
		if oldest := c.order.Back(); oldest != nil {
			c.order.Remove(oldest)
			delete(c.elems, oldest.Value.(*cacheEntry).key)
		}
	}
	entry := &cacheEntry{key: key, dismissed: true}
	c.elems[key] = c.order.PushFront(entry)
}
