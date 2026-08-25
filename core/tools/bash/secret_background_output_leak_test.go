package bash_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/tasks"
	"github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// outputLeakSentinel is a distinctive plaintext value, grep-able in any
// leaked artifact, matching the reviewer's reproduction shape.
const outputLeakSentinel = "SUPERSECRET-OUTPUT-PLAINTEXT-456"

// hookCapture is a race-safe capture of the last background_task_complete
// hook payload fired. Per CLAUDE.md's race-safe test fake pattern: writes
// come from Registry.End's `go hookFirer(...)` goroutine, reads come from
// the test body — so this needs a mutex + snapshot helper, not a bare
// struct field.
type hookCapture struct {
	mu      sync.Mutex
	payload tasks.BackgroundTaskCompletePayload
	fired   bool
}

func (h *hookCapture) fire(_ context.Context, p tasks.BackgroundTaskCompletePayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.payload = p
	h.fired = true
}

func (h *hookCapture) snapshot() (tasks.BackgroundTaskCompletePayload, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.payload, h.fired
}

// setupOutputSecretResolverCtx mirrors setupSecretResolverCtx in
// secret_background_leak_test.go but resolves a DIFFERENT locator/value
// pair (outputLeakSentinel) so the two tests can never pass each other's
// fixture by accident.
func setupOutputSecretResolverCtx(t *testing.T) (context.Context, *refs.Sanitizer) {
	t.Helper()
	idx := secrets.NewExposureIndex()
	idx.Add(secrets.ExposedEntry{
		Locator:     "user:tok",
		Description: "test secret",
		Scope:       secrets.ScopeSession,
		KindHint:    secrets.KindHintBearer,
	}, []byte(outputLeakSentinel))
	san := refs.NewSanitizer()
	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "chat",
	})
	ctx := refs.WithTurnSanitizer(context.Background(), san)
	ctx = refs.WithResolver(ctx, resolver)
	return ctx, san
}

// newRealBackgroundRegistry wires a *tasks.Registry the same way
// core/rpc/builtins_wiring.go does — including the Sanitizer.Clone() call
// at Register time that is the actual fix under test — and returns the
// bash.Options hooks plus the registry itself so the test can read the
// real sinks afterward.
func newRealBackgroundRegistry(t *testing.T, logDir string, hook *hookCapture) (*tasks.Registry, bash.Options) {
	t.Helper()
	reg := tasks.NewRegistry(tasks.Options{
		LogDir:    logDir,
		HookFirer: hook.fire,
	})

	bgSpawn := func(ctx context.Context, sessionID, cmd, description string, pid int) (string, error) {
		var sanitizer tasks.OutputSanitizer
		if s := refs.SanitizerFromContext(ctx); s != nil {
			sanitizer = s.Clone()
		}
		return reg.Register(ctx, tasks.RegisterOpts{
			Kind:           tasks.KindBash,
			OwnerSessionID: sessionID,
			Cmd:            cmd,
			Description:    description,
			PID:            pid,
			Sanitizer:      sanitizer,
		})
	}
	bgWriters := func(taskID string) (io.Writer, io.Writer, bool) {
		stdout, ok1 := reg.StdoutWriter(taskID)
		stderr, ok2 := reg.StderrWriter(taskID)
		if !ok1 || !ok2 {
			return nil, nil, false
		}
		return stdout, stderr, true
	}
	bgSetPID := func(taskID string, pid int) {
		reg.SetPID(taskID, pid)
	}
	bgEnd := func(ctx context.Context, taskID string, exitCode int) {
		_ = reg.End(ctx, taskID, exitCode)
	}

	return reg, bash.Options{
		SandboxRoot:       "/tmp",
		Allowlist:         []string{"echo", "sh", "bash", "sleep"},
		BackgroundSpawn:   bgSpawn,
		BackgroundWriters: bgWriters,
		BackgroundSetPID:  bgSetPID,
		BackgroundEnd:     bgEnd,
	}
}

