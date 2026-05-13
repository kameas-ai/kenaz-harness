//go:build !windows

package tasks

import (
	"os"
	"syscall"
)

// sigterm sends SIGTERM to the given os.Process. Unix-only.
func sigterm(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
