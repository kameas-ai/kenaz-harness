// fire.go — generic Fire / FireAsync for v2 lifecycle events.
//
// The v1 RunPreSend / RunPostSend methods on Runner remain the API for
// chat-pipeline events. This file adds:
//
//   - Event payload types for all 14 new v2 events.
//   - Runner.Fire(ctx, event, payload) → ([]HookOutput, error)
//     Dispatches every enabled matching hook synchronously; returns the
//     raw slice so callers can call MergeOutputs themselves.
//   - Runner.FireAsync(ctx, event, payload)
//     Enqueues in the bounded worker pool and returns immediately.
//     Pool size is configurable (AsyncPoolSize in Config); default 8.
//
// Outcome vocabulary (mirrors plan §telemetry):
//
//	"success"            — hook completed and returned output.
//	"non_blocking_error" — hook timed out or errored; pipeline continues.
//	"cancelled"          — context was cancelled before hook started.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ── v2 event payload types ────────────────────────────────────────────────

// PreToolUseEvent fires before a tool call is dispatched. The hook may
// return UpdatedInput, AdditionalContext, or Decision=block.
type PreToolUseEvent struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	Model     string          `json:"model,omitempty"`
	Kind      string          `json:"kind,omitempty"`
}

// PostToolUseEvent fires after a tool call completes (success path).
// The hook may return AdditionalContext or UpdatedMCPOutput (MCP only).
type PostToolUseEvent struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Model     string          `json:"model,omitempty"`
	Kind      string          `json:"kind,omitempty"`
}

// PostToolUseFailureEvent fires after a tool call returns an error.
type PostToolUseFailureEvent struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input,omitempty"`
	Error     string          `json:"error"`
	Model     string          `json:"model,omitempty"`
	Kind      string          `json:"kind,omitempty"`
}

// UserPromptSubmitEvent fires when the user submits a message before it
// is appended to the conversation and sent to the LLM. The hook may
// return AdditionalContext to inject project rules into the next turn.
type UserPromptSubmitEvent struct {
	SessionID string `json:"session_id"`
	UserText  string `json:"user_text"`
	Model     string `json:"model,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// SessionStartEvent fires when a new session is created. The hook may
// return WatchPaths to start the fsnotify watcher.
type SessionStartEvent struct {
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id,omitempty"`
	CWD       string `json:"cwd,omitempty"`
	Model     string `json:"model,omitempty"`
}

// SetupEvent fires once per session before the first LLM turn.
// One-time preparation (e.g. project rules ingestion) goes here.
type SetupEvent struct {
	SessionID string `json:"session_id"`
	ProjectID string `json:"project_id,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

// SubagentStartEvent fires when a branch node is about to spawn a child
// session. The hook's AdditionalContext is prepended to the child's
// system context.
type SubagentStartEvent struct {
	ParentSessionID string `json:"parent_session_id"`
	BranchID        string `json:"branch_id"`
	ProfileID       string `json:"profile_id,omitempty"`
	Prompt          string `json:"prompt,omitempty"`
}

// CwdChangedEvent fires when the working directory changes in a session.
// The hook may return WatchPaths to update the fsnotify watcher.
type CwdChangedEvent struct {
	SessionID string `json:"session_id"`
	OldCWD    string `json:"old_cwd,omitempty"`
	NewCWD    string `json:"new_cwd"`
}

// FileChangedEvent fires when a watched file changes (debounced batch).
type FileChangedEvent struct {
	SessionID string         `json:"session_id"`
	Paths     []string       `json:"paths"`
	Events    []FileChangedOp `json:"events"`
}

// FileChangedOp is one filesystem operation within a debounced batch.
type FileChangedOp struct {
	Path string `json:"path"`
	Op   string `json:"op"` // "create" | "write" | "remove" | "rename" | "chmod"
}

// PermissionRequestEvent fires before Cedar evaluates an interactive
// permission prompt. The hook may pre-decide allow or deny.
type PermissionRequestEvent struct {
	SessionID   string          `json:"session_id"`
	ActionKind  string          `json:"action_kind"`
	Resource    string          `json:"resource,omitempty"`
	ArgsSummary string          `json:"args_summary,omitempty"`
	RawInput    json.RawMessage `json:"raw_input,omitempty"`
}

// PermissionDeniedEvent fires after Cedar issues a final deny decision.
type PermissionDeniedEvent struct {
	SessionID  string `json:"session_id"`
	ActionKind string `json:"action_kind"`
	Resource   string `json:"resource,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// NotificationEvent fires when the harness wants to surface an
// OS-level notification to the user.
type NotificationEvent struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Level     string `json:"level,omitempty"` // "info" | "warning" | "error"
}