func waitForTerminal(t *testing.T, reg *tasks.Registry, taskID string) tasks.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, ok := reg.Get(taskID)
		if ok && task.IsTerminal() {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach terminal state in time", taskID)
	return tasks.Task{}
}

// TestRunInBackground_OutputSinksDoNotLeakResolvedSecret is the
// falsification test for LEAK 2 round 3: bash's background arm writes
// cmd.Stdout/cmd.Stderr straight to the task registry's writers with no
// sanitizer attached anywhere (core/tools/bash/background.go:78-81),
// unlike the synchronous arm which sanitizes res.Stdout/res.Stderr via
// refs.SanitizerFromContext(ctx) before returning (bash.go:470-477).
//
// This drives the REAL core/tasks.Registry with a REAL temp LogDir and
// REAL BackgroundWriters (StdoutWriter/StderrWriter -> lineWriter ->
// ring buffer + on-disk log file + AppendLine), not a stub that only
// captures its own argument — the existing
// secret_background_leak_test.go fixture stubs BackgroundSpawn/
// BackgroundEnd and never touches BackgroundWriters at all, so it has
// zero coverage of the output path this test targets.
//
// The command is `echo @secret:user:tok`, which the resolver substitutes
// to `echo SUPERSECRET-OUTPUT-PLAINTEXT-456` before exec — the child
// process's own stdout becomes the resolved secret. Assertions cover all
// three directly-inspectable sinks:
//  1. the actual on-disk log file bytes at <logDir>/<taskID>.log
//  2. the ring-buffer tail, read via the background_task_complete hook
//     payload's StdoutTail (which IS registry.go's e.stdout.Tail() call)
//  3. registry.Tail(taskID, 0), the exact read path kenaz__monitor's
//     drain/watch modes and Registry.AppendLine feed (core/tools/monitor/
//     tool.go)
func TestRunInBackground_OutputSinksDoNotLeakResolvedSecret(t *testing.T) {
	ctx, _ := setupOutputSecretResolverCtx(t)

	logDir := t.TempDir()
	hook := &hookCapture{}
	reg, opts := newRealBackgroundRegistry(t, logDir, hook)
	tool := bash.New(opts)

	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "echo @secret:user:tok",
		"run_in_background": true,
		"description":       "output leak test",
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.TaskID == "" {
		t.Fatal("task_id is empty")
	}

	task := waitForTerminal(t, reg, out.TaskID)
	if task.ExitCode != 0 {
		t.Fatalf("task exited %d, want 0 (echo should succeed)", task.ExitCode)
	}

	// ── Sink 1: on-disk log file ────────────────────────────────────────
	logPath := filepath.Join(logDir, out.TaskID+".log")
	logBytes, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read log file: %v", rerr)
	}
	if strings.Contains(string(logBytes), outputLeakSentinel) {
		t.Errorf("LEAK: on-disk task log carries resolved plaintext: %q", string(logBytes))
	}
	if !strings.Contains(string(logBytes), "[redacted: user:tok]") {
		t.Errorf("on-disk task log missing redaction placeholder; got %q", string(logBytes))
	}

	// ── Sink 2: ring-buffer tail (via the hook payload, which is a
	//    direct e.stdout.Tail() / e.stderr.Tail() read) ──────────────────
	deadline := time.Now().Add(2 * time.Second)
	var payload tasks.BackgroundTaskCompletePayload
	for time.Now().Before(deadline) {
		p, fired := hook.snapshot()
		if fired {
			payload = p
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if payload.TaskID == "" {
		t.Fatal("background_task_complete hook never fired")
	}
	if strings.Contains(payload.StdoutTail, outputLeakSentinel) {
		t.Errorf("LEAK: hook payload StdoutTail (ring-buffer tail) carries resolved plaintext: %q", payload.StdoutTail)
	}
	if !strings.Contains(payload.StdoutTail, "[redacted: user:tok]") {
		t.Errorf("hook payload StdoutTail missing redaction placeholder; got %q", payload.StdoutTail)
	}

	// ── Sink 3: kenaz__monitor's read path (Registry.Tail) ──────────────
	lines, _, ok := reg.Tail(out.TaskID, 0)
	if !ok {
		t.Fatal("Tail: task not found")
	}
	var allText strings.Builder
	for _, ln := range lines {
		allText.WriteString(ln.Text)
		allText.WriteString("\n")
	}
	if strings.Contains(allText.String(), outputLeakSentinel) {
		t.Errorf("LEAK: kenaz__monitor read path (Registry.Tail Lines) carries resolved plaintext: %q", allText.String())
	}
	if !strings.Contains(allText.String(), "[redacted: user:tok]") {
		t.Errorf("Registry.Tail Lines missing redaction placeholder; got %q", allText.String())
	}
}

// TestRunInBackground_OutputRedactionSurvivesTurnClear is the proof
// required for the hard part of this fix: the turn's refs.Sanitizer is
// Clear()'d by chat_runner.go's `defer sanitizer.Clear()` at end-of-turn,
// but a background task keeps running (and keeps writing output) after
// the turn that spawned it ends. A fix that wraps the task's output
// writers with the SAME *refs.Sanitizer the turn uses (instead of a
// Clone) would redact correctly right up until Clear() runs, then
// silently stop — passing a fast single-write test while leaking in
// production on any task whose output arrives after the turn completes.
//
// This test spawns the background task, waits for BackgroundSpawn to
// return (which is when core/rpc/builtins_wiring.go's bgSpawn closure
// calls Sanitizer.Clone(), BEFORE cmd.Start() can write a single byte —
// see background.go's Register-before-Start ordering comment), THEN
// calls Clear() on the ORIGINAL turn sanitizer to simulate the chat
// runner's end-of-turn defer firing while the task is still writing.
// Redaction must still occur after Clear().
func TestRunInBackground_OutputRedactionSurvivesTurnClear(t *testing.T) {
	ctx, turnSanitizer := setupOutputSecretResolverCtx(t)

	logDir := t.TempDir()
	hook := &hookCapture{}
	reg, opts := newRealBackgroundRegistry(t, logDir, hook)
	tool := bash.New(opts)

	// A command that sleeps briefly before echoing, so we have a window
	// to Clear() the turn sanitizer while the task is still alive and
	// has not yet written its output.
	argsJSON, _ := json.Marshal(map[string]any{
		"command":           "sleep 0.3 && echo @secret:user:tok",
		"run_in_background": true,
		"description":       "turn-clear survival test",
	})

	result, err := tool.Call(ctx, argsJSON)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	var out struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(result, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Simulate end-of-turn: the chat runner's defer fires while the
	// background task is still running (it sleeps 0.3s before writing).
	// Len() proves there was something to clear in the first place —
	// otherwise this test would pass vacuously.
	if turnSanitizer.Len() == 0 {
		t.Fatal("turn sanitizer has no registered fingerprints before Clear(); fixture is broken")
	}
	turnSanitizer.Clear()
	if turnSanitizer.Len() != 0 {
		t.Fatal("Clear() did not empty the turn sanitizer; fixture is broken")
	}

	task := waitForTerminal(t, reg, out.TaskID)
	if task.ExitCode != 0 {
		t.Fatalf("task exited %d, want 0", task.ExitCode)
	}

	logPath := filepath.Join(logDir, out.TaskID+".log")
	logBytes, rerr := os.ReadFile(logPath)
	if rerr != nil {
		t.Fatalf("read log file: %v", rerr)
	}
	if strings.Contains(string(logBytes), outputLeakSentinel) {
		t.Errorf("LEAK: on-disk task log carries resolved plaintext AFTER the turn's Sanitizer.Clear() ran: %q", string(logBytes))
	}
	if !strings.Contains(string(logBytes), "[redacted: user:tok]") {
		t.Errorf("on-disk task log missing redaction placeholder after turn Clear(); got %q — redaction silently stopped when the turn ended", string(logBytes))
	}
}
