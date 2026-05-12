// flags.go — feature-flag gate for user-defined slash commands.
//
// The HARNESS_USER_SLASHCMD environment variable controls whether the
// user-slash-commands surface is enabled. Default: on.
//
// Values that disable the surface: "0", "false" (case-insensitive).
// Any other non-empty string (including "1", "true", "yes") is treated
// as enabled, matching the HARNESS_BRANCH_ADVISOR convention.
//
// When the flag is off:
//   - Dispatch.Run returns ErrFeatureDisabled for user commands.
//   - The RPC layer (slashview.API) gates UserSave / UserRun via IsEnabled.
//   - Built-in commands (/help, /model, /branch, etc.) are unaffected —
//     they bypass Dispatch.Run entirely.
//
// user-slash-commands-01KQ8TD9 WP09.
package slashcmd

import (
	"errors"
	"os"
	"strings"
)

// ErrFeatureDisabled is returned by Dispatch.Run when
// HARNESS_USER_SLASHCMD=false is set.
var ErrFeatureDisabled = errors.New("slashcmd: user slash commands are disabled (HARNESS_USER_SLASHCMD=false)")

// UserSlashcmdEnabled reports whether the user-defined slash command
// surface is enabled. Default: true.
//
// Flip to false by setting HARNESS_USER_SLASHCMD=0 or
// HARNESS_USER_SLASHCMD=false (case-insensitive).
func UserSlashcmdEnabled() bool {
	v := os.Getenv("HARNESS_USER_SLASHCMD")
	if v == "" {
		return true // default ON
	}
	lower := strings.ToLower(strings.TrimSpace(v))
	return lower != "0" && lower != "false"
}
