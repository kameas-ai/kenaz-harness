//go:build !darwin && !linux && !windows

package fleet

import "runtime"

// detectOSVersion on an unsupported platform reports only the GOOS name.
func detectOSVersion() string { return runtime.GOOS }
