//go:build darwin

package mlsidecar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// hdiutilMounter mounts a .dmg read-only with hdiutil, at a private
// mount point (no /Volumes entry, no Finder window: -nobrowse
// -noautoopen). -noverify is deliberate: the caller has ALREADY verified
// the image's bytes against the A-1 manifest digest, which is strictly
// stronger than hdiutil's internal checksum, and skipping it avoids a
// second full read of a ~200 MB image.
type hdiutilMounter struct{}

// DefaultDMGMounter returns the hdiutil-backed mounter.
func DefaultDMGMounter() DMGMounter { return hdiutilMounter{} }

func (hdiutilMounter) Attach(ctx context.Context, dmgPath string) (string, func() error, error) {
	mp, err := os.MkdirTemp("", "kenaz-ml-mnt-")
	if err != nil {
		return "", nil, fmt.Errorf("mkdir mount point: %w", err)
	}
	out, err := exec.CommandContext(ctx, "hdiutil", "attach",
		"-nobrowse", "-readonly", "-noverify", "-noautoopen",
		"-mountpoint", mp, dmgPath).CombinedOutput()
	if err != nil {
		_ = os.Remove(mp)
		return "", nil, fmt.Errorf("hdiutil attach: %w: %s", err, out)
	}
	detach := func() error {
		// Detach must run even if the install ctx was cancelled.
		out, derr := exec.Command("hdiutil", "detach", "-force", mp).CombinedOutput()
		if derr != nil {
			return fmt.Errorf("hdiutil detach: %w: %s", derr, out)
		}
		_ = os.Remove(mp)
		return nil
	}
	return mp, detach, nil
}
