package update

import (
	"context"
	"errors"
	"os"
)

// Swapper is the per-platform install primitive. Production code
// builds a realSwapper; tests inject a fakeSwapper that records the
// calls without touching the running process. Hiding the swap behind
// this interface is what lets us unit-test ApplyAndRestart without
// fork-exec or os.Exit blowing up the test runner.
type Swapper interface {
	// Swap performs the platform-specific binary replacement. On
	// macOS/Linux it atomically renames the staged path over the
	// running binary path; on Windows it writes the pending.json
	// marker and returns. Implementations MUST verify the staged
	// sha256 before any destructive action.
	Swap(ctx context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error

	// Restart fork-execs the new binary path with the same args and
	// terminates the current process with status 0. On Windows this
	// is a plain os.Exit(0) — the bootswap shim relaunches on next
	// startup. Implementations MUST NOT return on the macOS/Linux
	// path (the test fake captures the call instead).
	Restart(ctx context.Context, newBinaryPath string) error
}

// errSwapNotSupported is returned by realSwapper on platforms where
// the swap helper has not been implemented. Today only the three
// targets {darwin, linux, windows} are supported; everything else
// trips this error so the user gets a loud failure rather than a
// silent no-op.
var errSwapNotSupported = errors.New("update: platform swap not supported on this OS")

// realSwapper is the production Swapper. Behavior is dispatched per
// runtime.GOOS in swap_unix.go and swap_windows.go to keep the
// platform-conditional code small and testable.
type realSwapper struct{}

// NewRealSwapper returns the production Swapper used by the rpc
// boot path. Exported so the rpc layer can construct it without
// knowing the underlying type.
func NewRealSwapper() Swapper { return realSwapper{} }

// fileExists reports whether the given path exists. Used in the
// per-platform swap to validate inputs before any destructive op.
func fileExists(p string) bool {
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
