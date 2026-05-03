package bash

import (
	"path/filepath"
	"strings"
)

// dangerousBasenames is the set of argv[0] basenames classified as
// dangerous-tier (spec §2 + spec FR-015). Commands in this set never
// persist an "Allow always" Cedar policy without
// Settings.PermissionCacheDangerousOps=true.
var dangerousBasenames = map[string]struct{}{
	"rm":       {},
	"sudo":     {},
	"chmod":    {},
	"chown":    {},
	"dd":       {},
	"mkfs":     {},
	"kill":     {},
	"killall":  {},
	"pkill":    {},
	"shutdown": {},
	"reboot":   {},
	"mv":       {},
	"cp":       {},
	// curl and wget are dangerous in the pipe-to-shell pattern. We flag
	// them regardless of whether a pipe is detectable in argv because
	// the parser runs without a shell (pipes are not shell-expanded). The
	// user who wants curl for safe use cases clicks Allow once, which is
	// appropriate given how common curl-piped-to-sh attacks are.
	"curl": {},
	"wget": {},
}

// IsDangerous reports whether an argv represents a dangerous command
// and returns a human-readable reason string if so.
//
// Dangerous classification is based on argv[0] basename only. We do not
// inspect further arguments (e.g. we do not distinguish "rm file" from
// "rm -rf /") because the Cedar gate operates at the derived-pattern
// level, not individual argument level (spec FR-014, §9 out-of-scope).
//
// Returns (false, "") for safe commands.
// Returns (true, reason) for dangerous commands. The reason is the
// one-line modal copy from DangerousCopy.
func IsDangerous(argv []string) (bool, string) {
	if len(argv) == 0 {
		return false, ""
	}
	rawBase := filepath.Base(argv[0])

	// Exact match first.
	if _, ok := dangerousBasenames[rawBase]; ok {
		return true, DangerousCopy(rawBase)
	}

	// Strip a single file extension (handles "mkfs.ext4", "rm.exe" on
	// Windows, etc.) and check again. This also covers "mkfs.*" variants
	// which are all dangerous filesystem formatters.
	if ext := filepath.Ext(rawBase); ext != "" {
		stripped := strings.TrimSuffix(rawBase, ext)
		if _, ok := dangerousBasenames[stripped]; ok {
			// Use the stripped name for copy lookup so "mkfs" → mkfs copy.
			return true, DangerousCopy(stripped)
		}
	}

	return false, ""
}
