package bash

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// newTool constructs a Tool with a freshly-created temp dir as its
// sandbox root. Returns the tool and the canonical (EvalSymlinks-
// resolved) sandbox path so tests can compare against pwd output.
func newTool(t *testing.T, opts ...func(*Options)) (*Tool, string) {
	t.Helper()
	dir := t.TempDir()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", dir, err)
	}
	o := Options{SandboxRoot: canonical}
	for _, fn := range opts {
		fn(&o)
	}
	return New(o), canonical
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func unmarshalResult(t *testing.T, raw json.RawMessage) callResult {
	t.Helper()
	var out callResult
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal result %s: %v", raw, err)
	}
	return out
}

func TestToolMetadata(t *testing.T) {
	t.Parallel()
	tool := New(Options{SandboxRoot: t.TempDir()})
	if got := tool.Name(); got != "kaneaz__bash" {
		t.Errorf("Name = %q, want kaneaz__bash", got)
	}
	if tool.Description() == "" {
		t.Errorf("Description is empty")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema(), &schema); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	for _, key := range []string{"command", "working_dir", "timeout_seconds"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing property %q", key)
		}
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "command" {
		t.Errorf("required = %v, want [command]", required)
	}
}

func TestCallAllowedCommandRunsInSandbox(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `echo hello`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Errorf("Stdout = %q, want contains hello", res.Stdout)
	}
}

func TestCallDisallowedCommandReturnsNotAllowed(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `rm -rf /`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "not allowed") {
		t.Errorf("Stderr = %q, want contains 'not allowed'", res.Stderr)
	}
}

func TestCallWorkingDirTraversalRejected(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command:    `ls`,
		WorkingDir: "../../../../etc",
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "outside sandbox") {
		t.Errorf("Stderr = %q, want contains 'outside sandbox'", res.Stderr)
	}
}

func TestCallSymlinkEscapeRejected(t *testing.T) {
	t.Parallel()
	tool, root := newTool(t)
	// Plant a symlink "escape" inside the sandbox pointing at /tmp.
	target := os.TempDir()
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", target, err)
	}
	if strings.HasPrefix(canonicalTarget, root) {
		t.Skipf("temp dir %q resolves under sandbox %q; cannot stage escape", canonicalTarget, root)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(canonicalTarget, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command:    `ls`,
		WorkingDir: "escape",
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if !strings.Contains(res.Stderr, "outside sandbox") {
		t.Errorf("Stderr = %q, want contains 'outside sandbox' (symlink escape rejection)", res.Stderr)
	}
}

func TestCallAbsoluteWorkingDirOutsideRejected(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command:    `ls`,
		WorkingDir: "/tmp",
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if !strings.Contains(res.Stderr, "outside sandbox") {
		t.Errorf("Stderr = %q, want contains 'outside sandbox'", res.Stderr)
	}
}

func TestCallPipeBlockedByAllowlist(t *testing.T) {
	t.Parallel()
	// Pipes / redirects parse into argv but the metachar token
	// itself ("|") fails the allowlist downstream. The allowlist
	// is checked on argv[0] only, so a command like
	//     echo hi | grep h
	// parses as ["echo", "hi", "|", "grep", "h"] and "echo" is
	// allowed. To test the ACTUAL pipe-blocking story (the model
	// can't get a shell pipe), assert that the |/redirect bytes
	// arrive in argv (so the child sees them as literal args, not
	// as shell metacharacters).
	tool, _ := newTool(t)
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `echo hi | grep h`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	// echo prints all its args verbatim — we should see the literal
	// "|" and "grep" tokens in stdout because there's no shell.
	if !strings.Contains(res.Stdout, "|") || !strings.Contains(res.Stdout, "grep") {
		t.Errorf("Stdout = %q, want containing literal pipe + grep tokens", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (echo always succeeds)", res.ExitCode)
	}
}

func TestCallOutputCapTruncates(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skipf("python3 missing: %v", err)
	}
	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `python3 -c "import sys; sys.stdout.write('x' * 102400)"`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if !res.Truncated {
		t.Errorf("Truncated = false, want true")
	}
	if got := len(res.Stdout); got != DefaultMaxOutputBytes {
		t.Errorf("len(Stdout) = %d, want %d", got, DefaultMaxOutputBytes)
	}
}

func TestCallTimeoutFires(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skipf("sleep missing: %v", err)
	}
	tool, _ := newTool(t, func(o *Options) {
		o.Allowlist = append([]string{"sleep"}, DefaultAllowlist...)
	})
	one := 1
	start := time.Now()
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command:        `sleep 30`,
		TimeoutSeconds: &one,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("elapsed = %v, want timeout near 1s", elapsed)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode == 0 {
		t.Errorf("ExitCode = 0 after timeout, want non-zero")
	}
}

