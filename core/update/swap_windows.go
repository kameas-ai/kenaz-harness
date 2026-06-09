//go:build windows

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// helperBinaryName is the bundled stand-alone updater that lives next
// to the main exe inside the Windows zip. core/update spawns it with
// the parent PID + staged path and exits; the helper waits, swaps,
// and relaunches the new binary. See cmd/kenaz-updater for the helper
// implementation.
const helperBinaryName = "kenaz-updater.exe"

// helperSpawner abstracts the helper-launch primitive so swap_windows
// tests can inject a recording fake without spawning a real process.
// Production code uses realHelperSpawner; tests in
// swap_windows_test.go inject a fakeHelperSpawner.
type helperSpawner interface {
	// Spawn starts the helper with the given args and returns nil on
	// successful spawn (the helper is now running detached). A non-nil
	// error indicates the spawn itself failed — caller falls back to
	// the deferred-swap path.
	Spawn(helperPath string, args []string) error
}

type realHelperSpawner struct{}

// Spawn launches the helper as a detached child process. We use
// exec.Cmd.Start (not Run) so the parent harness can exit immediately
// while the helper runs in the background — the whole point of the
// helper architecture.
func (realHelperSpawner) Spawn(helperPath string, args []string) error {
	cmd := exec.Command(helperPath, args...)
	// Don't tie stdio to the parent: the parent is about to exit and
	// the helper writes its diagnostics to <DataDir>/update/updater.log.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// realSwapperWindows extends the cross-platform realSwapper with the
// Windows-specific helper-spawn injection point. The Swap method
// declared on the bare realSwapper consumes this struct's spawner; a
// nil spawner falls back to the production realHelperSpawner.
//
// We don't add the field to realSwapper (cross-platform) because the
// Mac/Linux build tag wouldn't see helperSpawner at all. Keeping the
// hook private to swap_windows.go is the smallest seam.
var windowsSpawner helperSpawner = realHelperSpawner{}

// Swap on Windows: spawn the bundled kenaz-updater.exe helper, then
// return so ApplyAndRestart can call Restart (which os.Exit's). The
// helper waits for the parent to exit, re-verifies the staged sha256,
// renames the file into place, and launches the new binary.
//
// Falls back to the deferred-pending-marker path if:
//   - kenaz-updater.exe isn't bundled next to the running exe
//     (defensive — production always ships the helper, but a hand-
//     curated install may have stripped it).
//   - The helper spawn itself fails (rare; usually means the helper
//     binary is corrupted or AV blocked execution).
//
// The bootswap shim in core/update/bootswap/ remains the safety net
// for the deferred path — boot-time the next time the user launches.
func (realSwapper) Swap(ctx context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error {
	_ = ctx
	if !fileExists(staged.Path) {
		return fmt.Errorf("update: staged path missing: %s", staged.Path)
	}
	if runningBinaryPath == "" {
		return fmt.Errorf("update: empty running binary path")
	}
	// Re-verify before we hand off to the helper. Cheap and closes a
	// disk-tampering window between Download.staged-rename and Swap.
	if err := verifyFileSha256(staged.Path, staged.Sha256); err != nil {
		return fmt.Errorf("update: pre-swap sha256 verify: %w", err)
	}

	helperPath := filepath.Join(filepath.Dir(runningBinaryPath), helperBinaryName)
	if !fileExists(helperPath) {
		logging.L().Info("update.swap.helper_missing",
			"path", helperPath,
			"fallback", "deferred_swap",
		)
		return deferredSwap(staged, runningBinaryPath, dataDir)
	}

	helperArgs := buildHelperArgs(staged, runningBinaryPath, dataDir)
	spawner := windowsSpawner
	if spawner == nil {
		spawner = realHelperSpawner{}
	}
	if err := spawner.Spawn(helperPath, helperArgs); err != nil {
		logging.L().Warn("update.swap.helper_spawn_failed",
			"err", err.Error(),
			"fallback", "deferred_swap",
		)
		return deferredSwap(staged, runningBinaryPath, dataDir)
	}
	logging.L().Info("update.swap.helper_launched",
		"helper", helperPath,
		"target_version", staged.TargetVersion,
	)
	return nil
}

// buildHelperArgs assembles the argv handed to kenaz-updater.exe. The
// flag schema is documented in cmd/kenaz-updater/main.go — keep both
// in sync. We forward os.Args[1:] so the relaunched binary sees the
// same argv the user originally launched with (single-instance flag,
// debug toggles, etc.).
func buildHelperArgs(staged StagedUpdate, runningBinaryPath, dataDir string) []string {
	args := []string{
		"--parent-pid", strconv.Itoa(os.Getpid()),
		"--staged", staged.Path,
		"--target", runningBinaryPath,
		"--sha256", staged.Sha256,
		"--data-dir", dataDir,
	}
	for _, a := range os.Args[1:] {
		args = append(args, "--launch-args", a)
	}
	return args
}

// deferredSwap is the legacy fallback retained for the case where the
// helper isn't bundled or fails to spawn. Writes <DataDir>/update/
// pending.json so the bootswap shim picks it up on next launch.
//
// Identical semantics to the pre-helper Swap; only callsite changed.
func deferredSwap(staged StagedUpdate, runningBinaryPath, dataDir string) error {
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

// Restart on Windows: with the helper architecture, the parent's only
// job is to exit cleanly so the helper can rename the locked .exe.
// We use os.Exit(0) so deferred goroutines and finalizers don't
// linger — the helper has already fork-spawned and is sleeping on
// WaitForSingleObject.
//
// In the deferred-swap fallback path the helper isn't running; the
// bootswap shim handles the rename on the next launch instead.
func (realSwapper) Restart(ctx context.Context, newBinaryPath string) error {
	_ = ctx
	_ = newBinaryPath
	os.Exit(0)
	return nil // unreachable
}
