// Package monitor implements the kaneaz__monitor builtin tool
// (background-task-monitor-01KZNP3C, WP03).
//
// kaneaz__monitor subscribes to the output stream of a background task
// started with kaneaz__bash(run_in_background:true). Two modes:
//
//   - drain: return all output since the last call and return immediately.
//   - watch: block until the first condition fires:
//     • until.regex matches a line,
//     • until.exit is true and the task ends, or
//     • until.timeout_s elapses (default 300 s).
//
// The tool is Cedar-gated under ActionToolTasksMonitor (tool.tasks.monitor)
// as a default-allow passive tool that reads task state.
//
// FR references: FR-009 .. FR-013.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/tasks"
)

const (
	// ToolName is the tool identifier surfaced to the model.
	ToolName = "kaneaz__monitor"

	// defaultWatchTimeoutS is the default watch timeout in seconds.
	defaultWatchTimeoutS = 300

	// maxReturnedLines caps how many lines are included in a single response
	// to avoid exceeding token-ceiling constraints (FR-012).
	maxReturnedLines = 2000
)

// ToolDescription is the user-facing description.
const ToolDescription = "Monitor the output of a background task started with kaneaz__bash(run_in_background:true). " +
	"Use mode:\"drain\" to retrieve all output since the last call. " +
	"Use mode:\"watch\" to block until a regex matches, the task exits, or a timeout fires."

// inputSchema is the JSON Schema for kaneaz__monitor.
const inputSchema = `{
  "type": "object",
  "properties": {
    "task_id": {
      "type": "string",
      "description": "The task_id returned by a run_in_background bash call."
    },
    "mode": {
      "type": "string",
      "enum": ["drain", "watch"],
      "description": "drain: return buffered output immediately. watch: block until a condition fires."
    },
    "until": {
      "type": "object",
      "description": "Conditions for watch mode (ignored in drain mode).",
      "properties": {
        "regex": {"type": "string", "description": "Return when any output line matches this regex."},
        "exit":  {"type": "boolean", "default": true, "description": "Return when the task reaches a terminal state."},
        "timeout_s": {"type": "integer", "default": 300, "description": "Maximum seconds to wait. Default 300."}
      }
    }
  },
  "required": ["task_id", "mode"]
}`

// monitorArgs is the parsed input shape.
type monitorArgs struct {
	TaskID string `json:"task_id"`
	Mode   string `json:"mode"`
	Until  *until `json:"until,omitempty"`
}

type until struct {
	Regex     string `json:"regex,omitempty"`
	Exit      *bool  `json:"exit,omitempty"`
	TimeoutS  *int   `json:"timeout_s,omitempty"`
}

func (u *until) timeoutDuration() time.Duration {
	if u == nil || u.TimeoutS == nil {
		return defaultWatchTimeoutS * time.Second
	}
	secs := *u.TimeoutS
	if secs <= 0 {
		secs = defaultWatchTimeoutS
	}
	return time.Duration(secs) * time.Second
}

func (u *until) wantExit() bool {
	if u == nil || u.Exit == nil {
		return true // default: return on task exit
	}
	return *u.Exit
}

// monitorResult is the JSON returned to the model.
type monitorResult struct {
	TaskID    string   `json:"task_id"`
	Matched   string   `json:"matched,omitempty"` // "regex" | "exit" | "timeout" | "drained"
	Lines     []lineOut `json:"lines"`
	ExitCode  *int     `json:"exit_code,omitempty"`
	Truncated bool     `json:"truncated,omitempty"`
}

type lineOut struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
}

// errorResult is returned for user-correctable errors.
type errorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// RegistryIface is the narrow interface Tool needs from core/tasks.Registry.
// Defined here to avoid import cycle.
type RegistryIface interface {
	// Get returns the current task snapshot.
	Get(id string) (tasks.Task, bool)
	// Tail returns lines since fromOffset and the next offset.
	Tail(id string, fromOffset int64) ([]tasks.Line, int64, bool)
	// Subscribe registers a live line channel. Returns (ch, cancel, ok).
	Subscribe(id string) (<-chan tasks.Line, func(), bool)
}

// OffsetStore maps (session+task) → last-drained offset.
// The monitor tool maintains drain state per session so two sessions can
// independently drain the same task.
//
// OffsetStore is safe for concurrent use if the implementation is.
type OffsetStore interface {
	Get(sessionID, taskID string) int64
	Set(sessionID, taskID string, offset int64)
}


// Tool implements kaneaz__monitor. Safe for concurrent use.
type Tool struct {
	reg          RegistryIface
	offsets      OffsetStore
	sessionIDFn  func(ctx context.Context) string
}

