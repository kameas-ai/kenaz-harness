package bash

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DefaultMaxOutputBytes caps each captured stream to 64 KiB per
// FR-011's "truncated flag" semantics. Writes beyond the cap are
// dropped and the truncated flag is set; the model sees the FIRST N
// bytes of output, which keeps error messages (typically printed
// up-front) intact.
const DefaultMaxOutputBytes = 64 * 1024

// RunOpts carries the parameters Run needs to execute a single
// command. When CommandLine is non-empty it is passed verbatim to
// `bash -l -c`; Argv is ignored for execution but is still accepted
// for callers that need it (unused in the shell-mode path).
// The Cedar gate in bash.go gates on argv from FirstSegmentArgv before
// Run is called, so NFR-005 is upheld at the layer above.
type RunOpts struct {
	// CommandLine is the raw shell command string. When non-empty Run
	// dispatches via `$SHELL -l -c CommandLine` (shell mode). When
	// empty Run falls back to direct argv exec via Argv (legacy path,
	// used by unit tests that drive Run directly).
	CommandLine string
	// LogCommandLine is the PRE-substitution command line to use for
	// anything that leaves the process (error labels, logs) instead of
	// CommandLine. When CommandLine came from @secret: reference
	// substitution, it carries resolved plaintext; the caller-supplied
	// error wrapper in Run must never fold that plaintext into the
	// returned error's message, because the model-facing tool result IS
	// sanitized before it reaches the caller but the harness's own
	// t.logf(...) call is not (the seventh @secret: egress finding,
	// release/v0.72.0). Empty falls back to CommandLine, so callers that
	// never resolve a secret (or drive the legacy Argv path) see no
	// behaviour change — this mirrors the execCommand/logCommand split
	// background.go already uses for the exact same reason.
	LogCommandLine string
	// Argv is the direct-exec fallback used when CommandLine is empty.
	// Argv[0] must be an absolute path already resolved by the caller.
	Argv []string
	// Cwd is the absolute working directory. Caller validates that
	// Cwd lies under the sandbox root (FR-013).
	Cwd string
	// Timeout caps the total wall-clock runtime. Zero means use the
	// caller-supplied context's deadline; a non-zero value layers
	// an additional context.WithTimeout over ctx.
	Timeout time.Duration
	// MaxOutputBytes caps each of stdout and stderr independently.
	// Zero falls back to DefaultMaxOutputBytes.
	MaxOutputBytes int
	// Env, when non-nil, fully replaces the child's environment.
	// Nil inherits the parent process environment.
	Env []string
}

// RunResult is the structured return of a single command execution.
type RunResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
	Duration  time.Duration
}

// Run executes a command with timeout + output cap. Stdout and stderr
// are captured into capWriter buffers that drop bytes past
// MaxOutputBytes (first-N strategy). The returned RunResult.Truncated
// is true iff either stream hit its cap.
//
// When opts.CommandLine is non-empty the command is dispatched via
// `$SHELL -l -c <CommandLine>` so pipes, redirects, variable
// expansion, globbing, and command substitution all work.
// When opts.CommandLine is empty Run falls back to direct argv exec
// (legacy path; used by unit tests that drive Run directly).
//
// ExitCode reflects the OS exit status: 0 on success, the program's
// exit status on failure, or -1 if the process could not be started.
func Run(ctx context.Context, opts RunOpts) (RunResult, error) {
	if opts.CommandLine == "" && len(opts.Argv) == 0 {
		return RunResult{}, errors.New("bash: Run: empty argv")
	}
	maxBytes := opts.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxOutputBytes
	}

	cancel := func() {}
	if opts.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
	}
	defer cancel()

	var cmd *exec.Cmd
	if opts.CommandLine != "" {
		shell := os.Getenv("SHELL")
		if shell == "" || !filepath.IsAbs(shell) {
			shell = "/bin/bash"
		}
		cmd = exec.CommandContext(ctx, shell, "-l", "-c", opts.CommandLine)
	} else {
		cmd = exec.CommandContext(ctx, opts.Argv[0], opts.Argv[1:]...)
	}
	cmd.Dir = opts.Cwd
	if opts.Env != nil {
		cmd.Env = opts.Env
	}
	stdout := &capWriter{max: maxBytes}
	stderr := &capWriter{max: maxBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Includes context-cancellation, shell not found, etc.
			// Caller surfaces these as failed runs with exit -1.
			//
			// The label uses LogCommandLine (pre-substitution) in
			// preference to CommandLine (which may carry resolved
			// @secret: plaintext) — see the LogCommandLine doc comment.
			label := opts.LogCommandLine
			if label == "" {
				label = opts.CommandLine
			}
			if label == "" && len(opts.Argv) > 0 {
				label = opts.Argv[0]
			}
			return RunResult{
				Stdout:    stdout.bytes(),
				Stderr:    stderr.bytes(),
				ExitCode:  -1,
				Truncated: stdout.truncated || stderr.truncated,
				Duration:  duration,
			}, fmt.Errorf("bash: run %q: %w", label, err)
		}
	}

	return RunResult{
		Stdout:    stdout.bytes(),
		Stderr:    stderr.bytes(),
		ExitCode:  exitCode,
		Truncated: stdout.truncated || stderr.truncated,
		Duration:  duration,
	}, nil
}

// capWriter is an io.Writer that buffers up to max bytes and drops
// the remainder. It claims success on every Write so the producer
// (the child process) never sees a write error and keeps running —
// which matters when the child's output blocks on stdout pipe back-
// pressure.
type capWriter struct {
	buf       []byte
	max       int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	remaining := w.max - len(w.buf)
	if remaining <= 0 {
		w.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		w.buf = append(w.buf, p[:remaining]...)
		w.truncated = true
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *capWriter) bytes() []byte {
	if len(w.buf) == 0 {
		return nil
	}
	out := make([]byte, len(w.buf))
	copy(out, w.buf)
	return out
}
