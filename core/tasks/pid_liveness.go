//go:build !windows

package tasks

import (
	"os"
	"syscall"
)

// IsProcessAlive returns true when the process with the given PID exists
// and has not yet exited. On POSIX systems this sends signal 0 (a null
// signal) which succeeds only when the process is reachable.
//
// On Windows os.FindProcess always succeeds; callers that need real
// liveness on Windows would need to open the handle with PROCESS_QUERY_LIMITED_INFORMATION —
// that path is deferred. For now Windows returns true (conservative:
// boot-time MarkCrashed marks unknown-PID rows anyway).
//
// pid==0 means the PID was never recorded (sub-agent tasks); we return
// false so those rows are always marked crashed at boot (safe: sub-agents
// are in-process and do not survive a harness restart).
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 does not kill the process; it probes reachability.
	err = p.Signal(syscall.Signal(0))
	return err == nil
}