// Options configures the monitor Tool.
type Options struct {
	// Registry is required. The tool returns an error result when nil.
	Registry RegistryIface
	// Offsets tracks per-session drain state. Defaults to a process-global
	// in-memory store when nil.
	Offsets OffsetStore
	// SessionIDFromCtx extracts the session ID for per-session offset
	// tracking. nil means a fixed empty session key is used (test-only).
	SessionIDFromCtx func(ctx context.Context) string
}

// processLocalOffsets is the default global store.
var processLocalOffsets = NewInMemoryOffsetStore()

// New constructs a monitor Tool.
func New(opts Options) *Tool {
	os := opts.Offsets
	if os == nil {
		os = processLocalOffsets
	}
	return &Tool{
		reg:         opts.Registry,
		offsets:     os,
		sessionIDFn: opts.SessionIDFromCtx,
	}
}

// Name returns the tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the user-facing description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON Schema.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

// Call dispatches the tool invocation.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	var args monitorArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalError("invalid_args", fmt.Sprintf("parse error: %v", err))
	}
	if args.TaskID == "" {
		return marshalError("missing_task_id", "task_id is required")
	}
	if args.Mode != "drain" && args.Mode != "watch" {
		return marshalError("invalid_mode", `mode must be "drain" or "watch"`)
	}

	if t.reg == nil {
		return marshalError("registry_unavailable", "task registry is not wired")
	}

	_, ok := t.reg.Get(args.TaskID)
	if !ok {
		return marshalError("task_not_found", fmt.Sprintf("task %q not found", args.TaskID))
	}

	sessionID := t.sessionKey(ctx)

	switch args.Mode {
	case "drain":
		return t.drain(args.TaskID, sessionID)
	case "watch":
		return t.watch(ctx, args.TaskID, sessionID, args.Until)
	}
	return marshalError("invalid_mode", "unknown mode")
}

// drain returns all output since the last drain call for this (session, task) pair.
func (t *Tool) drain(taskID, sessionID string) (json.RawMessage, error) {
	fromOffset := t.offsets.Get(sessionID, taskID)
	lines, nextOffset, ok := t.reg.Tail(taskID, fromOffset)
	if !ok {
		return marshalError("task_not_found", fmt.Sprintf("task %q not found", taskID))
	}
	t.offsets.Set(sessionID, taskID, nextOffset)

	out, truncated := convertLines(lines, maxReturnedLines)
	return marshalResult(monitorResult{
		TaskID:    taskID,
		Matched:   "drained",
		Lines:     out,
		Truncated: truncated,
	})
}

