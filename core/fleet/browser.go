package fleet

import (
	"fmt"
	"os/exec"
	"runtime"
)

// openBrowserPlatform opens the given URL in the default system browser.
// Uses xdg-open on Linux, open on macOS, and start on Windows.
func openBrowserPlatform(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", rawURL)
	default:
		// Linux and other Unix.
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("fleet: open browser (%s): %w", runtime.GOOS, err)
	}
	// Do not Wait() — the browser is a detached process.
	return nil
}
