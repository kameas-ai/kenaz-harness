// Package main is the kenaz-updater helper binary, a stand-alone exe
// that runs OUTSIDE the main harness process to perform a true
// auto-restart on Windows. The main app cannot rename its own running
// .exe (Windows holds an exclusive lock until the process exits), so
// it spawns this helper, exits, and the helper waits, swaps, and
// relaunches.
//
// Auto-update mission WP01 (Windows helper updater).
//
// # Wire format
//
// Invoked from core/update/swap_windows.go with these flags:
//
//	kenaz-updater.exe \
//	    --parent-pid <PID>       \
//	    --staged   <abspath>     \
//	    --target   <abspath>     \
//	    --sha256   <hex>         \
//	    --launch-args ARG1 --launch-args ARG2 ...
//
// All flags except --launch-args are required. --launch-args may
// repeat zero or more times; each value is forwarded verbatim to the
// relaunched binary as argv[1..].
//
// # Lifecycle
//
//  1. Open parent process by PID, wait up to 30s for it to exit.
//  2. Re-verify sha256 of --staged (defense in depth — paranoid TOCTOU).
//  3. Atomic os.Rename(--staged, --target). Windows allows this only
//     after the parent process has fully released its file lock.
//  4. exec.Cmd{...}.Start() the new --target — Start, not Run, so the
//     updater can exit cleanly without hanging on the child.
//  5. Exit 0.
//
// # Logging
//
// Every step appends to <DataDir>/update/updater.log (DataDir is
// derived from the parent --target's directory layout: the harness
// always lives next to its data dir on Windows installs).
//
// The log is capped at ~64KB; on rotation we truncate to the last 32KB
// so a stuck-in-a-loop helper doesn't fill the disk.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// version is overwritten at link-time via -ldflags="-X main.version=...".
// "dev" is the source-tree default so a local `go build` produces a
// runnable artifact.
var version = "dev"

// stringList is a flag.Value that accumulates repeated --launch-args
// occurrences into a slice. flag.StringVar would only keep the last
// value; we need every argument.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, " ") }

func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// args is the parsed command-line, kept in a struct so run() can be
// called from tests with a synthetic invocation.
type args struct {
	parentPID  int
	stagedPath string
	targetPath string
	sha256Hex  string
	launchArgs []string
	dataDir    string // optional override; resolved when empty
	timeout    time.Duration
}

// parseArgs consumes the supplied argv (NOT including the program
// name) and returns the populated args struct or an error suitable for
// stderr.
func parseArgs(argv []string) (args, error) {
	fs := flag.NewFlagSet("kenaz-updater", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // we own the error reporting
	var (
		a   args
		la  stringList
		ver bool
	)
	fs.IntVar(&a.parentPID, "parent-pid", 0, "PID of the harness process to wait for")
	fs.StringVar(&a.stagedPath, "staged", "", "absolute path to the verified replacement binary")
	fs.StringVar(&a.targetPath, "target", "", "absolute path to the running binary to overwrite")
	fs.StringVar(&a.sha256Hex, "sha256", "", "expected sha256 of --staged (hex)")
	fs.StringVar(&a.dataDir, "data-dir", "", "harness data dir (for updater.log); empty = derive from --target")
	fs.DurationVar(&a.timeout, "wait-timeout", 30*time.Second, "max time to wait for parent process to exit")
	fs.Var(&la, "launch-args", "argument forwarded to the relaunched binary (repeatable)")
	fs.BoolVar(&ver, "version", false, "print version and exit")
	if err := fs.Parse(argv); err != nil {
		return args{}, fmt.Errorf("parse args: %w", err)
	}
	if ver {
		return args{}, errVersionRequested
	}
	if a.parentPID <= 0 {
		return args{}, errors.New("--parent-pid required (positive integer)")
	}
	if a.stagedPath == "" {
		return args{}, errors.New("--staged required")
	}
	if a.targetPath == "" {
		return args{}, errors.New("--target required")
	}
	if a.sha256Hex == "" {
		return args{}, errors.New("--sha256 required")
	}
	a.launchArgs = []string(la)
	if a.timeout <= 0 {
		a.timeout = 30 * time.Second
	}
	return a, nil
}

// errVersionRequested is the sentinel parseArgs returns when the
// caller passes --version. main() prints + exits 0 without doing any
// other work.
var errVersionRequested = errors.New("version requested")

// verifyFileSha256 hashes the file at path and returns an error if it
// does not match expected (case-insensitive). Mirrors the verifier in
// core/update so the helper has no dep on the parent module.
func verifyFileSha256(path, expected string) error {
	if expected == "" {
		return errors.New("empty expected sha256")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash %s: %w", path, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s want %s", got, expected)
	}
	return nil
}

// resolveDataDir picks the directory for updater.log. If the caller
// passed --data-dir we trust it; otherwise we use the parent of
// --target. Either way the "<dir>/update/" subdir is created if it
// doesn't exist (Windows installs with the harness under
// %LOCALAPPDATA%\Programs\Kenaz\).
func resolveDataDir(a args) string {
	if a.dataDir != "" {
		return a.dataDir
	}
	return filepath.Dir(a.targetPath)
}

