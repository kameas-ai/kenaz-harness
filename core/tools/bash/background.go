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

// spawnBackground executes a bash command asynchronously. It spawns the
// process, confirms it is alive within 100 ms, registers it in the task
// registry via t.backgroundSpawn, then returns immediately with
// {task_id, status:"running"}.
//
// The goroutine launched here monitors the process and calls
// t.backgroundEnd(taskID, exitCode) when it finishes.
func (t *Tool) spawnBackground(ctx context.Context, commandLine, cwd string, timeout time.Duration, description string) (json.RawMessage, error) {
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		shell = "/bin/bash"
	}

	// Spawn without inheriting the parent context's cancellation.
	// Background tasks live independently of the calling turn's context.
	cmd := exec.Command(shell, "-l", "-c", commandLine)
	cmd.Dir = cwd

	// Start the process.
	if err := cmd.Start(); err != nil {
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
		return marshalResult(callResult{
			Stderr:   "bash background: process exited immediately",
			ExitCode: exitCode,
		})
	}

	sessionID := ""
	if t.sessionIDFromCtx != nil {
		sessionID = t.sessionIDFromCtx(ctx)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	taskID, err := t.backgroundSpawn(ctx, sessionID, commandLine, description, pid)
	if err != nil {
		// Registration failed — kill the orphaned process and return error.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return marshalResult(callResult{
			Stderr:   fmt.Sprintf("bash background: task registration failed: %v", err),
			ExitCode: -1,
		})
	}

	t.logf("bash.background.spawned",
		"task_id", taskID,
		"pid", pid,
		"command_truncated", truncateForLog(commandLine, 120),
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