func TestCallEmptyAllowlistDeniesEverything(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t, func(o *Options) {
		o.Allowlist = []string{}
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `echo hi`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if !strings.Contains(res.Stderr, "not allowed") {
		t.Errorf("Stderr = %q, want contains 'not allowed' under empty allowlist", res.Stderr)
	}
}

func TestCallEmptyCommandErrors(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	if _, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: ""})); err == nil {
		t.Errorf("Call with empty command err = nil, want non-nil")
	}
}

func TestCallParseErrorReturnsError(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	raw := json.RawMessage(`{"command":"echo 'unterminated"}`)
	if _, err := tool.Call(context.Background(), raw); err == nil {
		t.Errorf("Call with unterminated quote err = nil, want non-nil")
	}
}

func TestCallInvalidJSONErrors(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t)
	if _, err := tool.Call(context.Background(), json.RawMessage(`{not json`)); err == nil {
		t.Errorf("Call with bad JSON err = nil, want non-nil")
	}
}

// NFR-005: the allowlist gate sits BEFORE exec.LookPath. A planted
// "rm" binary on the test process's PATH must NOT bypass the gate.
// We plant a script named "rm" in a temp dir, prepend that dir to
// PATH, and verify the call still returns "command not allowed".
func TestCallAllowlistRunsBeforeLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("planted-binary test relies on POSIX shebang")
	}
	plantedDir := t.TempDir()
	plantedPath := filepath.Join(plantedDir, "rm")
	if err := os.WriteFile(plantedPath, []byte("#!/bin/sh\necho planted_rm_was_executed\n"), 0o755); err != nil {
		t.Fatalf("write planted rm: %v", err)
	}
	t.Setenv("PATH", plantedDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tool, _ := newTool(t)
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `rm everything`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if !strings.Contains(res.Stderr, "not allowed") {
		t.Errorf("Stderr = %q, want 'not allowed' (allowlist must gate before LookPath)", res.Stderr)
	}
	if strings.Contains(res.Stdout, "planted_rm_was_executed") {
		t.Errorf("planted rm executed; allowlist gate failed")
	}
}

func TestCallCommandNotFoundReturns127(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t, func(o *Options) {
		// Allow a name that we know isn't on PATH.
		o.Allowlist = []string{"definitely_not_a_real_binary_xyz"}
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `definitely_not_a_real_binary_xyz`,
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 127 {
		t.Errorf("ExitCode = %d, want 127", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "not found") {
		t.Errorf("Stderr = %q, want contains 'not found'", res.Stderr)
	}
}

func TestCallTimeoutDefaults(t *testing.T) {
	t.Parallel()
	if got := resolveTimeout(nil); got != defaultTimeoutSeconds*time.Second {
		t.Errorf("resolveTimeout(nil) = %v, want %ds", got, defaultTimeoutSeconds)
	}
	zero := 0
	if got := resolveTimeout(&zero); got != defaultTimeoutSeconds*time.Second {
		t.Errorf("resolveTimeout(0) = %v, want default", got)
	}
	huge := 99999
	if got := resolveTimeout(&huge); got != maxTimeoutSeconds*time.Second {
		t.Errorf("resolveTimeout(99999) = %v, want capped at %ds", got, maxTimeoutSeconds)
	}
	five := 5
	if got := resolveTimeout(&five); got != 5*time.Second {
		t.Errorf("resolveTimeout(5) = %v, want 5s", got)
	}
}

// NFR-007: bash dispatch overhead must be under 100ms beyond the
// child process's own runtime. We approximate by running an
// allowlist-passing nonexistent program: the LookPath miss returns
// without ever spawning a child, so the elapsed time is pure
// dispatch overhead.
func TestCallDispatchOverheadUnder100ms(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t, func(o *Options) {
		o.Allowlist = []string{"definitely_missing_xyz"}
	})
	start := time.Now()
	if _, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command: `definitely_missing_xyz`,
	})); err != nil {
		t.Fatalf("Call: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("dispatch overhead = %v, want < 100ms (NFR-007)", elapsed)
	}
}

func TestCallRelativeWorkingDirJoinsToSandbox(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("pwd"); err != nil {
		t.Skipf("pwd missing: %v", err)
	}
	tool, root := newTool(t)
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{
		Command:    `pwd`,
		WorkingDir: "sub",
	}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	got := strings.TrimSpace(res.Stdout)
	if !strings.HasSuffix(got, "sub") {
		t.Errorf("pwd stdout = %q, want suffix 'sub'", got)
	}
}

func TestNewWithNilAllowlistUsesDefault(t *testing.T) {
	t.Parallel()
	tool := New(Options{SandboxRoot: t.TempDir(), Allowlist: nil})
	if len(tool.allowlist) != len(DefaultAllowlist) {
		t.Errorf("allowlist len = %d, want %d", len(tool.allowlist), len(DefaultAllowlist))
	}
}
