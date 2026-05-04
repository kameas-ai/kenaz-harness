//go:build !windows

package main

import (
	"errors"
	"os"
	"time"
)

// waitForParentExit on non-Windows platforms is a polling shim. The
// helper is shipped as a Windows binary in production, but we keep
// the cross-OS build green so `go build ./cmd/kenaz-updater/...` from
// a Mac dev box and `go vet ./...` both work.
//
// Polls os.FindProcess + Signal(0) every 200ms until the process is
// gone or the timeout elapses. Best-effort — Unix's Signal(0) on a
// non-existent PID returns os.ErrProcessDone wrapped, which we treat
// as success.
func waitForParentExit(parentPID int, timeout time.Duration) error {
	if parentPID <= 0 {
		return errors.New("invalid parent pid")
	}
	deadline := time.Now().Add(timeout)
	for {
		p, err := os.FindProcess(parentPID)
		if err != nil {
			return nil // already gone
		}
		if err := p.Signal(syscallZero); err != nil {
			return nil // gone
		}
		if time.Now().After(deadline) {
			return errors.New("timeout waiting for parent")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