// BackgroundTaskCompleteEvent fires when a background task finishes.
// This is the authoritative wire shape for the background_task_complete event
// (background-task-monitor-01KZNP3C WP04). Other v0.11.0 missions that register
// hooks on this event consume ExitCode, DurationMs, StdoutTail, and StderrTail.
type BackgroundTaskCompleteEvent struct {
	// SessionID is the owning session (may be empty for orphaned tasks).
	SessionID string `json:"session_id"`
	// TaskID identifies the completed task (matches tasks.Task.ID).
	TaskID string `json:"task_id"`
	// TaskKind is the task kind: "bash" | "subagent" | "wails".
	TaskKind string `json:"task_kind"`
	// ExitCode is the OS exit status. 0 for clean completion; non-zero for failure.
	ExitCode int `json:"exit_code"`
	// DurationMs is the wall-clock duration from task start to end.
	DurationMs int64 `json:"duration_ms"`
	// StdoutTail is the last ≤8 KiB of the task's stdout stream.
	StdoutTail string `json:"stdout_tail,omitempty"`
	// StderrTail is the last ≤8 KiB of the task's stderr stream.
	StderrTail string `json:"stderr_tail,omitempty"`
	// Success is true when ExitCode == 0. Convenience alias retained for
	// hooks written against the pre-WP04 shape.
	Success bool `json:"success"`
	// Error is the string form of the non-zero exit (empty on success).
	// Retained for backward compatibility with hooks written before WP04.
	Error string `json:"error,omitempty"`
}

// WorktreeCreateEvent fires when a new git worktree is created.
type WorktreeCreateEvent struct {
	SessionID    string `json:"session_id"`
	WorktreePath string `json:"worktree_path"`
	BranchName   string `json:"branch_name,omitempty"`
}

// ── Fire / FireAsync ──────────────────────────────────────────────────────

// Fire dispatches every enabled matching hook for the given event and
// payload synchronously, returns the raw HookOutput slice. Callers call
// MergeOutputs on the result.
//
// Each hook runs with its own per-hook timeout. Shell hooks that
// exceed the timeout are logged as non_blocking_error; the hook is
// counted as having returned an empty HookOutput (no mutation).
func (r *Runner) Fire(ctx context.Context, event string, payload any) ([]HookOutput, error) {
	if r == nil || r.registry == nil {
		return nil, nil
	}
	// Extract session/kind/model from payload if present (best-effort).
	sessionID, kind, model := extractMatchKeys(payload)
	hooks := r.registry.EnabledForEvent(event, sessionID, kind, model)
	if len(hooks) == 0 {
		return nil, nil
	}

	var outputs []HookOutput
	for _, h := range hooks {
		out, err := r.fireOne(ctx, event, h, payload)
		if err != nil {
			r.logger.Warn("hooks.fire.dispatch_failed",
				"id", h.ID, "event", event, "kind", h.Kind, "err", err.Error())
			continue
		}
		outputs = append(outputs, out)
	}
	return outputs, nil
}

// FireAsync dispatches every enabled matching hook for the event in the
// background via the worker pool. Returns immediately. Output is logged
// but not returned to the caller.
func (r *Runner) FireAsync(ctx context.Context, event string, payload any) {
	if r == nil || r.registry == nil {
		return
	}
	sessionID, kind, model := extractMatchKeys(payload)
	hooks := r.registry.EnabledForEvent(event, sessionID, kind, model)
	if len(hooks) == 0 {
		return
	}
	pool := r.lazyPool()
	for _, h := range hooks {
		h := h // capture loop variable
		// Detach from caller's context so the async hook outlives the request.
		asyncCtx := context.WithoutCancel(ctx)
		work := asyncWork{fn: func() {
			timeoutMs := h.TimeoutMs
			if timeoutMs <= 0 {
				timeoutMs = DefaultAsyncTimeoutMs
			}
			tctx, cancel := context.WithTimeout(asyncCtx, time.Duration(timeoutMs)*time.Millisecond)
			defer cancel()
			out, err := r.fireOne(tctx, event, h, payload)
			if err != nil {
				r.logger.Warn("hooks.fireasync.dispatch_failed",
					"id", h.ID, "event", event, "kind", h.Kind, "err", err.Error())
				return
			}
			r.logger.Info("hooks.fireasync.completed",
				"id", h.ID, "event", event, "decision", out.Decision,
				"additional_context_len", len(out.AdditionalContext))
		}}
		if !pool.submit(work) {
			r.logger.Warn("hooks.fireasync.pool_saturated",
				"id", h.ID, "event", event, "dropped", true)
		}
	}
}

