package fleet

import (
	"os"
	"strings"
)

// Disabled reports whether the kill-switch environment variable
// HARNESS_FLEET_DISABLED is set to a truthy value. When true, NewClient
// returns a NopClient regardless of settings.
//
// Truthy values: "1", "true", "yes" (case-insensitive). Any other non-empty
// value is also treated as truthy so operators can set
// HARNESS_FLEET_DISABLED=disabled without surprise.
//
// False values: "0", "false", "no".
func Disabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("HARNESS_FLEET_DISABLED")))
	if v == "" {
		return false
	}
	switch v {
	case "0", "false", "no":
		return false
	}
	return true
}
