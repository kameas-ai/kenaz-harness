// Package bootswap implements the boot-time swap shim that completes
// a deferred update on Windows. Called from main() before any other
// init so the running .exe can be replaced before the user-facing
// Wails app boots.
//
// On macOS/Linux the auto-update path swaps in-process, so this is a
// no-op there. The package ships on all platforms (one consolidated
// import in main.go) for forward-compatibility — if a future
// platform needs the same deferred-swap pattern, the only change is
// adding a build tag in this package, not in main.go.
//
// # Behavior
//
// On entry, MaybeSwapAndRelaunch:
//
//  1. Reads <DataDir>/update/pending.json. Missing → return (no-op).
//  2. Validates the marker schema and re-verifies sha256 of
//     marker.staged_path. A mismatch deletes the marker and returns
//     a hard error (the staged binary was corrupted between the
//     download and the relaunch).
//  3. Atomically renames marker.staged_path over marker.target_path.
//  4. Deletes the marker.
//  5. Calls Relauncher.Relaunch(target_path), which fork-execs the
//     new binary and terminates the current process.
//
// Step (5) is hidden behind a Relauncher interface so tests can
// drive end-to-end without nuking the test process.
package bootswap

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// Relauncher is the fork-exec primitive. Production callers use
// NewOSRelauncher; tests inject a fake that records the call.
type Relauncher interface {
	// Relaunch starts target with the given args (typically
	// os.Args[1:]) and exits the current process. MUST NOT return
	// in production; tests' fakes record + return for assertion.
	Relaunch(target string, args []string) error
}

// SwapResult is returned by MaybeSwapAndRelaunch for tests + audit
// logging. In production callers ignore the return value because
// Relaunch terminates the process.
type SwapResult struct {
	// Swapped is true iff a marker was found, validated, and the
	// rename completed. False on the no-marker fast path.
	Swapped bool
	// TargetVersion is the version string from the marker.
	TargetVersion string
	// TargetPath is the binary path that was overwritten.
	TargetPath string
}

// Config wires MaybeSwapAndRelaunch. Required fields are validated
// at the top of the function rather than via a constructor — this
// package is a one-shot helper, not a long-lived service.
type Config struct {
	// DataDir is the harness data directory. The marker is read from
	// <DataDir>/update/pending.json.
	DataDir string
	// Relauncher is the fork-exec primitive. nil falls back to the
	// real OS implementation.
	Relauncher Relauncher
	// Args is os.Args[1:] (or a test override). Threaded through to
	// Relaunch verbatim.
	Args []string
}

// MaybeSwapAndRelaunch is the entrypoint. Returns a SwapResult and
// (sometimes) an error. In production a Swapped=true result NEVER
// returns to the caller because Relauncher.Relaunch terminates the
// process; tests with a fake Relauncher get the captured result.
//
// Errors are intentionally non-fatal at the call site: a corrupt
// marker should NOT brick the user's launch. The shim logs and
// returns; main() continues to boot the existing (un-updated)
// binary so the user has a working app to upgrade from again.
func MaybeSwapAndRelaunch(cfg Config) (SwapResult, error) {
	if cfg.DataDir == "" {
		return SwapResult{}, errors.New("bootswap: DataDir required")
	}
	markerPath := filepath.Join(cfg.DataDir, "update", "pending.json")
	m, ok, err := readMarker(markerPath)
	if err != nil {
		// Marker exists but is malformed. Delete it so the user can
		// re-attempt the update; log loudly.
		logging.L().Warn("update.bootswap.invalid_marker", "err", err.Error())
		_ = os.Remove(markerPath)
		return SwapResult{}, err
	}
	if !ok {
		return SwapResult{}, nil
	}

	// Re-verify sha256 immediately before the destructive rename.
	if err := verifyFileSha256(m.StagedPath, m.Sha256); err != nil {
		logging.L().Warn("update.bootswap.sha_mismatch",
			"err", err.Error(),
			"version", m.TargetVersion,
		)
		_ = os.Remove(markerPath)
		_ = os.Remove(m.StagedPath)
		return SwapResult{}, err
	}

	if err := os.Rename(m.StagedPath, m.TargetPath); err != nil {
		logging.L().Warn("update.bootswap.rename_failed",
			"err", err.Error(),
			"target", m.TargetPath,
		)
		// Don't delete the marker — give the user a chance to
		// retry on next launch (the staged file is still present).
		return SwapResult{}, fmt.Errorf("bootswap: rename %s -> %s: %w", m.StagedPath, m.TargetPath, err)
	}

	if err := os.Remove(markerPath); err != nil {
		// Non-fatal — the rename already succeeded so we MUST
		// proceed with the relaunch. A stale marker on next launch
		// would re-attempt and fail at the rename step (target now
		// IS the new version), which is recoverable.
		logging.L().Warn("update.bootswap.marker_cleanup_failed", "err", err.Error())
	}

	logging.L().Info("update.bootswap.applied",
		"version", m.TargetVersion,
		"target", m.TargetPath,
	)

	r := cfg.Relauncher
	if r == nil {
		r = NewOSRelauncher()
	}
	if err := r.Relaunch(m.TargetPath, cfg.Args); err != nil {
		return SwapResult{Swapped: true, TargetVersion: m.TargetVersion, TargetPath: m.TargetPath},
			fmt.Errorf("bootswap: relaunch: %w", err)
	}
	return SwapResult{Swapped: true, TargetVersion: m.TargetVersion, TargetPath: m.TargetPath}, nil
}
