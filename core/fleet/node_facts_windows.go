//go:build windows

package fleet

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// detectOSVersion returns "Windows <major>.<minor>.<build>" from
// RtlGetVersion (not spoofable by manifest compatibility shims).
func detectOSVersion() string {
	v := windows.RtlGetVersion()
	if v == nil {
		return "Windows"
	}
	return fmt.Sprintf("Windows %d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
}
