//go:build windows

package tasks

// IsProcessAlive returns true conservatively on Windows. A real PID
// liveness check on Windows requires opening the process handle with
// PROCESS_QUERY_LIMITED_INFORMATION — that path is deferred. Boot-time
// MarkCrashed marks unknown-PID (pid==0) rows regardless, so sub-agent
// tasks are still cleaned up correctly.
func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return true // conservative on Windows
}
