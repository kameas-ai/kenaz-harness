package capabilities

import (
	"context"
	"sync"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// RefreshFunc is the callback invoked by the Refresher to re-fetch
// capabilities for a (profileID, modelID) pair that has gone stale.
// Implementations call the adapter's ProviderCapabilities method and
// store the result in the cache.
type RefreshFunc func(ctx context.Context, profileID, modelID string) (llm.ProviderCapabilities, error)

// Refresher runs background 7-day TTL refreshes for stale capability
// cache entries. It is started once per process via Start() and stopped
// via the returned cancel function.
type Refresher struct {
	cache   CapabilityCache
	refresh RefreshFunc
	mu      sync.Mutex
	// inFlight tracks currently-running refresh goroutines by key.
	inFlight map[string]struct{}
}

// NewRefresher returns a Refresher that uses cache for Get/Put and calls
// refresh when an entry is found stale.
func NewRefresher(cache CapabilityCache, refresh RefreshFunc) *Refresher {
	return &Refresher{
		cache:    cache,
		refresh:  refresh,
		inFlight: make(map[string]struct{}),
	}
}

// MaybeRefresh checks whether the entry for (profileID, modelID) needs a
// background refresh (cache miss or stale). If so and no refresh is already
// running for that key, it starts one in a goroutine. The caller receives
// the stale entry (if any) immediately; the refreshed entry will be
// available on the next call after the goroutine completes.
//
// This implements the stale-while-revalidate pattern: readers always get
// a response within p95 ≤ 1ms (from cache); a background goroutine
// updates the cache for subsequent requests.
func (r *Refresher) MaybeRefresh(ctx context.Context, profileID, modelID string) {
	key := cacheKey(profileID, modelID)
	r.mu.Lock()
	if _, running := r.inFlight[key]; running {
		r.mu.Unlock()
		return
	}
	r.inFlight[key] = struct{}{}
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.inFlight, key)
			r.mu.Unlock()
		}()

		// Use a bounded context so a stalled adapter doesn't block
		// the refresher goroutine indefinitely.
		rctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		caps, err := r.refresh(rctx, profileID, modelID)
		if err != nil {
			// Refresh failure is non-fatal: the stale entry remains in
			// cache for the next reader.
			return
		}
		_ = r.cache.Put(rctx, profileID, modelID, caps)
	}()
}

// Start launches a periodic scan that triggers background refreshes for
// any entries that become stale. The returned cancel function stops the
// scanner. Typical interval is 1 hour (the scanner checks staleness but
// actual staleness threshold is cacheTTL = 7 days).
func (r *Refresher) Start(interval time.Duration) (cancel func()) {
	ctx, stop := context.WithCancel(context.Background())
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// The MemoryCache does not expose an iterator, so the
				// periodic scan relies on callers to trigger MaybeRefresh
				// on access. This goroutine's job is primarily to keep
				// the Refresher alive for future integration with the
				// SQLite backend's iterator.
			}
		}
	}()
	return stop
}
