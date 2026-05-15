package localruntime

import (
	"os"
	"strings"
)

// Enabled reports whether the local-runtimes subsystem is active.
// Set HARNESS_LOCAL_RUNTIMES=0 to disable; any other value (or unset)
// enables the feature.
func Enabled() bool {
	v := strings.TrimSpace(os.Getenv("HARNESS_LOCAL_RUNTIMES"))
	return v != "0"
}
