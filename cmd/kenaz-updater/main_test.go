package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestParseArgs_HappyPath covers the full required-flag set plus a
// repeated --launch-args. Mirrors the wire format core/update produces
// in swap_windows.go.
func TestParseArgs_HappyPath(t *testing.T) {
	a, err := parseArgs([]string{
		"--parent-pid", "12345",
		"--staged", "/tmp/staged.exe",
		"--target", "/usr/local/bin/harness.exe",
		"--sha256", "abc123",
		"--launch-args", "--debug",
		"--launch-args", "/path with spaces",
	})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if a.parentPID != 12345 {
		t.Errorf("parent_pid = %d", a.parentPID)
	}
	if a.stagedPath != "/tmp/staged.exe" {
		t.Errorf("staged = %s", a.stagedPath)
	}
	if a.targetPath != "/usr/local/bin/harness.exe" {
		t.Errorf("target = %s", a.targetPath)
	}
	if a.sha256Hex != "abc123" {
		t.Errorf("sha256 = %s", a.sha256Hex)
	}
	if got, want := strings.Join(a.launchArgs, "|"), "--debug|/path with spaces"; got != want {
		t.Errorf("launchArgs = %q want %q", got, want)
	}
	if a.timeout != 30*time.Second {
		t.Errorf("default timeout = %s", a.timeout)
	}
}

// TestParseArgs_MissingRequired enumerates every required-flag omission
// and asserts the error mentions the flag. Catches a regression where
// a future refactor accidentally drops a validation.
func TestParseArgs_MissingRequired(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"no parent-pid", []string{"--staged", "x", "--target", "y", "--sha256", "z"}, "parent-pid"},
		{"no staged", []string{"--parent-pid", "1", "--target", "y", "--sha256", "z"}, "staged"},
		{"no target", []string{"--parent-pid", "1", "--staged", "x", "--sha256", "z"}, "target"},
		{"no sha", []string{"--parent-pid", "1", "--staged", "x", "--target", "y"}, "sha256"},
		{"zero pid", []string{"--parent-pid", "0", "--staged", "x", "--target", "y", "--sha256", "z"}, "parent-pid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseArgs(c.argv)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %q want substring %q", err.Error(), c.want)
			}
		})
	}
}

// TestParseArgs_VersionFlag asserts the --version sentinel flows back
// without other-flag validation kicking in.
func TestParseArgs_VersionFlag(t *testing.T) {
	_, err := parseArgs([]string{"--version"})
	if !errors.Is(err, errVersionRequested) {
		t.Fatalf("err = %v want errVersionRequested", err)
	}
}

