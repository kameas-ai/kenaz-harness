package bash

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func resolveOrSkip(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("missing %q on PATH: %v", name, err)
	}
	return p
}

func TestRunEchoCapturesStdout(t *testing.T) {
	t.Parallel()
	echo := resolveOrSkip(t, "echo")
	res, err := Run(context.Background(), RunOpts{
		Argv: []string{echo, "hello"},
		Cwd:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if got := strings.TrimSpace(string(res.Stdout)); got != "hello" {
		t.Errorf("Stdout = %q, want %q", got, "hello")
	}
	if res.Truncated {
		t.Errorf("Truncated = true, want false on small output")
	}
}

func TestRunFalseExitsNonZero(t *testing.T) {
	t.Parallel()
	falseBin := resolveOrSkip(t, "false")
	res, err := Run(context.Background(), RunOpts{
		Argv: []string{falseBin},
		Cwd:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero")
	}
}

func TestRunOutputCapTruncates(t *testing.T) {
	t.Parallel()
	// Use yes piped through head — but we don't have shell. Use
	// `head -c 102400 /dev/zero` then translate to printable. The
	// simplest cross-platform approach is to spawn `python3` if
	// present and let it print 100 KiB to stdout.
	py := resolveOrSkip(t, "python3")
	res, err := Run(context.Background(), RunOpts{
		Argv:           []string{py, "-c", "import sys; sys.stdout.write('x' * 102400)"},
		Cwd:            t.TempDir(),
		MaxOutputBytes: DefaultMaxOutputBytes,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if got := len(res.Stdout); got != DefaultMaxOutputBytes {
		t.Errorf("len(Stdout) = %d, want %d", got, DefaultMaxOutputBytes)
	}
}

func TestRunOutputCapUnderLimitNoTruncation(t *testing.T) {
	t.Parallel()
	echo := resolveOrSkip(t, "echo")
	res, err := Run(context.Background(), RunOpts{
		Argv:           []string{echo, "small"},
		Cwd:            t.TempDir(),
		MaxOutputBytes: 4096,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Truncated {
		t.Errorf("Truncated = true, want false")
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	t.Parallel()
	sleep := resolveOrSkip(t, "sleep")
	start := time.Now()
	res, _ := Run(context.Background(), RunOpts{
		Argv:    []string{sleep, "5"},
		Cwd:     t.TempDir(),
		Timeout: 100 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Errorf("Run elapsed = %v, expected timeout near 100ms", elapsed)
	}
	// Process killed by signal: exit code is non-zero or the run
	// returned an error wrapping context.DeadlineExceeded.
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0 after timeout, want non-zero")
	}
}

func TestRunCwdHonored(t *testing.T) {
	t.Parallel()
	pwd := resolveOrSkip(t, "pwd")
	dir := t.TempDir()
	res, err := Run(context.Background(), RunOpts{
		Argv: []string{pwd},
		Cwd:  dir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := strings.TrimSpace(string(res.Stdout))
	// On macOS t.TempDir() lives under /var/folders which symlinks
	// to /private/var/folders. pwd may print either form. Match by
	// HasSuffix on the leaf component and presence of the temp dir
	// basename in the result.
	if !strings.Contains(got, dir) && !strings.Contains(dir, got) {
		t.Errorf("pwd = %q, want stdout under %q", got, dir)
	}
}

func TestRunEmptyArgvErrors(t *testing.T) {
	t.Parallel()
	_, err := Run(context.Background(), RunOpts{
		Argv: nil,
		Cwd:  t.TempDir(),
	})
	if err == nil {
		t.Errorf("Run(nil argv) err = nil, want non-nil")
	}
}

func TestRunMissingExecutableReturnsError(t *testing.T) {
	t.Parallel()
	res, err := Run(context.Background(), RunOpts{
		Argv: []string{"/nonexistent/binary/xyz/abc/foo"},
		Cwd:  t.TempDir(),
	})
	if err == nil {
		t.Errorf("Run(missing) err = nil, want non-nil")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
}

func TestCapWriterBoundary(t *testing.T) {
	t.Parallel()
	w := &capWriter{max: 10}
	n, err := w.Write([]byte("abcdefghij"))
	if err != nil || n != 10 {
		t.Fatalf("Write1 returned (%d, %v), want (10, nil)", n, err)
	}
	if w.truncated {
		t.Errorf("truncated = true at exact boundary, want false")
	}
	n, err = w.Write([]byte("k"))
	if err != nil || n != 1 {
		t.Fatalf("Write2 returned (%d, %v), want (1, nil)", n, err)
	}
	if !w.truncated {
		t.Errorf("truncated = false after overflow, want true")
	}
	if got := string(w.bytes()); got != "abcdefghij" {
		t.Errorf("bytes = %q, want %q", got, "abcdefghij")
	}
}

func TestCapWriterLargeWriteSplit(t *testing.T) {
	t.Parallel()
	w := &capWriter{max: 5}
	n, err := w.Write([]byte("abcdefghij"))
	if err != nil || n != 10 {
		t.Fatalf("Write returned (%d, %v), want (10, nil)", n, err)
	}
	if !w.truncated {
		t.Errorf("truncated = false after split write, want true")
	}
	if got := string(w.bytes()); got != "abcde" {
		t.Errorf("bytes = %q, want %q", got, "abcde")
	}
}
