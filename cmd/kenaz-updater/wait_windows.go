//go:build windows

package main

import (
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// waitForParentExit blocks until the process identified by parentPID
// exits or the timeout elapses. Implementation:
//
//   - OpenProcess with SYNCHRONIZE | QUERY_LIMITED_INFORMATION so we
//     can both WaitForSingleObject AND poll GetExitCodeProcess.
//   - WaitForSingleObject with the configured timeout; returns
//     WAIT_OBJECT_0 when the process exits.
//
// If OpenProcess fails with ERROR_INVALID_PARAMETER (or similar) the
// parent is already gone — that's a no-op success, not an error,
// because the rename can proceed safely.
func waitForParentExit(parentPID int, timeout time.Duration) error {
	if parentPID <= 0 {
		return fmt.Errorf("invalid parent pid: %d", parentPID)
	}
	const access = windows.SYNCHRONIZE | windows.PROCESS_QUERY_LIMITED_INFORMATION
	h, err := windows.OpenProcess(access, false, uint32(parentPID))
	if err != nil {
		// ERROR_INVALID_PARAMETER (PID not found) means the process is
		// already gone. Treat as success.
		if isProcessGoneError(err) {
			return nil
		}
		return fmt.Errorf("OpenProcess(%d): %w", parentPID, err)
	}
	defer windows.CloseHandle(h)

	ms := uint32(timeout / time.Millisecond)
	if ms == 0 {
		ms = 1
	}
	rc, err := windows.WaitForSingleObject(h, ms)
	if err != nil {
		return fmt.Errorf("WaitForSingleObject: %w", err)
	}
	switch rc {
	case windows.WAIT_OBJECT_0:
		return nil
	case uint32(windows.WAIT_TIMEOUT):
		return fmt.Errorf("timed out after %s waiting for pid %d to exit", timeout, parentPID)
	default:
		return fmt.Errorf("WaitForSingleObject returned 0x%x", rc)
	}
}

// isProcessGoneError reports whether the OpenProcess error indicates
// the target PID is no longer running. The two common codes are:
//
//   - ERROR_INVALID_PARAMETER (87) — PID not in the process table.
//   - ERROR_ACCESS_DENIED (5) — also returned for some terminated
//     processes; treat as "gone enough" so the swap proceeds.
func isProcessGoneError(err error) bool {
	if err == nil {
		return false
	}
	if errno, ok := err.(windows.Errno); ok {
		switch errno {
		case windows.ERROR_INVALID_PARAMETER, windows.ERROR_ACCESS_DENIED:
			return true
		}
	}
	return false
}