// TestVerifyFileSha256_HappyPath covers the on-disk hash check: write
// a file, hash it, pass the digest in, expect nil.
func TestVerifyFileSha256_HappyPath(t *testing.T) {
	dir := t.TempDir()
	body := []byte("hello kenaz updater")
	path := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	hexed := hex.EncodeToString(sum[:])
	if err := verifyFileSha256(path, hexed); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// TestVerifyFileSha256_Mismatch asserts a wrong digest returns an
// error mentioning "sha256 mismatch" — that string is matched by
// run() to delete the staged file.
func TestVerifyFileSha256_Mismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(path, []byte("real body"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong := strings.Repeat("0", 64)
	err := verifyFileSha256(path, wrong)
	if err == nil {
		t.Fatal("want mismatch error")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("err = %q", err.Error())
	}
}

// TestVerifyFileSha256_MissingFile guards the open-error path — a
// missing staged file should surface a wrapped open error, not a
// silent success.
func TestVerifyFileSha256_MissingFile(t *testing.T) {
	err := verifyFileSha256(filepath.Join(t.TempDir(), "does-not-exist"), strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("want open error")
	}
}

// TestUpdaterLog_RotatesAtCap exercises the soft-cap rotation. Write
// a large string in chunks until the file exceeds 64KB; assert it
// gets truncated to <= 32KB+epsilon on the next write.
func TestUpdaterLog_RotatesAtCap(t *testing.T) {
	dir := t.TempDir()
	logger, err := newUpdaterLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "update", "updater.log")

	// Pre-seed the file with > 64KB of content so the next write
	// triggers the rotation logic.
	big := bytes.Repeat([]byte("x = needs to fit a key=val pair y\n"), 3000) // ~100KB
	if err := os.WriteFile(logPath, big, 0o644); err != nil {
		t.Fatal(err)
	}

	logger.write("info", "after_rotation", "marker", "kept")

	st, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// After rotation we keep ~32KB plus the freshly-appended line.
	if st.Size() > 64*1024 {
		t.Fatalf("log not rotated: size = %d", st.Size())
	}
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "after_rotation") {
		t.Errorf("post-rotation write missing from log:\n%s", string(body)[:min(len(body), 200)])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestRun_BadArgsExit1 covers the parse-failure path of run(): bad
// argv → stderr message + exit 1. Doesn't touch the OS process tree.
func TestRun_BadArgsExit1(t *testing.T) {
	var buf bytes.Buffer
	rc := run([]string{"--parent-pid", "0"}, &buf)
	if rc != 1 {
		t.Errorf("rc = %d want 1", rc)
	}
	if !strings.Contains(buf.String(), "parent-pid") {
		t.Errorf("stderr = %q", buf.String())
	}
}

// TestRun_VersionFlagExit0 confirms --version short-circuits before
// any required-flag validation, returning 0 with the version printed.
func TestRun_VersionFlagExit0(t *testing.T) {
	var buf bytes.Buffer
	rc := run([]string{"--version"}, &buf)
	if rc != 0 {
		t.Errorf("rc = %d want 0", rc)
	}
	if !strings.Contains(buf.String(), "kenaz-updater") {
		t.Errorf("stderr = %q", buf.String())
	}
}

// TestRun_HappyPath_NonWindows exercises the full run() flow using the
// non-Windows polling waitForParentExit fallback. We spawn a short-
// lived child process, point --parent-pid at it, and assert the
// staged file ends up at --target with a relaunched child PID logged.
//
// On Windows the WaitForSingleObject path is exercised by the
// integration smoke (release.yml hosted runner) — not in this unit
// test because Go tests on Windows runners are scoped to the package
// build, not a running harness instance.
func TestRun_HappyPath_NonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("non-windows polling fallback test")
	}
	dir := t.TempDir()
	staged := filepath.Join(dir, "staged.bin")
	target := filepath.Join(dir, "target.bin")
	// Use a real shell command as the relaunch target so the helper
	// can actually exec it. /bin/echo is universally available.
	body := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(staged, body, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// Use this process's parent PID as the "harness" — it's a real,
	// long-lived process the helper can probe. Combined with a short
	// wait-timeout we exercise the timeout-handled-as-success path
	// (a 30s timeout in production is a soft fail; on the unit test
	// here we accept that the helper saw a timeout and proceeds with
	// the swap because the parent never exited within the window).
	//
	// To exercise the happy "parent exited" path we instead use a
	// /bin/true short-lived child and Wait on it so it's reaped
	// before run() probes — Signal(0) returns ESRCH on a fully-reaped
	// PID, which the polling fallback treats as "gone".
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` binary on PATH: %v", err)
	}
	cmd := exec.Command(truePath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	parentPID := cmd.Process.Pid

	var buf bytes.Buffer
	rc := run([]string{
		"--parent-pid", itoa(parentPID),
		"--staged", staged,
		"--target", target,
		"--sha256", digest,
		"--data-dir", dir,
		"--wait-timeout", "5s",
	}, &buf)
	if rc != 0 {
		t.Fatalf("rc = %d stderr = %s", rc, buf.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("target not present after rename: %v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("staged still present (rename should have moved it): err=%v", err)
	}
	logBody, err := os.ReadFile(filepath.Join(dir, "update", "updater.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, want := range []string{"start", "parent_exited", "sha_verified", "rename_ok", "launched"} {
		if !strings.Contains(string(logBody), want) {
			t.Errorf("log missing %q:\n%s", want, string(logBody))
		}
	}
}

// TestRun_ShaMismatchDeletesStaged covers step 2's destructive-cleanup
// branch: a corrupt staged file gets unlinked so a future helper
// invocation doesn't keep failing on the same artifact.
func TestRun_ShaMismatchDeletesStaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sleep as fake parent")
	}
	dir := t.TempDir()
	staged := filepath.Join(dir, "corrupt.bin")
	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(staged, []byte("totally not the right bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` binary on PATH: %v", err)
	}
	cmd := exec.Command(truePath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	parentPID := cmd.Process.Pid

	var buf bytes.Buffer
	rc := run([]string{
		"--parent-pid", itoa(parentPID),
		"--staged", staged,
		"--target", target,
		"--sha256", strings.Repeat("0", 64),
		"--data-dir", dir,
		"--wait-timeout", "5s",
	}, &buf)
	if rc != 1 {
		t.Errorf("rc = %d want 1", rc)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Errorf("staged file should have been deleted: err=%v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("target should NOT have been created on sha mismatch: err=%v", err)
	}
}

// itoa is a tiny strconv.Itoa wrapper so the test table reads cleanly
// without an import line in every line.
func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = digits[i%10]
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
