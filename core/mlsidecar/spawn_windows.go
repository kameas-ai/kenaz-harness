//go:build windows

package mlsidecar

import "os/exec"

// detachProcess is a no-op on Windows: there is no kenaz-ml freeze target
// there (design §6.3), so this only has to compile.
func detachProcess(cmd *exec.Cmd) { _ = cmd }
