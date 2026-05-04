//go:build !darwin && !linux && !windows

package update

import "context"

// Swap on unsupported platforms returns errSwapNotSupported. The user
// gets a loud failure rather than a silent no-op.
func (realSwapper) Swap(ctx context.Context, staged StagedUpdate, runningBinaryPath, dataDir string) error {
	_ = ctx
	_ = staged
	_ = runningBinaryPath
	_ = dataDir
	return errSwapNotSupported
}

// Restart on unsupported platforms returns errSwapNotSupported.
func (realSwapper) Restart(ctx context.Context, newBinaryPath string) error {
	_ = ctx
	_ = newBinaryPath
	return errSwapNotSupported
}
