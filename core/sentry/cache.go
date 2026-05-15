package sentry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/logging"
)

const (
	cacheFileName = "sentry-cache.json"
	cacheMaxBytes = 50 * 1024 // 50 KB per NFR-007
	cacheMaxItems = 5
)

// CacheEntry is one persisted crash event summary.
type CacheEntry struct {
	ID            string    `json:"id"`
	CapturedAt    time.Time `json:"capturedAt"`
	Kind          string    `json:"kind"`
	Summary       string    `json:"summary"`
	SentryEventID string    `json:"sentryEventId,omitempty"`
}

var (
	cacheMu sync.Mutex
)

// cachePath returns <dataDir>/sentry-cache.json.
func cachePath(dataDir string) string {
	return filepath.Join(dataDir, cacheFileName)
}

// loadCache reads the cache file. Returns an empty slice on any error.
func loadCache(dataDir string) []CacheEntry {
	b, err := os.ReadFile(cachePath(dataDir))
	if err != nil {
		return nil
	}
	var entries []CacheEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil
	}
	return entries
}

// saveCache writes entries to the cache file, clamping size.
func saveCache(dataDir string, entries []CacheEntry) {
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		logging.L().Warn("sentry.cache.marshal_error", "err", err.Error())
		return
	}
	// Clamp to cacheMaxBytes — drop oldest entries until under limit.
	for len(b) > cacheMaxBytes && len(entries) > 0 {
		entries = entries[1:]
		b, err = json.MarshalIndent(entries, "", "  ")
		if err != nil {
			return
		}
	}
	if err := os.WriteFile(cachePath(dataDir), b, 0o600); err != nil {
		logging.L().Warn("sentry.cache.write_error", "err", err.Error())
	}
}

// AppendToCache prepends a new entry and saves the cache. Trims to 5 entries.
// Thread-safe.
func AppendToCache(dataDir string, entry CacheEntry) {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	entries := loadCache(dataDir)
	// Prepend (newest first on disk, returned newest-first by GetLastFive).
	entries = append([]CacheEntry{entry}, entries...)
	if len(entries) > cacheMaxItems {
		entries = entries[:cacheMaxItems]
	}
	saveCache(dataDir, entries)
}

// GetLastFive returns up to 5 cached entries, newest first.
func GetLastFive(dataDir string) []CacheEntry {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return loadCache(dataDir)
}

// newCacheID returns a simple time-based unique ID for a cache entry.
func newCacheID() string {
	return fmt.Sprintf("sentry-%d", time.Now().UnixNano())
}
