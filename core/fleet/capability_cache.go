package fleet

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// capabilitiesPath returns the canonical path of the capability cache file
// inside the harness data directory. This is the single source of truth for
// the path; both load and save use this function.
func capabilitiesPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "capabilities.json")
}

// LoadCapabilities reads the capability cache from disk. Three outcomes:
//
//   - File present + parseable → (Capabilities, nil). Source is set to "cache".
//   - File absent (os.IsNotExist) → (zero-value Capabilities, nil).
//   - File present but corrupt → warn-log, return (zero-value Capabilities, nil).
//
// The function never returns an error for missing or corrupt files so that
// callers can always fall back to the poller fetching fresh data.
func LoadCapabilities(dataDir string) (Capabilities, error) {
	if dataDir == "" {
		return Capabilities{}, nil
	}
	path := capabilitiesPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Capabilities{}, nil
		}
		// Other read errors (permissions, IO) — treat as absent.
		log.Printf("fleet: capabilities cache read error (treating as absent): %v", err)
		return Capabilities{}, nil
	}
	var c Capabilities
	if err := json.Unmarshal(data, &c); err != nil {
		log.Printf("fleet: capabilities cache corrupt (resetting): %v", err)
		return Capabilities{}, nil
	}
	// Mark the snapshot as coming from disk cache.
	c.Source = "cache"
	return c, nil
}

// SaveCapabilities atomically persists a Capabilities snapshot to disk using a
// temp-file + rename pattern (same as identity.go) to avoid partial writes.
func SaveCapabilities(dataDir string, c Capabilities) error {
	if dataDir == "" {
		return nil
	}
	dir := filepath.Join(dataDir, "fleet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("fleet: mkdir fleet (capabilities): %w", err)
	}
	data, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("fleet: marshal capabilities: %w", err)
	}
	path := capabilitiesPath(dataDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("fleet: write capabilities tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("fleet: rename capabilities: %w", err)
	}
	return nil
}
