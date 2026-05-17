// Package fleet — weight_urls.go
//
// Persists kameas-ml weight URLs from fleet config bundles to disk.
// The persisted file is consumed by the kameas-ml subsystem (out-of-scope for
// this mission); we simply cache to disk and expose the in-memory snapshot.
//
// (fleet-config-pull-01NDFSEX10 WP04)
package fleet

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// weightURLsPath returns the canonical path for the cached weight URLs file.
func weightURLsPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "weight_urls.json")
}

// weightURLState is the singleton in-memory cache for kameas-ml weight URLs.
var weightURLState struct {
	mu   sync.RWMutex
	urls []string
}

// SetWeightURLs updates the in-memory weight URL cache and persists to disk.
// Called by the fleet config poller after each successful bundle apply.
func SetWeightURLs(dataDir string, urls []string) {
	weightURLState.mu.Lock()
	cp := make([]string, len(urls))
	copy(cp, urls)
	weightURLState.urls = cp
	weightURLState.mu.Unlock()

	if dataDir == "" {
		return
	}
	if err := saveWeightURLs(dataDir, urls); err != nil {
		log.Printf("fleet: save weight URLs: %v", err)
	}
}

// CurrentWeightURLs returns the in-memory weight URL snapshot. Loads from
// disk on first call if the in-memory cache is empty.
func CurrentWeightURLs(dataDir string) []string {
	weightURLState.mu.RLock()
	urls := weightURLState.urls
	weightURLState.mu.RUnlock()
	if urls != nil {
		return urls
	}

	// Try to load from disk.
	loaded, err := loadWeightURLs(dataDir)
	if err != nil {
		return nil
	}
	weightURLState.mu.Lock()
	weightURLState.urls = loaded
	weightURLState.mu.Unlock()
	return loaded
}

func saveWeightURLs(dataDir string, urls []string) error {
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir weight_urls: %w", err)
	}
	data, err := json.Marshal(urls)
	if err != nil {
		return fmt.Errorf("fleet: marshal weight_urls: %w", err)
	}
	path := weightURLsPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("fleet: write weight_urls tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fleet: rename weight_urls: %w", err)
	}
	return nil
}

func loadWeightURLs(dataDir string) ([]string, error) {
	if dataDir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(weightURLsPath(dataDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var urls []string
	if err := json.Unmarshal(data, &urls); err != nil {
		return nil, err
	}
	return urls, nil
}