// updaterLog is the on-disk log writer, append-only, with a soft cap.
type updaterLog struct {
	path string
}

func newUpdaterLog(dataDir string) (*updaterLog, error) {
	dir := filepath.Join(dataDir, "update")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return &updaterLog{path: filepath.Join(dir, "updater.log")}, nil
}

// write appends a structured line to the log. Format:
//
//	<RFC3339> <level> <event> key=val key=val ...
//
// Best-effort: a write failure is silently dropped (we can't write
// anywhere else anyway). The log is rotated when it exceeds 64KB.
func (l *updaterLog) write(level, event string, kv ...any) {
	if l == nil || l.path == "" {
		return
	}
	if err := l.maybeRotate(); err != nil {
		// best-effort
		_ = err
	}
	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" ")
	b.WriteString(level)
	b.WriteString(" ")
	b.WriteString(event)
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
	}
	b.WriteString("\n")
	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(b.String())
	_ = f.Close()
}

// maybeRotate truncates the log to the last 32KB if it exceeds 64KB.
// Cheap O(file size) read; the log is bounded by the cap so this is
// fast in steady state.
func (l *updaterLog) maybeRotate() error {
	const maxBytes = 64 * 1024
	const keep = 32 * 1024
	st, err := os.Stat(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if st.Size() < maxBytes {
		return nil
	}
	body, err := os.ReadFile(l.path)
	if err != nil {
		return err
	}
	if len(body) <= keep {
		return nil
	}
	tail := body[len(body)-keep:]
	// Drop a partial leading line so the rotated file starts at a
	// clean newline boundary.
	if i := strings.IndexByte(string(tail), '\n'); i >= 0 && i+1 < len(tail) {
		tail = tail[i+1:]
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, tail, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

// run is the testable entrypoint; main() wraps it. Returns an exit
// code: 0 success, 1 any failure. The error text (if any) is written
// to stderr by the caller for surfacing under `kenaz-updater --version
// 2>&1` style invocations.
func run(argv []string, stderr io.Writer) int {
	a, err := parseArgs(argv)
	if errors.Is(err, errVersionRequested) {
		fmt.Fprintf(stderr, "kenaz-updater %s\n", version)
		return 0
	}
	if err != nil {
		fmt.Fprintln(stderr, "kenaz-updater:", err.Error())
		fmt.Fprintln(stderr, "usage: kenaz-updater --parent-pid <PID> --staged <path> --target <path> --sha256 <hex> [--launch-args ARG]...")
		return 1
	}

	logger, lerr := newUpdaterLog(resolveDataDir(a))
	if lerr != nil {
		// Logging is best-effort; press on without it. The caller's
		// stderr still surfaces the error.
		fmt.Fprintln(stderr, "kenaz-updater: log dir:", lerr.Error())
	}
	logger.write("info", "start",
		"version", version,
		"parent_pid", a.parentPID,
		"target", a.targetPath,
		"goos", runtime.GOOS,
	)

	// Step 1: wait for the parent process to exit. On Windows this is
	// the WaitForSingleObject path (in main_windows.go); on other
	// platforms the build tag selects a stub that no-ops.
	if err := waitForParentExit(a.parentPID, a.timeout); err != nil {
		logger.write("warn", "parent_wait_failed", "err", err.Error())
		fmt.Fprintln(stderr, "kenaz-updater: wait for parent:", err.Error())
		return 1
	}
	logger.write("info", "parent_exited")

	// Step 2: re-verify sha256.
	if err := verifyFileSha256(a.stagedPath, a.sha256Hex); err != nil {
		logger.write("warn", "sha_mismatch", "err", err.Error())
		// Delete the staged file so a future helper invocation doesn't
		// keep tripping on the same corrupt artifact.
		_ = os.Remove(a.stagedPath)
		fmt.Fprintln(stderr, "kenaz-updater:", err.Error())
		return 1
	}
	logger.write("info", "sha_verified")

	// Step 3: atomic rename. On Windows os.Rename uses MoveFileEx with
	// MOVEFILE_REPLACE_EXISTING semantics — succeeds because the
	// target is no longer locked.
	if err := os.Rename(a.stagedPath, a.targetPath); err != nil {
		logger.write("warn", "rename_failed", "err", err.Error())
		fmt.Fprintln(stderr, "kenaz-updater: rename:", err.Error())
		return 1
	}
	logger.write("info", "rename_ok", "target", a.targetPath)

	// Step 4: launch the new binary. Use Start, not Run — we exit
	// immediately and don't wait on the child.
	cmd := exec.Command(a.targetPath, a.launchArgs...)
	if err := cmd.Start(); err != nil {
		logger.write("warn", "launch_failed", "err", err.Error())
		fmt.Fprintln(stderr, "kenaz-updater: launch:", err.Error())
		return 1
	}
	logger.write("info", "launched", "pid", cmd.Process.Pid)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}
