package bash

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// spawnBackground executes a bash command asynchronously. It registers
// the task in the task registry via t.backgroundSpawn BEFORE the process
// starts (subagent-control-and-background-tasks-01PMZB11 UNIT-3) — the
// task id has to exist first so the stdout/stderr writers can be
// attached to cmd.Stdout/cmd.Stderr ahead of cmd.Start(). Registering
// after Start(), as this function used to, means the writers can never
// be attached post-hoc and every task's captured output is permanently
// empty (scripts/ci/allowlists/i11-unregistered-builtin-tools.txt named
// this exact defect).
//
// It spawns the process, confirms it is alive within 100 ms, reports the
// real PID once known, then returns immediately with
// {task_id, status:"running"}.
//
// The goroutine launched here monitors the process and calls
// t.backgroundEnd(taskID, exitCode) when it finishes.
//
// execCommand is the resolved (post-@secret:-substitution) command line —
// the only thing ever handed to exec.Command, so the process itself still
// sees the real credential. logCommand is the unresolved (pre-substitution)
// command line the model originally sent — the only thing ever persisted
// via backgroundSpawn (registry -> SQLite tasks.cmd) or written to logs.
// This mirrors the synchronous path in bash.go, which stores args.Command
// (pre-substitution) into t.store, never the resolved commandLine.
// Regression: an earlier version of this function took a single
// commandLine parameter and used the resolved form for both execution AND
// persistence/logging, writing resolved secret plaintext permanently to
// SQLite and to the bash.background.spawned log line on every DNS
// failure- free background command carrying a @secret: reference.
func (t *Tool) spawnBackground(ctx context.Context, execCommand, logCommand, cwd string, timeout time.Duration, description string) (json.RawMessage, error) {
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		shell = "/bin/bash"
	}

	sessionID := ""
	if t.sessionIDFromCtx != nil {
		sessionID = t.sessionIDFromCtx(ctx)
	}

	// Register FIRST, with pid:0 — the id must exist before Start() so
	// the output writers below can be attached to cmd.Stdout/cmd.Stderr
	// from the first byte the process writes, not after the fact.
	// logCommand (unresolved) is what gets persisted — never execCommand.
	taskID, err := t.backgroundSpawn(ctx, sessionID, logCommand, description, 0)
	if err != nil {
		return marshalResult(callResult{
			Stderr:   fmt.Sprintf("bash background: task registration failed: %v", err),
			ExitCode: -1,
		})
	}

	// Spawn without inheriting the parent context's cancellation.
	// Background tasks live independently of the calling turn's context.
	// execCommand (resolved) is what actually runs — the process needs the
	// real credential.
	cmd := exec.Command(shell, "-l", "-c", execCommand)
	cmd.Dir = cwd

	// Attach the registry's stdout/stderr writers BEFORE Start() so every
	// byte the process writes reaches the ring buffer + log file +
	// __monitor subscribers in real time. A nil BackgroundWriters (no
	// task registry wired) or a false ok leaves cmd.Stdout/cmd.Stderr
	// nil, same as before this unit — output is simply discarded, not a
	// new failure mode.
	if t.backgroundWriters != nil {
		if stdout, stderr, ok := t.backgroundWriters(taskID); ok {
			cmd.Stdout = stdout
			cmd.Stderr = stderr
		}
	}

	// Start the process.
	if err := cmd.Start(); err != nil {
		if t.backgroundEnd != nil {
			t.backgroundEnd(ctx, taskID, -1)
		}
		return marshalResult(callResult{
			Stderr:   fmt.Sprintf("bash background: failed to start: %v", err),
			ExitCode: -1,
		})
	}

	// Confirm alive within 100 ms.
	alive := make(chan struct{}, 1)
	go func() {
		// A nil ProcessState means the process is still running.
		if cmd.ProcessState == nil {
			close(alive)
		}
	}()
	select {
	case <-alive:
	case <-time.After(100 * time.Millisecond):
		// 100 ms grace period elapsed; treat as alive (most processes
		// haven't had time to exit yet).
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		// Process already exited within 100 ms (unlikely but handle).
		exitCode := cmd.ProcessState.ExitCode()
		if t.backgroundEnd != nil {
			t.backgroundEnd(ctx, taskID, exitCode)
		}
		return marshalResult(callResult{
			Stderr:   "bash background: process exited immediately",
			ExitCode: exitCode,
		})
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if t.backgroundSetPID != nil {
		t.backgroundSetPID(taskID, pid)
	}

	t.logf("bash.background.spawned",
		"task_id", taskID,
		"pid", pid,
		"command_truncated", truncateForLog(logCommand, 120),
	)

	// Monitor goroutine: waits for process exit then calls backgroundEnd.
	go func() {
		// Apply the timeout if provided.
		var waitCtx context.Context
		var cancel context.CancelFunc
		if timeout > 0 {
			waitCtx, cancel = context.WithTimeout(context.Background(), timeout)
		} else {
			waitCtx, cancel = context.WithCancel(context.Background())
		}
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		var exitErr error
		select {
		case exitErr = <-done:
		case <-waitCtx.Done():
			// Timeout — kill the process.
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			exitErr = waitCtx.Err()
		}

		exitCode := 0
		if exitErr != nil {
			if e, ok := exitErr.(*exec.ExitError); ok {
				exitCode = e.ExitCode()
			} else {
				exitCode = -1
			}
		}

		if t.backgroundEnd != nil {
			t.backgroundEnd(context.Background(), taskID, exitCode)
		}

		t.logf("bash.background.exited",
			"task_id", taskID,
			"pid", pid,
			"exit_code", exitCode,
		)
	}()

	b, err := json.Marshal(backgroundResult{
		TaskID: taskID,
		Status: "running",
	})
	if err != nil {
		return nil, fmt.Errorf("bash: marshal background result: %w", err)
	}
	return json.RawMessage(b), nil
}

// truncateForLog truncates a string for log output.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
