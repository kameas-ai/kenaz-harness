//go:build unix

package mlsidecar

import (
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own process group so a terminal
// Ctrl-C or the harness's own group-kill does not reach the shared engine.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
