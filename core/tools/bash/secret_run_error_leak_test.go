package bash_test

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// TestSyncRunError_DoesNotLogResolvedSecret is the falsification test for
// the seventh @secret: egress (release/v0.72.0 blocking finding 1):
//
//   - core/tools/bash/exec.go:118 sets `label := opts.CommandLine` — the
//     RESOLVED, post-substitution command line — and :128 wraps it into
//     `fmt.Errorf("bash: run %q: %w", label, err)`.
//   - core/tools/bash/bash.go:468 then does
//     `t.logf("bash.run_error", "err", runErr.Error())`, THREE LINES
//     BEFORE the WP08 sanitizer runs at :470-477. The tool result is
//     redacted (rawStderr, which already has runErr.Error() folded in at
//     :464, is what gets sanitized); the log record is not.
//
// This test drives the deterministic trigger the finding names: SHELL set
// to an absolute path that does not exist. exec.go:88-93 checks
// filepath.IsAbs but never existence, so cmd.Run() returns a plain
// *exec.Error (not *exec.ExitError) on every single invocation — no
// timing race, no context-cancellation window needed.
//
// setupSecretResolverCtx and syncBuffer are shared with
// secret_background_leak_test.go (same package, same leakSentinel
// "SUPERSECRET-PLAINTEXT-123" the reviewer's reproduction used).
func TestSyncRunError_DoesNotLogResolvedSecret(t *testing.T) {
	nonexistentShell := filepath.Join(t.TempDir(), "no-such-shell-zz72f1")
	t.Setenv("SHELL", nonexistentShell)

	ctx := setupSecretResolverCtx(t)

	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	tool := bash.New(bash.Options{
		SandboxRoot: "/tmp",
		Allowlist:   []string{"echo"},
		Logger:      logger,
	})

	argsJSON, _ := json.Marshal(map[string]any{
		"command": "echo @secret:user:tok",
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var out struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ExitCode == 0 {
		t.Fatalf("setup broken: expected a nonzero/negative exit code from a missing $SHELL, got 0 (out=%+v)", out)
	}
	if !strings.Contains(out.Stderr, "no-such-shell-zz72f1") {
		t.Fatalf("setup broken: stderr does not mention the missing-shell failure at all — the non-ExitError branch may not have been reached: %q", out.Stderr)
	}

	// Half 1 (must stay true both before and after the fix): the tool
	// result stays redacted — WP08's pre-existing contract. Proves the
	// fix cannot be "stop logging entirely" while leaving the result path
	// broken, and pins that the redaction on the RESULT already worked
	// before this fix (rawStderr is sanitized at bash.go:470-477,
	// downstream of where runErr.Error() was folded in at :464).
	if strings.Contains(out.Stderr, leakSentinel) {
		t.Fatalf("tool result stderr carries resolved plaintext: %q", out.Stderr)
	}

	// Half 2 (the actual finding): the emitted slog record — a REAL
	// *slog.Logger with a captured handler, not a stub's recorded
	// argument — must not carry the plaintext either.
	logged := logBuf.String()
	if strings.Contains(logged, leakSentinel) {
		t.Fatalf("LEAK: bash.run_error log record carries resolved plaintext: %s", logged)
	}
}
