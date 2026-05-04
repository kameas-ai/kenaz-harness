//go:build !windows

package main

import "syscall"

// syscallZero is the no-op probe signal used by the non-Windows
// waitForParentExit fallback. Sending signal 0 to a Unix process is
// the canonical "is this PID alive?" check — it returns ESRCH for
// dead PIDs without affecting live ones.
var syscallZero = syscall.Signal(0)
