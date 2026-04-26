package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// DefaultMaxOutputBytes caps each captured stream to 64 KiB per
// FR-011's "truncated flag" semantics. Writes beyond the cap are
// dropped and the truncated flag is set; the model sees the FIRST N
// bytes of output, which keeps error messages (typically printed
// up-front) intact.
const DefaultMaxOutputBytes = 64 * 1024

// RunOpts carries the parameters Run needs to execute a single
// command. Argv must already be allowlisted by the caller; Run does
// not re-check the allowlist (NFR-005 keeps that gate at the bash.go
// layer, before LookPath, so a planted binary cannot slip past).
type RunOpts struct {
	// Argv is the program (Argv[0]) plus arguments. Argv[0] should
	// be a name suitable for exec.LookPath OR an absolute path
	// already resolved by the caller.
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

// Run executes Opts.Argv with timeout + output cap. Stdout and stderr
// are captured into capWriter buffers that drop bytes past
// MaxOutputBytes (first-N strategy). The returned RunResult.Truncated
// is true iff either stream hit its cap.
//
// Run never spawns a shell — it dispatches argv directly via
// exec.CommandContext. ExitCode reflects the OS exit status: 0 on
// success, the program's status on failure, or -1 if the process
// could not be started (cmd.Start error).
func Run(ctx context.Context, opts RunOpts) (RunResult, error) {
	if len(opts.Argv) == 0 {
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

	cmd := exec.CommandContext(ctx, opts.Argv[0], opts.Argv[1:]...)
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
			// Includes context-cancellation, LookPath miss when
			// CommandContext defers to PATH lookup, etc. Caller
			// surfaces these as failed runs with exit -1.
			return RunResult{
				Stdout:    stdout.bytes(),
				Stderr:    stderr.bytes(),
				ExitCode:  -1,
				Truncated: stdout.truncated || stderr.truncated,
				Duration:  duration,
			}, fmt.Errorf("bash: run %q: %w", opts.Argv[0], err)
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
