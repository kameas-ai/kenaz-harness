package risk

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// defaultRaterCacheCapacity bounds the number of distinct cache entries
// an LLMRater tracks across every session it serves. Mirrors
// core/agentgraph's doomLoopHistoryCapacity pattern (a bounded LRU, not
// an unbounded per-session map): a long-running process serving many
// sessions must keep memory flat, and evicting the least-recently-used
// entry only costs a redundant rater call on the next lookup for a
// call pattern that has gone cold — never a correctness problem.
const defaultRaterCacheCapacity = 512

// cacheKey is the rater cache's lookup key: (session, tool,
// normalized-args-hash, prompt-version). SessionID is a real struct
// field — not folded into the hash — so cross-session isolation is
// structural: two sessions rating byte-identical (tool, args) never
// collide in the map, and a payload that poisoned one session's rating
// cannot poison another's cache entry merely by matching hash bytes.
type cacheKey struct {
	sessionID     string
	tool          string
	argsHash      string
	promptVersion string
}

// hashNormalizedArgs derives the cache key's argsHash component from an
// already-normalized argument string (NormalizeArgs's output). Hashing
// rather than storing the raw string keeps map keys small and fixed-size
// regardless of argument payload length.
func hashNormalizedArgs(normalizedArgs string) string {
	sum := sha256.Sum256([]byte(normalizedArgs))
	return hex.EncodeToString(sum[:])
}

// cacheEntry is one LRU node's payload.
type cacheEntry struct {
	key    cacheKey
	rating Rating
}

// ratingCache is a bounded, race-safe LRU cache of Rating values keyed
// by cacheKey. Session-scoped by construction (sessionID is part of the
// key): a rating computed for one session is never returned as a hit for
// a different session, even for byte-identical (tool, normalized args) —
// see cacheKey's doc comment for why that matters against prompt
// injection.
//
// Safe for concurrent use: production dispatch fans tool calls out
// across a worker pool (CLAUDE.md's race-safe test-fake discipline
// applies here too, though this is production code rather than a test
// fake — the mutex + snapshot-free direct-return shape is deliberately
// simple since callers never need a point-in-time enumeration, only
// get/put).
type ratingCache struct {
	mu       sync.Mutex
	order    *list.List
	elems    map[cacheKey]*list.Element
	capacity int
}

// newRatingCache returns an empty cache bounded to capacity distinct
// keys. capacity <= 0 falls back to defaultRaterCacheCapacity.
func newRatingCache(capacity int) *ratingCache {
	if capacity <= 0 {
		capacity = defaultRaterCacheCapacity
	}
	return &ratingCache{
		order:    list.New(),
		elems:    make(map[cacheKey]*list.Element, capacity),
		capacity: capacity,
	}
}

// get returns the cached Rating for key, promoting it to
// most-recently-used on a hit.
func (c *ratingCache) get(key cacheKey) (Rating, bool) {
	if c == nil {
		return Rating{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.elems[key]
	if !ok {
		return Rating{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*cacheEntry).rating, true
}

// put inserts or replaces the cached Rating for key, evicting the
// least-recently-used entry first if the cache is already at capacity
// and key is new.
func (c *ratingCache) put(key cacheKey, rating Rating) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.elems[key]; ok {
		el.Value.(*cacheEntry).rating = rating
		c.order.MoveToFront(el)
		return
	}

	if c.order.Len() >= c.capacity {
		if oldest := c.order.Back(); oldest != nil {
			c.order.Remove(oldest)
			delete(c.elems, oldest.Value.(*cacheEntry).key)
		}
	}

	entry := &cacheEntry{key: key, rating: rating}
	c.elems[key] = c.order.PushFront(entry)
}
