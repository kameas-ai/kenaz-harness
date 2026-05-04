package bootswap

import (
	"fmt"
	"os"
	"os/exec"
)

// osRelauncher is the production Relauncher: fork-execs target,
// terminates the current process. Returns only on Cmd.Start error;
// the os.Exit(0) on the success path is unreachable.
type osRelauncher struct{}

// NewOSRelauncher returns the production Relauncher.
func NewOSRelauncher() Relauncher { return osRelauncher{} }

func (osRelauncher) Relaunch(target string, args []string) error {
	cmd := exec.Command(target, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("relaunch start %s: %w", target, err)
	}
	os.Exit(0)
	return nil // unreachable
}
