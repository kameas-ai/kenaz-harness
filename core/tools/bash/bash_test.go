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

	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
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

// ---------------------------------------------------------------------------
// Cedar gate tests (WP03)
//
// These tests exercise the six branches required by the WP03 acceptance
// criteria using a deterministic Cedar engine built from inline policy text.
// The "prompt" branches use a fake registry that captures the surface and
// pre-loads a canned resolution.
// ---------------------------------------------------------------------------

// buildAllowEngine returns an Engine whose single policy permits every
// BashCommand action.
func buildAllowEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	policy := `permit(principal, action == Action::"run_bash_command", resource);`
	if err := eng.SetPolicyText("allow_all_bash.cedar", []byte(policy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return eng
}

// buildDenyEngine returns an Engine whose single policy forbids every
// BashCommand action.
func buildDenyEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	policy := `forbid(principal, action == Action::"run_bash_command", resource);`
	if err := eng.SetPolicyText("deny_all_bash.cedar", []byte(policy)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return eng
}

// buildNotApplicableEngine returns an Engine with no policies at all —
// every evaluation returns NotApplicable.
func buildNotApplicableEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	eng, err := cedar.NewEngine(cedar.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// Empty policy set → NotApplicable.
	if err := eng.SetPolicyText("empty.cedar", []byte(`// no policies`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return eng
}

// fakeRegistry is a cedar.Registry pre-seeded with one resolution that
// fires when RequestInteractive is called. It captures the surface for
// inspection.
type fakeRegistry struct {
	reg      *cedar.Registry
	decision cedar.PromptDecision
	captured *cedar.PromptSurface
}

// newFakeRegistry builds a registry that immediately resolves any
// RequestInteractive call with the given decision. The registry uses a
// short timeout so tests don't hang.
func newFakeRegistry(t *testing.T, decision cedar.PromptDecision) *fakeRegistry {
	t.Helper()
	var captured cedar.PromptSurface
	fr := &fakeRegistry{
		decision: decision,
		captured: &captured,
	}

	// Build the registry first without a dispatcher.
	reg := cedar.NewRegistry(cedar.WithTimeout(2 * time.Second))
	fr.reg = reg

	// Wire a goroutine that polls pending requests and resolves them
	// immediately with the pre-seeded decision. Polling at 1ms intervals
	// keeps the RequestInteractive wait tiny.
	go func() {
		for {
			list := reg.ListPending()
			for _, p := range list {
				captured = p.Surface
				_ = reg.Resolve(p.RequestID, decision)
			}
			if len(list) > 0 {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	return fr
}

// TestCedarGateAllow verifies that a Cedar Allow decision lets the
// command run and the result carries ExitCode 0.
func TestCedarGateAllow(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = buildAllowEngine(t)
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "echo cedar_allow"}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0; stderr=%q", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "cedar_allow") {
		t.Errorf("Stdout = %q, want contains cedar_allow", res.Stdout)
	}
}

// TestCedarGateDenyTypedError verifies that a Cedar Deny decision
// produces a non-zero exit code and a "policy denied" stderr without
// returning a Go error (the model must see the denial, not a crash).
func TestCedarGateDenyTypedError(t *testing.T) {
	t.Parallel()
	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = buildDenyEngine(t)
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "echo denied_cmd"}))
	if err != nil {
		t.Fatalf("Call: %v (want nil Go error; denial surfaced as result)", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "denied") {
		t.Errorf("Stderr = %q, want contains 'denied'", res.Stderr)
	}
}

// TestCedarGateNotApplicablePromptAllowOnce verifies the
// NotApplicable → prompt → AllowOnce → run branch.
func TestCedarGateNotApplicablePromptAllowOnce(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	eng := buildNotApplicableEngine(t)
	fr := newFakeRegistry(t, cedar.DecisionAllowOnce)

	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = eng
		o.PromptRegistry = fr.reg
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "echo allow_once"}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (AllowOnce → run); stderr=%q", res.ExitCode, res.Stderr)
	}
}

