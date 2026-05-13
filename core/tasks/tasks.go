// Package tasks is the harness background-task registry.
//
// Every long-running process spawned by the harness — backgrounded bash
// commands, dispatched sub-agents — is tracked as a Task here. The registry
// is in-memory (sync.Map) with a SQLite journal for forensics/audit.
//
// # Public cross-mission API
//
// Other v0.11.0 missions consume:
//
//   - Registry.Register  — create a new task entry (bash background mode, sub-agent dispatch)
//   - Registry.End       — mark a task terminal; fires background_task_complete hook
//   - Registry.Get / List / Tail / Abort
//   - Registry.Subscribe — line-stream subscription used by __monitor
//   - BackgroundTaskCompletePayload — hook event shape published to hooks.EventBackgroundTaskComplete
//
// Status constants follow the lifecycle:
//
//	pending → running → completed | failed | cancelled | crashed
package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Status values for a Task.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusCrashed   = "crashed"
)

// Kind values for a Task.
const (
	KindBash     = "bash"
	KindSubagent = "subagent"
	KindWails    = "wails"
)

// Task is a single background job tracked by the Registry.
// The layout is the cross-mission public contract; other missions may
// embed or reference this struct but must not modify it independently.
type Task struct {
	// ID is a globally-unique task identifier (hex-encoded 12 random bytes
	// with a "task-" prefix, matching the bash run-id shape).
	ID string `json:"id"`

	// Kind ∈ KindBash | KindSubagent | KindWails.
	Kind string `json:"kind"`

	// OwnerSessionID is the session that spawned this task. May be empty
	// for harness-internal tasks. Tasks outlive their owner session.
	OwnerSessionID string `json:"owner_session_id,omitempty"`

	// Cmd is the shell command or symbolic name. Stored for display only;
	// never passed to Cedar (the policy gate runs at spawn time).
	Cmd string `json:"cmd,omitempty"`

	// Description is the human-readable label shown in the Tasks panel
	// (populated from the run_in_background.description input field).
	Description string `json:"description,omitempty"`

	// Status is one of the Status* constants.
	Status string `json:"status"`

	// ExitCode is the OS exit status. 0 on success; -1 if the process was
	// killed, crashed, or never started. Meaningful only in terminal states.
	ExitCode int `json:"exit_code"`

	// StartedAt is set when the task is registered.
	StartedAt time.Time `json:"started_at"`

	// EndedAt is set when End is called.
	EndedAt *time.Time `json:"ended_at,omitempty"`
}

// IsTerminal returns true for all terminal-state status values.
func (t *Task) IsTerminal() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusCancelled, StatusCrashed:
		return true
	}
	return false
}

// BackgroundTaskCompletePayload is the hook-event payload published
// on hooks.EventBackgroundTaskComplete. This shape is the cross-mission
// contract; branch-subagent-interactive registers hooks on this event to
// observe sub-agent completion.
//
// Field sizes: StdoutTail and StderrTail are at most 8 KiB each (8192 bytes).
type BackgroundTaskCompletePayload struct {
	TaskID         string `json:"task_id"`
	Kind           string `json:"kind"`
	ExitCode       int    `json:"exit_code"`
	DurationMs     int64  `json:"duration_ms"`
	StdoutTail     string `json:"stdout_tail"`
	StderrTail     string `json:"stderr_tail"`
	OwnerSessionID string `json:"owner_session_id,omitempty"`
}

// Line is one captured output line from a task's stdout or stderr.
type Line struct {
	// Stream is "stdout" or "stderr".
	Stream string `json:"stream"`
	// Text is the line text (without the trailing newline byte).
	Text string `json:"text"`
	// Offset is the monotonic line number within the task's combined stream.
	Offset int64 `json:"offset"`
	// At is the wall-clock time the line was captured.
	At time.Time `json:"at"`
}

// NewTaskID returns a unique task identifier.
func NewTaskID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return "task-" + hex.EncodeToString(b[:])
}
