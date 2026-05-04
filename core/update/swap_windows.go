//go:build windows

package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Swap on Windows: cannot replace the running .exe in place. Stage
// the artifact under <DataDir>/update/staging/ (already done by
// Service.Download — we just trust staged.Path here) and write a
// pending.json marker the bootswap shim consumes on next launch.
//
// The marker carries target_path + staged_path + sha256 +
// target_version so the shim can re-verify and rename without
// re-fetching the manifest.
func (realSwapper) Swap(ctx context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error {
	_ = ctx
	if !fileExists(staged.Path) {
		return fmt.Errorf("update: staged path missing: %s", staged.Path)
	}
	if runningBinaryPath == "" {
		return fmt.Errorf("update: empty running binary path")
	}
	// Re-verify the sha256 before writing the marker. A corrupt
	// staged file with a mismatched marker would brick the next
	// launch — fail loudly here instead.
	if err := verifyFileSha256(staged.Path, staged.Sha256); err != nil {
		return fmt.Errorf("update: pre-marker sha256 verify: %w", err)
	}
	markerPath := filepath.Join(dataDir, "update", "pending.json")
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return fmt.Errorf("update: mkdir pending dir: %w", err)
	}
	m := pendingMarker{
		TargetPath:    runningBinaryPath,
		StagedPath:    staged.Path,
		Sha256:        staged.Sha256,
		TargetVersion: staged.TargetVersion,
		Platform:      staged.Platform,
	}
	if err := writePendingMarker(markerPath, m); err != nil {
		return fmt.Errorf("update: write pending marker: %w", err)
	}
	return nil
}

// Restart on Windows: cannot relaunch the new binary in-process
// because the swap was deferred to the boot-time shim. Just exit;
// the user (or their shell) relaunches and the shim does the rename
// before main() runs.
func (realSwapper) Restart(ctx context.Context, newBinaryPath string) error {
	_ = ctx
	_ = newBinaryPath
	os.Exit(0)
	return nil // unreachable
}
