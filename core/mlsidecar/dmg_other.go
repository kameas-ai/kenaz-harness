//go:build !darwin

package mlsidecar

import "context"

type unsupportedMounter struct{}

// DefaultDMGMounter on non-macOS platforms refuses with ErrDMGUnsupported:
// there is no hdiutil, and the engine DMG is a macOS artifact.
func DefaultDMGMounter() DMGMounter { return unsupportedMounter{} }

func (unsupportedMounter) Attach(context.Context, string) (string, func() error, error) {
	return "", nil, ErrDMGUnsupported
}
