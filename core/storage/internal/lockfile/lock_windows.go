//go:build windows

package lockfile

import (
	"os"

	"golang.org/x/sys/windows"
)

// flockExNB acquires a non-blocking exclusive byte-range lock on the
// first byte of the file via LockFileEx. The lock is released
// automatically when the file is closed; flockUn is provided for
// symmetry with the Unix path.
func flockExNB(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	return windows.LockFileEx(handle, flags, 0, 1, 0, &ol)
}

func flockUn(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	return windows.UnlockFileEx(handle, 0, 1, 0, &ol)
}
