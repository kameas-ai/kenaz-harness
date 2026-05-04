//go:build darwin || linux

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Swap on macOS/Linux: rename the staged artifact over the running
// binary path. On macOS the running process is not unlinked because
// the kernel keeps the in-memory image alive after the rename — this
// is the documented "drop-in replace" pattern used by Sparkle and
// other Mac auto-updaters.
func (realSwapper) Swap(ctx context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error {
	_ = ctx
	_ = dataDir
	if !fileExists(staged.Path) {
		return fmt.Errorf("update: staged path missing: %s", staged.Path)
	}
	if runningBinaryPath == "" {
		return fmt.Errorf("update: empty running binary path")
	}
	// Re-verify the sha256 immediately before the destructive rename.
	// The download step already verified, but the file was on disk in
	// between — re-checking here closes the TOCTOU window.
	if err := verifyFileSha256(staged.Path, staged.Sha256); err != nil {
		return fmt.Errorf("update: pre-swap sha256 verify: %w", err)
	}
	if err := os.Rename(staged.Path, runningBinaryPath); err != nil {
		return fmt.Errorf("update: rename %s -> %s: %w", staged.Path, runningBinaryPath, err)
	}
	return nil
}

// Restart on macOS/Linux: fork-exec the (now updated) binary with
// the same argv (minus argv[0]) and terminate the current process.
// The exec returns immediately because we use Cmd.Start, not Run.
func (realSwapper) Restart(ctx context.Context, newBinaryPath string) error {
	_ = ctx
	args := os.Args[1:]
	cmd := exec.Command(newBinaryPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("update: relaunch %s: %w", newBinaryPath, err)
	}
	os.Exit(0)
	return nil // unreachable
}