// Shutdown drains the async pool and waits for all workers to finish.
// It should be called when the harness shuts down.
func (r *Runner) Shutdown() {
	r.poolMu.Lock()
	p := r.pool
	r.poolMu.Unlock()
	if p != nil {
		p.shutdown()
	}
}

// lazyPool returns the runner's async pool, creating it on first call.
func (r *Runner) lazyPool() *asyncPool {
	r.poolMu.Lock()
	defer r.poolMu.Unlock()
	if r.pool == nil {
		r.pool = newAsyncPool(r.asyncPoolSize)
	}
	return r.pool
}

// fireOne dispatches a single hook and returns its output.
func (r *Runner) fireOne(ctx context.Context, event string, h Hook, payload any) (HookOutput, error) {
	timeoutMs := h.EffectiveTimeoutMs()
	tctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	switch h.Kind {
	case KindShell:
		return r.fireShell(tctx, event, h, payload)
	case KindBuiltin:
		if r.builtins != nil {
			if fn, ok := r.builtins.genericFire[h.Builtin]; ok {
				return fn(tctx, event, payload, h.Config)
			}
		}
		// No v2 builtin registered for this id — not an error.
		return HookOutput{}, nil
	case KindMCP:
		if r.mcp == nil {
			return HookOutput{}, fmt.Errorf("mcp invoker not configured")
		}
		body, _ := json.Marshal(fireEnvelope{Event: event, Input: payload})
		raw, err := r.mcp.InvokeTool(tctx, h.MCPTool, body)
		if err != nil {
			return HookOutput{}, fmt.Errorf("mcp invoke %q: %w", h.MCPTool, err)
		}
		var out HookOutput
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &out); err != nil {
				return HookOutput{}, fmt.Errorf("mcp parse output: %w", err)
			}
		}
		return out, nil
	default:
		return HookOutput{}, fmt.Errorf("unknown kind %q", h.Kind)
	}
}

// fireEnvelope is the shell hook input envelope for v2 events.
// The hook reads {"event": "<name>", "input": <payload>} on stdin.
type fireEnvelope struct {
	Event string `json:"event"`
	Input any    `json:"input"`
}

// fireShell runs a shell hook for a v2 event. The hook reads
// {"event": "<name>", "input": <payload>} on stdin and must write a
// JSON HookOutput on stdout.
func (r *Runner) fireShell(ctx context.Context, event string, h Hook, payload any) (HookOutput, error) {
	envelope := fireEnvelope{Event: event, Input: payload}
	body, err := json.Marshal(envelope)
	if err != nil {
		return HookOutput{}, fmt.Errorf("marshal fire envelope: %w", err)
	}

	cmd := commandContext(ctx, h.Command)
	cmd.Stdin = bytes.NewReader(body)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 500 * time.Millisecond

	if err := cmd.Run(); err != nil {
		return HookOutput{}, fmt.Errorf("run %q: %w (stderr: %s)",
			h.Command, err, strings.TrimSpace(stderr.String()))
	}
	if stderr.Len() > 0 {
		r.logger.Warn("hooks.shell.stderr",
			"id", h.ID, "event", event, "msg", strings.TrimSpace(stderr.String()))
	}
	if stdout.Len() == 0 {
		return HookOutput{}, nil
	}
	var out HookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return HookOutput{}, fmt.Errorf("parse shell output: %w", err)
	}
	return out, nil
}

// extractMatchKeys pulls session_id, kind, model from the payload for
// EnabledForEvent matching. Uses JSON marshal/unmarshal to avoid a big
// type switch.
func extractMatchKeys(payload any) (sessionID, kind, model string) {
	if payload == nil {
		return
	}
	type keys struct {
		SessionID       string `json:"session_id"`
		ParentSessionID string `json:"parent_session_id"`
		Kind            string `json:"kind"`
		Model           string `json:"model"`
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	var k keys
	if err := json.Unmarshal(data, &k); err != nil {
		return
	}
	sessionID = k.SessionID
	if sessionID == "" {
		sessionID = k.ParentSessionID
	}
	kind = k.Kind
	model = k.Model
	return
}
