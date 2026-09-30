//go:build unix

package mlsidecar

import (
	"errors"
	"os"
	"syscall"
)

// processAlive reports whether pid identifies a live process, via the
// standard signal-0 liveness probe (design §3.7 R5: "leases are
// pid-liveness-validated at sweep"; "stale-lock breaking keyed on
// lock-holder pid liveness"). Sending signal 0 delivers no actual
// signal — it only performs the kernel's existence/permission checks.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		return false
	}
	// EPERM (exists, owned by another user) or any other error: treat as
	// alive — the safe default for a liveness check that decides whether
	// to break a lock. Silently breaking another user's live lock would
	// be a worse outcome than the rare unnecessary conservatism here.
	return true
}
