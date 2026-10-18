//go:build windows

package mlsidecar

import "os"

// processAlive on Windows: Windows has no kenaz-ml freeze target yet
// (design §6.3 / OQ-N8), so this only needs to compile for the harness's
// existing cross-platform CI build, not be exact. os.FindProcess opens
// a real handle on Windows (unlike Unix, where it never fails), so a
// failure here is at least a genuine signal; a success is treated as
// "alive" — conservative in the same direction as the Unix
// implementation's EPERM case.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
