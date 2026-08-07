//go:build unix

package workspace

import (
	"os"
	"syscall"
)

// statDev is the unix implementation of the device-number probe backing
// statDevFn: it stats path and reads the device number off the raw
// syscall.Stat_t, which is how isMountpoint tells a virtio-fs bind mount
// (different device than its parent) from a plain stub directory (same
// device). Every production caller of this package — the served-mode
// harness inside a Linux workbench guest and the macOS/Linux desktop
// builds — runs on a unix GOOS, so this is the exercised path.
func statDev(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