// watch blocks until a condition fires: regex match, task exit, or timeout.
func (t *Tool) watch(ctx context.Context, taskID, sessionID string, u *until) (json.RawMessage, error) {
	timeout := u.timeoutDuration()
	wantExit := u.wantExit()

	var compiled *regexp.Regexp
	if u != nil && u.Regex != "" {
		var err error
		compiled, err = regexp.Compile(u.Regex)
		if err != nil {
			return marshalError("invalid_regex",
				fmt.Sprintf("regex %q: %v", u.Regex, err))
		}
	}

	// Check if task is already terminal before subscribing.
	task, ok := t.reg.Get(taskID)
	if !ok {
		return marshalError("task_not_found", fmt.Sprintf("task %q not found", taskID))
	}

	var collectedLines []tasks.Line

	// Drain buffered output first.
	fromOffset := t.offsets.Get(sessionID, taskID)
	buffered, nextOffset, _ := t.reg.Tail(taskID, fromOffset)
	collectedLines = append(collectedLines, buffered...)
	t.offsets.Set(sessionID, taskID, nextOffset)

	// Check regex match on buffered lines.
	if compiled != nil {
		for _, ln := range buffered {
			if compiled.MatchString(ln.Text) {
				exitCode := exitCodeOf(task)
				out, truncated := convertLines(collectedLines, maxReturnedLines)
				return marshalResult(monitorResult{
					TaskID:    taskID,
					Matched:   "regex",
					Lines:     out,
					ExitCode:  exitCode,
					Truncated: truncated,
				})
			}
		}
	}

	// If task is already terminal, short-circuit.
	if task.IsTerminal() {
		if wantExit {
			exitCode := exitCodeOf(task)
			out, truncated := convertLines(collectedLines, maxReturnedLines)
			return marshalResult(monitorResult{
				TaskID:    taskID,
				Matched:   "exit",
				Lines:     out,
				ExitCode:  exitCode,
				Truncated: truncated,
			})
		}
		// Task ended but caller only wants regex (which didn't match) — timeout immediately.
		out, truncated := convertLines(collectedLines, maxReturnedLines)
		return marshalResult(monitorResult{
			TaskID:    taskID,
			Matched:   "timeout",
			Lines:     out,
			Truncated: truncated,
		})
	}

	// Subscribe to live line stream.
	ch, cancel, subOK := t.reg.Subscribe(taskID)
	if !subOK {
		// Race: task ended between Get and Subscribe.
		task2, _ := t.reg.Get(taskID)
		exitCode := exitCodeOf(task2)
		out, truncated := convertLines(collectedLines, maxReturnedLines)
		return marshalResult(monitorResult{
			TaskID:    taskID,
			Matched:   "exit",
			Lines:     out,
			ExitCode:  exitCode,
			Truncated: truncated,
		})
	}
	defer cancel()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case ln, open := <-ch:
			if !open {
				// Channel closed = task reached terminal state.
				// Drain remaining buffered lines.
				final, finalOffset, _ := t.reg.Tail(taskID, t.offsets.Get(sessionID, taskID))
				t.offsets.Set(sessionID, taskID, finalOffset)
				collectedLines = append(collectedLines, final...)

				if wantExit {
					task3, _ := t.reg.Get(taskID)
					exitCode := exitCodeOf(task3)
					out, truncated := convertLines(collectedLines, maxReturnedLines)
					return marshalResult(monitorResult{
						TaskID:    taskID,
						Matched:   "exit",
						Lines:     out,
						ExitCode:  exitCode,
						Truncated: truncated,
					})
				}
				out, truncated := convertLines(collectedLines, maxReturnedLines)
				return marshalResult(monitorResult{
					TaskID:    taskID,
					Matched:   "timeout",
					Lines:     out,
					Truncated: truncated,
				})
			}
			collectedLines = append(collectedLines, ln)
			// Update drain offset.
			t.offsets.Set(sessionID, taskID, ln.Offset+1)

			if compiled != nil && compiled.MatchString(ln.Text) {
				task4, _ := t.reg.Get(taskID)
				exitCode := exitCodeOf(task4)
				out, truncated := convertLines(collectedLines, maxReturnedLines)
				return marshalResult(monitorResult{
					TaskID:    taskID,
					Matched:   "regex",
					Lines:     out,
					ExitCode:  exitCode,
					Truncated: truncated,
				})
			}

		case <-timer.C:
			out, truncated := convertLines(collectedLines, maxReturnedLines)
			return marshalResult(monitorResult{
				TaskID:    taskID,
				Matched:   "timeout",
				Lines:     out,
				Truncated: truncated,
			})

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// sessionKey returns a stable key for the offset store.
func (t *Tool) sessionKey(ctx context.Context) string {
	if t.sessionIDFn != nil {
		return t.sessionIDFn(ctx)
	}
	return ""
}

// exitCodeOf returns a pointer to the task's exit code if terminal, or nil.
func exitCodeOf(task tasks.Task) *int {
	if task.IsTerminal() {
		ec := task.ExitCode
		return &ec
	}
	return nil
}

// convertLines converts tasks.Line to the wire format, enforcing maxLines.
func convertLines(lines []tasks.Line, maxLines int) ([]lineOut, bool) {
	truncated := false
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}
	out := make([]lineOut, len(lines))
	for i, ln := range lines {
		out[i] = lineOut{Stream: ln.Stream, Text: ln.Text, Offset: ln.Offset}
	}
	return out, truncated
}

func marshalResult(r monitorResult) (json.RawMessage, error) {
	b, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("monitor: marshal result: %w", err)
	}
	return json.RawMessage(b), nil
}

func marshalError(kind, msg string) (json.RawMessage, error) {
	b, err := json.Marshal(errorResult{Error: kind, Message: msg})
	if err != nil {
		return nil, fmt.Errorf("monitor: marshal error: %w", err)
	}
	return json.RawMessage(b), nil
}

// ─── InMemoryOffsetStore ──────────────────────────────────────────────────────

// inMemOffsetStore implements OffsetStore in memory.
type inMemOffsetStore struct {
	mu      sync.RWMutex
	offsets map[string]int64
}

// NewInMemoryOffsetStore returns a new in-memory OffsetStore.
func NewInMemoryOffsetStore() OffsetStore {
	return &inMemOffsetStore{
		offsets: make(map[string]int64),
	}
}

func (s *inMemOffsetStore) key(sessionID, taskID string) string {
	return sessionID + "::" + taskID
}

func (s *inMemOffsetStore) Get(sessionID, taskID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.offsets[s.key(sessionID, taskID)]
}

func (s *inMemOffsetStore) Set(sessionID, taskID string, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offsets[s.key(sessionID, taskID)] = offset
}
