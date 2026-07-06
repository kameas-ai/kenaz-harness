// Package sites — recents.go
//
// RecordRecent / LoadRecents manage a capped recents index at
// <DataDir>/sites/recent.json, mapping site root directory paths to their
// last known deployment metadata.
//
// Atomic write protocol mirrors core/mcp/recipes/enabled.go:
//  1. MkdirAll on the parent directory.
//  2. Marshal to indented JSON.
//  3. os.CreateTemp (sibling .tmp-* file) + Write + Sync + Close.
//  4. os.Rename tmp → final (atomic on POSIX).
//  5. fsync the parent directory.
//
// FR-201: capped at 10 entries (oldest evicted by deployed_at).
// FR-201: LoadRecents returns empty slice (not error) on missing or corrupt file.
//
// OSS-boundary: no imports from core/fleet.
// Mission: sites-foundation-01NSITE04, WP04.
package sites

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	recentFilePath = "sites/recent.json"
	recentMaxCap   = 10
)

// RecentSite holds metadata for one recently-deployed site, keyed by the
// root_dir on disk (absolute path to the site directory the user deployed from).
type RecentSite struct {
	// Name is the site slug from kameas-site.json.
	Name string `json:"name"`
	// Kind is "static" or "dynamic".
	Kind string `json:"kind"`
	// LastDeploymentID is the deployment_id returned by the Fleet API on
	// the most recent deploy. Empty if the deploy has not yet completed.
	LastDeploymentID string `json:"last_deployment_id,omitempty"`
	// LastURL is the full site URL returned by the Fleet API.
	LastURL string `json:"last_url,omitempty"`
	// DeployedAt is the timestamp of the last completed deployment.
	DeployedAt time.Time `json:"deployed_at"`
}

// recentsFile is the on-disk shape. We keep the full map for O(1) lookups
// while preserving insertion order for ordered output.
type recentsFile struct {
	Entries map[string]RecentSite `json:"entries"`
	// Order tracks the insertion/update order for deterministic eviction.
	// Keys are root_dir values that match entries above.
	Order []string `json:"order"`
}

// RecordRecent upserts an entry for rootDir in <dataDir>/sites/recent.json.
// If this would exceed recentMaxCap, the oldest entries (by DeployedAt, then
// by position in the order slice) are evicted until the cap is met.
//
// The write is atomic (tmp + rename). rootDir should be an absolute path;
// the function does not enforce this — callers should call filepath.Abs first.
func RecordRecent(dataDir, rootDir, name, kind, deploymentID, url string) error {
	if dataDir == "" {
		return fmt.Errorf("sites/recents: dataDir is empty")
	}
	if rootDir == "" {
		return fmt.Errorf("sites/recents: rootDir is empty")
	}

	rf, err := loadRaw(dataDir)
	if err != nil {
		// On load error (corrupt) start fresh — same as LoadRecents policy.
		rf = &recentsFile{
			Entries: make(map[string]RecentSite),
			Order:   nil,
		}
	}

	now := time.Now().UTC()
	rec := RecentSite{
		Name:             name,
		Kind:             kind,
		LastDeploymentID: deploymentID,
		LastURL:          url,
		DeployedAt:       now,
	}

	_, existing := rf.Entries[rootDir]
	rf.Entries[rootDir] = rec

	if !existing {
		rf.Order = append(rf.Order, rootDir)
	} else {
		// Move to end (most recently used).
		rf.Order = moveToEnd(rf.Order, rootDir)
	}

	// Evict oldest entries if over cap.
	for len(rf.Entries) > recentMaxCap {
		oldest := rf.Order[0]
		delete(rf.Entries, oldest)
		rf.Order = rf.Order[1:]
	}

	return saveRaw(dataDir, rf)
}

// LoadRecents returns the list of recent sites, sorted by DeployedAt descending
// (most recently deployed first). Returns an empty slice (not an error) when
// the file is missing or corrupt.
func LoadRecents(dataDir string) ([]RecentSite, error) {
	if dataDir == "" {
		return nil, nil
	}
	rf, err := loadRaw(dataDir)
	if err != nil {
		return nil, nil // corrupt-tolerant: log already emitted by loadRaw
	}

	out := make([]RecentSite, 0, len(rf.Entries))
	for _, rec := range rf.Entries {
		out = append(out, rec)
	}
	// Sort by DeployedAt descending (most recent first).
	sort.Slice(out, func(i, j int) bool {
		return out[i].DeployedAt.After(out[j].DeployedAt)
	})
	return out, nil
}

// loadRaw reads and parses <dataDir>/sites/recent.json. Returns a fresh empty
// recentsFile on ErrNotExist; logs a warning and returns empty on parse error.
func loadRaw(dataDir string) (*recentsFile, error) {
	path := filepath.Join(dataDir, recentFilePath)
	data, err := os.ReadFile(path) // #nosec G304 -- dataDir is harness-owned
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &recentsFile{Entries: make(map[string]RecentSite)}, nil
		}
		return nil, fmt.Errorf("sites/recents: read %s: %w", path, err)
	}
	var rf recentsFile
	if err := json.Unmarshal(data, &rf); err != nil {
		log.Printf("sites/recents: warn: corrupt %s (%v) — resetting recents index", path, err)
		return nil, fmt.Errorf("sites/recents: corrupt: %w", err)
	}
	if rf.Entries == nil {
		rf.Entries = make(map[string]RecentSite)
	}
	return &rf, nil
}

// saveRaw atomically writes rf to <dataDir>/sites/recent.json.
func saveRaw(dataDir string, rf *recentsFile) error {
	finalPath := filepath.Join(dataDir, recentFilePath)
	parent := filepath.Dir(finalPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("sites/recents: mkdir %s: %w", parent, err)
	}

	data, err := json.MarshalIndent(rf, "", "  ")
	if err != nil {
		return fmt.Errorf("sites/recents: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(parent, filepath.Base(finalPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("sites/recents: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sites/recents: write tmp: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sites/recents: chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sites/recents: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("sites/recents: close tmp: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		cleanup()
		return fmt.Errorf("sites/recents: rename %s -> %s: %w", tmpPath, finalPath, err)
	}
	_ = os.Chmod(finalPath, 0o600) // reassert after rename

	// fsync parent directory for durability.
	if d, err := os.Open(parent); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// moveToEnd removes key from slice and appends it at the end.
func moveToEnd(order []string, key string) []string {
	out := make([]string, 0, len(order))
	for _, k := range order {
		if k != key {
			out = append(out, k)
		}
	}
	return append(out, key)
}
