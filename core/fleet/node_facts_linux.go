//go:build linux

package fleet

import "os"

// detectOSVersion renders /etc/os-release as "<NAME> <VERSION_ID>"
// ("NixOS 26.05", "Ubuntu 24.04"). Falls back to "Linux".
func detectOSVersion() string {
	for _, p := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v := osReleaseVersion(string(b)); v != "" {
			return v
		}
	}
	return "Linux"
}
