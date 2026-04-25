//go:build !windows

package lockfile

import (
	"os"
	"syscall"
)

func flockExNB(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func flockUn(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
