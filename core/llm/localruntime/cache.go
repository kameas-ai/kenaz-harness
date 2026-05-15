package localruntime

import (
	"context"
	"sync"
	"time"
)

const detectionCacheTTL = 5 * time.Minute

// detectionCache holds a cached DetectAll snapshot and its timestamp.
// The zero value is a valid (empty) cache.
type detectionCache struct {
	mu        sync.Mutex
	snapshot  []RuntimeInfo
	fetchedAt time.Time
}

// defaultCache is the package-level singleton detection cache.
var defaultCache detectionCache

// GetOrDetect returns the cached detection snapshot if it is younger than
// detectionCacheTTL; otherwise it calls DetectAll, stores the result,
// and returns the fresh snapshot.
func GetOrDetect(ctx context.Context) []RuntimeInfo {
	defaultCache.mu.Lock()
	defer defaultCache.mu.Unlock()

	if defaultCache.snapshot != nil && time.Since(defaultCache.fetchedAt) < detectionCacheTTL {
		return defaultCache.snapshot
	}

	// Release the mutex during detection to avoid blocking concurrent
	// GetOrDetect callers for the full probe duration. We re-acquire to
	// store the result.
	defaultCache.mu.Unlock()
	fresh := DetectAll(ctx)
	defaultCache.mu.Lock()

	defaultCache.snapshot = fresh
	defaultCache.fetchedAt = time.Now()
	return fresh
}

// Invalidate clears the cache so the next GetOrDetect call triggers a fresh
// DetectAll. Called by RescanLocalRuntimes.
func Invalidate() {
	defaultCache.mu.Lock()
	defaultCache.snapshot = nil
	defaultCache.fetchedAt = time.Time{}
	defaultCache.mu.Unlock()
}
