//go:build darwin

package fleet

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// detectOSVersion returns "macOS <productVersion>" via sw_vers. No hostname,
// no build number.
func detectOSVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "macOS"
	}
	return "macOS " + v
}
