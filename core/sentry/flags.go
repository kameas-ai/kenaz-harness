package sentry

import (
	"os"
	"strings"
)

// Disabled reports whether the kill-switch environment variable
// HARNESS_SENTRY_DISABLED is set to a truthy value. When true, Init returns
// a nopClient regardless of settings.
//
// Truthy values: "1", "true", "yes", "on" (case-insensitive). Any other
// non-empty value is also treated as truthy so operators can set
// HARNESS_SENTRY_DISABLED=disabled without surprise.
func Disabled() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("HARNESS_SENTRY_DISABLED")))
	if v == "" {
		return false
	}
	switch v {
	case "0", "false", "no", "off":
		return false
	}
	return true
}
