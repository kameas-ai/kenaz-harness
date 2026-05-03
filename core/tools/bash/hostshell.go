package bash

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CaptureLoginShellPath spawns the user's login shell ($SHELL) with
// `-l -c 'echo $PATH'` and returns the trimmed PATH the shell exports
// after sourcing the user's profile (~/.zprofile, ~/.bash_profile, etc.).
//
// Why: macOS app-bundle launches give a stripped PATH
// (`/usr/bin:/bin:/usr/sbin:/sbin`) so binaries installed by Homebrew at
// /opt/homebrew/bin or by user package managers under ~/.local/bin are
// invisible to exec.LookPath. Capturing the login PATH and prepending it
// lets the bash tool reach the user's actual setup while keeping the
// child-process spawn surface (direct argv exec) unchanged — no shell
// pipeline, so no metacharacter-injection surface.
//
// Returns the trimmed PATH on success, or an error if $SHELL is unset,
// the spawn fails, or the captured PATH is empty.
func CaptureLoginShellPath(ctx context.Context) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "", fmt.Errorf("bash: $SHELL unset; cannot capture login PATH")
	}
	if _, statErr := os.Stat(shell); statErr != nil {
		return "", fmt.Errorf("bash: $SHELL %q not executable: %w", shell, statErr)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", "printf %s \"$PATH\"")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("bash: capture login PATH from %s: %w", shell, err)
	}
	captured := strings.TrimSpace(string(out))
	if captured == "" {
		return "", fmt.Errorf("bash: login shell returned empty PATH")
	}
	return captured, nil
}

// AugmentProcessPATH prepends the login-shell PATH to the current
// process's PATH env var. Idempotent: if the captured prefix is already
// present, the call is a no-op.
//
// Process-wide mutation is intentional — exec.LookPath consults
// os.Getenv("PATH") and there is no public per-Cmd override. Child
// processes spawned via exec.CommandContext inherit os.Environ() by
// default, so a single PATH change covers both the LookPath() call site
// and the spawned binary's runtime PATH.
func AugmentProcessPATH(captured string) (changed bool, finalPath string) {
	current := os.Getenv("PATH")
	if captured == "" {
		return false, current
	}
	if current == "" {
		_ = os.Setenv("PATH", captured)
		return true, captured
	}
	// Already prepended? Idempotent.
	if strings.HasPrefix(current, captured+":") || current == captured {
		return false, current
	}
	merged := captured + ":" + current
	_ = os.Setenv("PATH", merged)
	return true, merged
}