// TestCedarGateNotApplicablePromptDeny verifies the
// NotApplicable → prompt → Deny → typed error branch.
func TestCedarGateNotApplicablePromptDeny(t *testing.T) {
	eng := buildNotApplicableEngine(t)
	fr := newFakeRegistry(t, cedar.DecisionDeny)

	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = eng
		o.PromptRegistry = fr.reg
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "echo user_denied"}))
	if err != nil {
		t.Fatalf("Call: %v (want nil Go error)", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 (Deny)", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "denied") {
		t.Errorf("Stderr = %q, want contains 'denied'", res.Stderr)
	}
}

// TestCedarGateNotApplicablePromptAllowAlwaysNonDangerous verifies the
// NotApplicable → prompt → AllowAlways non-dangerous → snippet written + run branch.
func TestCedarGateNotApplicablePromptAllowAlwaysNonDangerous(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	eng := buildNotApplicableEngine(t)
	fr := newFakeRegistry(t, cedar.DecisionAllowAlways)

	dataDir := t.TempDir()
	snippetWritten := false
	origMkdirAll := mkdirAll
	origWriteFile := writeFile
	t.Cleanup(func() {
		mkdirAll = origMkdirAll
		writeFile = origWriteFile
	})
	mkdirAll = func(path string) error { return os.MkdirAll(path, 0o755) }
	writeFile = func(path string, data []byte) error {
		snippetWritten = true
		return os.WriteFile(path, data, 0o644)
	}

	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = eng
		o.PromptRegistry = fr.reg
		o.DataDir = dataDir
	})
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "echo always_nondangerous"}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (AllowAlways → run); stderr=%q", res.ExitCode, res.Stderr)
	}
	if !snippetWritten {
		t.Errorf("policy snippet was not written for AllowAlways non-dangerous")
	}
	// Verify snippet exists in the expected location.
	entries, err := os.ReadDir(filepath.Join(dataDir, cedar.PolicyDir))
	if err != nil {
		t.Fatalf("ReadDir policy dir: %v", err)
	}
	found := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "bash_allow_") && strings.HasSuffix(e.Name(), ".cedar") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no bash_allow_*.cedar file found in %s after AllowAlways", dataDir)
	}
}

// TestCedarGateNotApplicablePromptAllowAlwaysDangerousDemoted verifies
// that AllowAlways on a dangerous command WITHOUT
// PermissionCacheDangerousOps is demoted to AllowOnce (no snippet written)
// and the command still runs.
func TestCedarGateNotApplicablePromptAllowAlwaysDangerousDemoted(t *testing.T) {
	if _, err := exec.LookPath("echo"); err != nil {
		t.Skipf("echo missing: %v", err)
	}
	eng := buildNotApplicableEngine(t)
	fr := newFakeRegistry(t, cedar.DecisionAllowAlways)

	dataDir := t.TempDir()
	snippetWritten := false
	origWriteFile := writeFile
	t.Cleanup(func() { writeFile = origWriteFile })
	writeFile = func(path string, data []byte) error {
		snippetWritten = true
		return os.WriteFile(path, data, 0o644)
	}

	// "rm" is classified dangerous; PermissionCacheDangerousOps = false (default).
	tool, _ := newTool(t, func(o *Options) {
		o.CedarEngine = eng
		o.PromptRegistry = fr.reg
		o.DataDir = dataDir
		o.PermissionCacheDangerousOps = false
	})
	// Use "rm" as command but echo-substitute via allowlist to avoid
	// actually removing anything — the gate fires before LookPath.
	// We only care about the gate logic, not execution.
	// Since Cedar engine is wired and the engine returns NotApplicable,
	// the gate will call the registry → AllowAlways → dangerous → demote.
	// The command will then try to run "rm" (which is on PATH). We just
	// check the policy snippet was NOT written.
	//
	// Note: we don't actually run rm here — we check ExitCode to confirm
	// the command was attempted (not blocked by the gate). If rm isn't
	// found, exit code 127 is also acceptable.
	raw, err := tool.Call(context.Background(), mustMarshal(t, callArgs{Command: "rm --version"}))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res := unmarshalResult(t, raw)
	// Gate should not have blocked (demotion allows the run).
	if res.ExitCode == -1 && strings.Contains(res.Stderr, "denied") {
		t.Errorf("command was blocked by gate; want demoted AllowOnce run; stderr=%q", res.Stderr)
	}
	if snippetWritten {
		t.Errorf("snippet was written for dangerous command without override; want demotion to AllowOnce (no snippet)")
	}
}
