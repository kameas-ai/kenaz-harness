//go:build windows

package tasks

import "os"

// sigterm on Windows sends os.Interrupt (Ctrl+C), which is the closest
// equivalent to SIGTERM since Windows does not have SIGTERM.
func sigterm(p *os.Process) error {
	return p.Signal(os.Interrupt)
}
