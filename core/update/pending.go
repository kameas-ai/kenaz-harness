package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// pendingMarker is the on-disk struct written to
// <DataDir>/update/pending.json by the Windows swap path and read by
// the bootswap shim on next launch. The shim atomically renames
// StagedPath over TargetPath, deletes the marker, and execs the
// updated binary.
//
// The struct is shipped on all platforms (the bootswap package
// reads it everywhere) but only the Windows code path writes it.
type pendingMarker struct {
	// TargetPath is the absolute path of the binary the shim should
	// overwrite — typically the running .exe under Program Files
	// or %LOCALAPPDATA%.
	TargetPath string `json:"target_path"`
	// StagedPath is the absolute path of the verified replacement
	// binary under <DataDir>/update/staging/.
	StagedPath string `json:"staged_path"`
	// Sha256 is the hex digest of StagedPath. The shim re-verifies
	// before the rename so a corrupted staged file blocks the swap.
	Sha256 string `json:"sha256"`
	// TargetVersion is the version string the staged binary
	// represents. Used by the shim's audit log line.
	TargetVersion string `json:"target_version"`
	// Platform is the GOOS/GOARCH tuple — sanity-checked by the
	// shim before swapping. A mismatch (e.g. an x86 marker on an
	// arm64 host) blocks the swap.
	Platform string `json:"platform"`
}

// writePendingMarker writes the marker to path atomically (write to
// path.tmp, then rename). The atomic rename avoids a half-written
// JSON file from a power loss between writer's open + close.
func writePendingMarker(path string, m pendingMarker) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("update: marshal pending marker: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("update: write tmp marker %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("update: rename marker %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// readPendingMarker loads the marker from path. Returns
// (zero, false, nil) if the file does not exist; (zero, false, err)
// on any other error including invalid JSON. Used by the bootswap
// shim and shared with internal helpers in this package.
func readPendingMarker(path string) (pendingMarker, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return pendingMarker{}, false, nil
	}
	if err != nil {
		return pendingMarker{}, false, fmt.Errorf("update: read pending marker %s: %w", path, err)
	}
	var m pendingMarker
	if err := json.Unmarshal(body, &m); err != nil {
		return pendingMarker{}, false, fmt.Errorf("update: decode pending marker: %w", err)
	}
	if m.TargetPath == "" || m.StagedPath == "" || m.Sha256 == "" {
		return pendingMarker{}, false, fmt.Errorf("update: pending marker missing required fields")
	}
	return m, true, nil
}

// deletePendingMarker removes the marker. Best-effort — used in
// tests and shared with the bootswap shim cleanup path.
func deletePendingMarker(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("update: remove pending marker: %w", err)
	}
	return nil
}
